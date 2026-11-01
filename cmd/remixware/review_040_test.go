package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Independent review of 0.4.0: GitHub output. Run with
// go test -run Review040 ./...

func review040Dir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// With --format github, deps still prints the policy's new allow rules
// (and the settings it changes) as plain text, through TerminalText, which
// keeps newlines. A reason in the change's own .remixware.json can then
// start a line with "::", and the runner reads it as a workflow command:
// a forged annotation, or ::stop-commands:: to hide the real ones.
func TestReview040AllowReasonForgesWorkflowCommand(t *testing.T) {
	lock := `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*"}}, "node_modules/a": {"version": "1.0.0"}}}`
	head := review040Dir(t, map[string]string{"package-lock.json": lock,
		".remixware.json": `{"deps": {"allow": [{"package": "npm/x", "reason": "ok\n::error file=README.md,line=1,title=Remixware::forged by the change\n::stop-commands::zz"}]}}`})
	base := review040Dir(t, map[string]string{"package-lock.json": lock})
	r := cli("deps", head, "--base", base, "--format", "github")
	for _, line := range strings.Split(r.stdout+"\n"+r.stderr, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "::") {
			t.Errorf("a line from the policy file reads as a workflow command: %q", line)
		}
	}
}

// pnpm-lock.yaml errors name importer paths and dependency names raw; a
// double-quoted YAML key can hold "\n", and the error goes to stderr,
// which the runner also reads for workflow commands.
func TestReview040LockfileErrorForgesWorkflowCommand(t *testing.T) {
	lock := "lockfileVersion: '9.0'\nimporters:\n  \"x\\n::error file=README.md::forged\":\n    dependencies: 3\n"
	head := review040Dir(t, map[string]string{"pnpm-lock.yaml": lock})
	base := review040Dir(t, map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\n"})
	r := cli("deps", head, "--base", base, "--format", "github")
	for _, line := range strings.Split(r.stdout+"\n"+r.stderr, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "::") {
			t.Errorf("a line from the lockfile reads as a workflow command: %q", line)
		}
	}
}

// GitHub's table parser treats "\\" as an escaped backslash, so the pipe
// after it ends the cell. mdCell and mdCode escape "|" but not "\", so a
// backslash before a pipe in project text splits the cell (and Markdown
// escapes like \* lose their meaning).
func TestReview040SummaryBackslashPipe(t *testing.T) {
	unescapedPipe := regexp.MustCompile(`(^|[^\\])(\\\\)*\|`)
	for name, s := range map[string]string{"mdCell": mdCell(`a\|b`), "mdCode": mdCode(`a\|b`)} {
		if unescapedPipe.MatchString(s) {
			t.Errorf("%s(`a\\|b`) = %q: the pipe ends the table cell", name, s)
		}
	}
	if s := mdCell(`\*\*bold\*\*`); regexp.MustCompile(`(^|[^\\])(\\\\)*\*`).MatchString(s) {
		t.Errorf("mdCell(`\\*\\*bold\\*\\*`) = %q: an unescaped * is left", s)
	}
}
