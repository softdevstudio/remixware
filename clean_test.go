package remixware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Text cleaning takes out what a terminal acts on and nothing else, so
// copyright lines and names still match where they came from.
func TestOneLine(t *testing.T) {
	keep := []string{
		"Copyright (c) 2019  Jane Doe", // two spaces, as in the GPL's template
		"Copyright\t2019 Jane Doe",
		"Copyright 2020 م‌ن همه", // a ZWNJ inside a Persian name
		"csv　copy.go", "a b.go", "co­operate", "é", "中文名称", "MIT OR Apache-2.0",
		strings.Repeat("GPL-3.0-only AND ", 30) + "MIT", // long expressions aren't cut
	}
	for _, s := range keep {
		if got := oneLine(s); got != s {
			t.Errorf("oneLine(%q) = %q, want it unchanged", s, got)
		}
	}
	change := map[string]string{
		"a\x1b[2Jb":   "a [2Jb",
		"x\ny\r\nz":   "x y  z",
		"\u009b31m":   " 31m",
		"\x9b31m":     "\ufffd31m",
		"r‮o⁦k":       "r o k",
		"l m n":       "l m n",
		"bad\xffbyte": "bad�byte",
		"del\x7f":     "del ",
	}
	for in, want := range change {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
	if got := textBlock("Line one\r\nLine\x1b[8m two\n\ttab‮"); got != "Line one\nLine[8m two\n\ttab" {
		t.Errorf("textBlock = %q", got)
	}
	if got := TerminalText("ok\n\tline\x1b]0;x\x07 ‮"); got != "ok\n\tline�]0;x� �" {
		t.Errorf("TerminalText = %q", got)
	}
}

func TestImportPath(t *testing.T) {
	for _, p := range []string{"csv　copy.go", "a b.go", "a‌b.go", "a­b.go", "a\tb.go", "dir/中.go"} {
		if got, ok := importPath(p); !ok || got != p {
			t.Errorf("importPath(%q) = %q, %v; want it kept", p, got, ok)
		}
	}
	for _, p := range []string{"a\x1bb.go", "a\nb.go", "a‮b.go", "a\u0085b.go", "a b.go", "a\xffb.go", "../a.go", "/etc/passwd", "c:x.go"} {
		if _, ok := importPath(p); ok {
			t.Errorf("importPath(%q) kept it", p)
		}
	}
}

func TestMarkdownLine(t *testing.T) {
	cases := map[string]string{
		"Copyright (c) 2020 Jane Doe <jane@example.com>": "Copyright (c) 2020 Jane Doe <jane@example.com>",
		"## Everything below is fine":                    `\## Everything below is fine`,
		"<!-- the rest is hidden":                        `\<!-- the rest is hidden`,
		"   > quoted":                                    `\> quoted`,
		"- item":                                         `\- item`,
		"1. first":                                       `1\. first`,
		"2019) Acme":                                     `2019\) Acme`,
		"2019 Acme":                                      "2019 Acme",
		"```":                                            "\\```",
		"| a | b |":                                      `\| a | b |`,
		"(c) 2020 Acme":                                  "(c) 2020 Acme",
		"[link](javascript:x)":                           `\[link](javascript:x)`,
	}
	for in, want := range cases {
		if got := mdLine(in); got != want {
			t.Errorf("mdLine(%q) = %q, want %q", in, got, want)
		}
	}
	if got := codeSpan("a`b"); got != "``a`b``" {
		t.Errorf("codeSpan = %q", got)
	}
	if got := codeSpan("`x"); got != "`` `x ``" {
		t.Errorf("codeSpan = %q", got)
	}
}

// A file whose name holds a character from everyday text keeps its findings
// through a findings file, and the SPDX document reads the real file.
func TestUnicodeFileNames(t *testing.T) {
	dir := t.TempDir()
	name := "csv　copy.go"
	body := []byte("package csv\n\n// Copy copies.\nfunc Copy() int { return 1 }\n")
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewResults(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := Source{Key: "gplthing@1", Component: "gplthing", Version: "1", License: "GPL-3.0-only", Via: "index",
		Copyrights: []string{"Copyright (c) 2019  Jane Doe"}}
	res.Add([]Finding{{File: name, Lines: Range{1, 4}, Source: src, Match: Match{Kind: "verbatim", Coverage: 1}}}, Input{Kind: "index", Path: "x.rmx"}, nil)
	Evaluate(res, nil, "MIT")
	var js bytes.Buffer
	if err := WriteJSON(&js, res); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(path, js.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	found, in, _, err := ImportResults(path, dir)
	if err != nil || in.Dropped != 0 || len(found) != 1 || found[0].File != name || found[0].Source.Copyrights[0] != "Copyright (c) 2019  Jane Doe" {
		t.Fatalf("import: %v, %+v, %+v", err, in, found)
	}
	var spdx bytes.Buffer
	if err := WriteSPDX(&spdx, res); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(spdx.String(), "da39a3ee5e6b4b0d3255bfef95601890afd80709") {
		t.Errorf("the SPDX document has the checksum of an empty file for %q", name)
	}
}

// A results file with entries that can't be read whole says how many it
// left out.
func TestImportDropped(t *testing.T) {
	dir := t.TempDir()
	match := `[{"id": "file", "lines": "all", "matched": "100%", "component": "x", "licenses": [{"name": "GPL-3.0-only", "source": "license_file"}]}]`
	results := fmt.Sprintf(`{"ok.go": %s, "c:gpl.go": %s, "../up.go": %s, "esc\u001b.go": %s, "none/../../x.go": [{"id": "none"}]}`, match, match, match, match)
	path := filepath.Join(dir, "scanoss.json")
	if err := os.WriteFile(path, []byte(results), 0o644); err != nil {
		t.Fatal(err)
	}
	found, in, _, err := ImportResults(path, dir)
	if err != nil || len(found) != 1 || in.Dropped != 3 {
		t.Errorf("%v: %d findings, %d dropped; want 1 and 3", err, len(found), in.Dropped)
	}
}

// Other spellings of a path count toward the same limit and the same single
// read of the file.
func TestImportPathAliases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := `{"id": "file", "lines": "all", "matched": "100%", "component": "c", "file": "f"}`
	list := func(n int) json.RawMessage {
		return json.RawMessage("[" + strings.TrimSuffix(strings.Repeat(one+",", n), ",") + "]")
	}
	reads := 0
	old := countFileLines
	countFileLines = func(p string) int { reads++; return old(p) }
	defer func() { countFileLines = old }()
	found, _, err := scanossFindings(map[string]json.RawMessage{"a.go": list(30), "./a.go": list(30), "x/../a.go": list(30)}, "aliases", dir, false)
	if err != nil || len(found) != 90 || reads != 1 {
		t.Errorf("%v: %d findings, the file read %d times; want 90 and once", err, len(found), reads)
	}
	if _, _, err := scanossFindings(map[string]json.RawMessage{"a.go": list(60), "./a.go": list(60)}, "aliases", dir, false); err == nil ||
		!strings.Contains(err.Error(), "more than 100 matches") {
		t.Errorf("120 matches for one file through two spellings: %v", err)
	}
	many := `{"id": "file", "lines": "1-2", "component": "c", "copyrights": [` + strings.TrimSuffix(strings.Repeat(`{"name": "Copyright X"},`, 101), ",") + `]}`
	if _, _, err := scanossFindings(map[string]json.RawMessage{"a.go": json.RawMessage("[" + many + "]")}, "many", dir, false); err == nil ||
		!strings.Contains(err.Error(), "lists more than 100") {
		t.Errorf("101 copyright lines: %v", err)
	}

	// A findings file may give one file at most 1,000 findings.
	f := map[string]any{"tool": "remixware 0.2.0", "findings": []map[string]any{}}
	for i := 0; i < 1001; i++ {
		f["findings"] = append(f["findings"].([]map[string]any), map[string]any{"file": "a.go", "lines": []int{1, 1},
			"source": map[string]any{"component": fmt.Sprint("c", i), "license": "MIT"}})
	}
	data, _ := json.Marshal(f)
	path := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ImportResults(path, dir); err == nil || !strings.Contains(err.Error(), "more than 1000 findings") {
		t.Errorf("1,001 findings for one file: %v", err)
	}
}

// An index from elsewhere can't put terminal commands or extra lines into
// what Remixware prints.
func TestIndexTextCleaned(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "node_modules", "evil")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "evil\u001b[2J\nPASS: all clear", "version": "1.0‮", "license": "MIT"}`
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	code := "// Copyright (c) 2020 Evil\u001b[8m Corp\nfunction add(a, b) { return a + b }\n"
	if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := BuildIndex([]string{filepath.Join(dir, "node_modules")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "evil.rmx")
	if err := ix.Save(out); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range loaded.Components {
		all := fmt.Sprintf("%q %q %q %q", c.Key, c.Name, c.Version, c.Copyrights)
		if unsafeText(c.Key + c.Name + c.Version + c.License + strings.Join(c.Copyrights, "")) {
			t.Errorf("component text not cleaned: %s", all)
		}
	}
}

// A copyright line can't start a Markdown block in the notices file.
func TestNoticeMarkdown(t *testing.T) {
	dir := t.TempDir()
	res, err := NewResults(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := Source{Key: "tool@1", Component: "tool", Version: "1", License: "MIT", Via: "scanoss",
		Copyrights: []string{"## Everything below is fine", "<!-- the rest is hidden", "Copyright (c) 2020 Jane Doe"}}
	res.Add([]Finding{{File: "a`b.go", Lines: Range{1, 9}, Source: src, Match: Match{Kind: "file", Coverage: 1}}}, Input{Kind: "scanoss", Path: "s.json"}, nil)
	Evaluate(res, nil, "MIT")
	out := Notices(res, "", false)
	for _, want := range []string{`\## Everything below is fine`, `\<!-- the rest is hidden`, "Copyright (c) 2020 Jane Doe", "``a`b.go`` lines 1-9"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("notices lack %q:\n%s", want, out.Text)
		}
	}
	if strings.Contains(out.Text, "\n## Everything") || strings.Contains(out.Text, "\n<!--") {
		t.Errorf("a copyright line starts a Markdown block:\n%s", out.Text)
	}
}
