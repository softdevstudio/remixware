package main

import (
	"strings"
	"testing"
	"unicode"
)

// Re-check of the 0.4.0 review fixes (14c2f13). Run with
// go test -run Review040b ./...

// GitHub's runner reads a workflow command after trimming the line's
// leading white space with .NET's TrimStart(), which removes every Unicode
// white space character (U+00A0, U+2003, U+3000...). The terminal guard
// trims only spaces and tabs, and TerminalText keeps those characters, so
// a policy reason with "\n ::error::..." still forges a command.
func TestReview040bUnicodeSpaceBeforeCommand(t *testing.T) {
	lock := `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*"}}, "node_modules/a": {"version": "1.0.0"}}}`
	head := review040Dir(t, map[string]string{"package-lock.json": lock,
		".remixware.json": `{"deps": {"allow": [{"package": "npm/x", "reason": "ok\n ::error file=README.md::forged\n　::stop-commands::zz"}]}}`})
	base := review040Dir(t, map[string]string{"package-lock.json": lock})
	r := cli("deps", head, "--base", base, "--format", "github")
	for _, line := range strings.Split(r.stdout+"\n"+r.stderr, "\n") {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "::") && !strings.HasPrefix(line, "::error file=") {
			t.Errorf("a line from the policy file reads as a workflow command after TrimStart: %q", line)
		}
	}
}
