package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/softdevstudio/remixware"
)

// annotation matches a whole workflow command line as GitHub reads it.
var annotation = regexp.MustCompile(`^::(error|warning) ([^:]*)::(.*)$`)

func annotations(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "::") {
			if !annotation.MatchString(line) {
				t.Errorf("not a well-formed workflow command: %q", line)
			}
			got = append(got, line)
		}
	}
	return got
}

func TestCheckFormatGitHub(t *testing.T) {
	ix := upstreamIndex(t)
	sum := filepath.Join(t.TempDir(), "summary.md")
	r := cli("check", fixture("projects", "shopfront"), "--index", ix, "--format", "github", "--summary", sum)
	expect(t, r, exitFail, "FAIL:")
	got := annotations(t, r.stdout)
	if len(got) != 8 {
		t.Errorf("%d annotations, want 8 (the failing findings):\n%s", len(got), strings.Join(got, "\n"))
	}
	want := "::error file=" + filepath.ToSlash(fixture("projects", "shopfront", "internal", "files", "order.go")) + ",line=7,endLine=46,title=Remixware%3A conflict::Copied from example.org/gplsort (GPL-3.0-or-later)."
	found := false
	for _, a := range got {
		if strings.HasPrefix(a, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("no annotation starting %q in:\n%s", want, strings.Join(got, "\n"))
	}
	if strings.Contains(r.stdout, "CONFLICT ") {
		t.Error("--format github still prints the text blocks for findings")
	}
	// The summary is appended, so two runs leave two sections.
	cli("check", fixture("projects", "clean"), "--index", ix, "--summary", sum)
	b, err := os.ReadFile(sum)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "### Remixware check: FAIL") || !strings.Contains(s, "### Remixware check: PASS") {
		t.Errorf("summary:\n%s", s)
	}
	if !strings.Contains(s, "| conflict | `"+filepath.ToSlash(fixture("projects", "shopfront", "internal", "files", "order.go"))+":7-46` | example.org/gplsort | GPL-3.0-or-later |") {
		t.Errorf("summary row missing:\n%s", s)
	}
	expect(t, cli("check", fixture("projects", "clean"), "--index", ix, "--format", "json"), exitUsage, "--format")
}

func TestDepsFormatGitHub(t *testing.T) {
	sum := filepath.Join(t.TempDir(), "summary.md")
	r := cli("deps", fixture("deps", "head"), "--base", fixture("deps", "base"), "--format", "github", "--summary", sum)
	expect(t, r, exitFail, "FAIL: 3 changes break")
	got := annotations(t, r.stdout)
	if len(got) != 3 {
		t.Fatalf("%d annotations, want 3:\n%s", len(got), strings.Join(got, "\n"))
	}
	lock := filepath.ToSlash(fixture("deps", "head", "web", "package-lock.json"))
	wants := []string{
		"::error file=" + lock + ",line=11,title=Remixware deps%3A install-script npm/esbuild::updated 0.19.2 -> 0.21.5. Runs an install script",
		"::error file=" + lock + ",line=16,title=Remixware deps%3A not-allowed npm/is-thirteen::added 2.0.0. New, and not on the allow list.",
	}
	for _, w := range wants {
		ok := false
		for _, a := range got {
			if strings.HasPrefix(a, w) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("no annotation starting %q in:\n%s", w, strings.Join(got, "\n"))
		}
	}
	b, _ := os.ReadFile(sum)
	s := string(b)
	for _, w := range []string{"### Remixware deps: FAIL", "| install-script | npm/esbuild | updated | 0.19.2 -&gt; 0.21.5 | `" + lock + "` |",
		"- allow list: ran", "- install scripts: partly"} {
		if !strings.Contains(s, w) {
			t.Errorf("summary lacks %q:\n%s", w, s)
		}
	}
	// Failures come first in the table.
	if strings.Index(s, "\n| updated |") < strings.Index(s, "\n| not-allowed |") {
		t.Errorf("failures aren't first:\n%s", s)
	}
	expect(t, cli("deps", fixture("deps", "head"), "--base", fixture("deps", "base"), "--format", "xml"), exitUsage, "--format")
}

// TestGitHubOutputCleansText: text from an index, a project or a registry
// can't end a workflow command or start another, and can't add Markdown or
// HTML to a summary.
func TestGitHubOutputCleansText(t *testing.T) {
	var b bytes.Buffer
	ghAnnotation(&b, "error", "a,b:c.go\n::warning::x", 3, 5, "t\r\n::error::y", "line one\n::error file=x::forged\x1b[31m 100%0A")
	out := b.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("not one line: %q", out)
	}
	if !annotation.MatchString(strings.TrimSuffix(out, "\n")) {
		t.Errorf("not well-formed: %q", out)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\r") {
		t.Errorf("control characters kept: %q", out)
	}
	if !strings.Contains(out, "file=a%2Cb%3Ac.go %3A%3Awarning%3A%3Ax,line=3,endLine=5,title=t %3A%3Aerror%3A%3Ay::") {
		t.Errorf("file property not escaped: %q", out)
	}
	if !strings.Contains(out, "100%250A") {
		t.Errorf("percent not escaped: %q", out)
	}
	cell := mdCell("x | <img src=x onerror=alert(1)> **bold** [link](http://e) `code`\n# head")
	for _, bad := range []string{"<img", " | ", "**", "[link](", "`", "\n"} {
		if strings.Contains(cell, bad) {
			t.Errorf("mdCell kept %q: %q", bad, cell)
		}
	}
	if c := mdCode("a`b|c"); c != "`a'b\\|c`" {
		t.Errorf("mdCode: %q", c)
	}
	f := remixware.Finding{File: "x.go", Status: "conflict", Source: remixware.Source{Component: "evil\n::error::forged"}, Reason: "r"}
	b.Reset()
	checkAnnotations(&b, ".", []remixware.Finding{f}, nil)
	// GitHub reads a command only at the start of a line.
	if lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n"); len(lines) != 1 {
		t.Errorf("a component name started another line: %q", b.String())
	}
}

// TestTerminalGuardAcrossWrites: a line split over several writes can't
// start a workflow command either.
func TestTerminalGuardAcrossWrites(t *testing.T) {
	var b bytes.Buffer
	w := newTerminal(&b)
	for _, s := range []string{"ok\n", "  ", "　", "::error::x\n", ":", ":warning::y\n", "a :: b\n"} {
		w.Write([]byte(s))
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), ":") {
			t.Errorf("line reads as a command: %q", line)
		}
	}
	if !strings.Contains(b.String(), "a :: b\n") {
		t.Errorf("ordinary text changed: %q", b.String())
	}
}
