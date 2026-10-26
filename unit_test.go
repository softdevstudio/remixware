package remixware

import (
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func streams(toks []token) (norm, exact []uint64) {
	for _, t := range toks {
		norm = append(norm, t.norm)
		exact = append(exact, t.exact)
	}
	return
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTokenizeDropsCommentsAndLayout(t *testing.T) {
	a := `package p

// Sum adds the numbers.
func Sum(xs []int) int {
	total := 0 /* running total */
	for _, x := range xs {
		total += x
	}
	return total
}
`
	b := "package p\nfunc Sum(xs []int) int { total := 0; for _, x := range xs { total += x }\n return total }"
	ta, tb := tokenize([]byte(a), synGo), tokenize([]byte(b), synGo)
	na, ea := streams(ta)
	nb, eb := streams(tb)
	// b has two semicolons that a doesn't: compare without them.
	semi := fnvHash('p', []byte(";"))
	var nb2, eb2 []uint64
	for i := range nb {
		if nb[i] != semi {
			nb2 = append(nb2, nb[i])
			eb2 = append(eb2, eb[i])
		}
	}
	if !equalU64(na, nb2) || !equalU64(ea, eb2) {
		t.Fatalf("comments and layout changed the tokens:\n%v\n%v", na, nb2)
	}
	if ta[0].line != 1 || ta[len(ta)-1].line != 10 {
		t.Errorf("lines: first %d, last %d", ta[0].line, ta[len(ta)-1].line)
	}
}

func TestTokenizeLanguages(t *testing.T) {
	count := func(src string, sx *syntax) int { return len(tokenize([]byte(src), sx)) }
	cases := []struct {
		name string
		sx   *syntax
		a, b string // must tokenize the same
	}{
		{"python comments and docstrings", synPython, "def f(x):\n    \"\"\"Doc\n    more\"\"\"\n    return x # c\n", "def f(x):\n    'other'\n    return x\n"},
		{"python string prefixes", synPython, "x = rb'abc'\n", "x = 'zzzzz'\n"},
		{"python prefixed docstring", synPython, "def f():\n    r\"\"\"Doc \"quoted\" text.\"\"\"\n    return 1\n", "def f():\n    'x'\n    return 1\n"},
		{"js template literal", synJS, "const s = `a ${b} c`;\n", "const s = 'q';\n"},
		{"shell hash in variables", synShell, "n=${#arr}\necho $# # count\n", "n=${#arr}\necho $#\n"},
		{"sql case and comments", synSQL, "SELECT id FROM t -- all\n", "select id from t\n"},
		{"ruby block comment", synRuby, "=begin\nanything\n=end\nx = 1\n", "x = 1\n"},
		{"lua block comment", synLua, "--[[ long\ncomment ]] local x = 1\n", "local x = 1\n"},
		{"rust nested comments", synRust, "/* a /* b */ c */ fn f() {}\n", "fn f() {}\n"},
	}
	for _, c := range cases {
		na, ea := streams(tokenize([]byte(c.a), c.sx))
		nb, _ := streams(tokenize([]byte(c.b), c.sx))
		if !equalU64(na, nb) {
			t.Errorf("%s: %q and %q tokenize differently (%d vs %d tokens)", c.name, c.a, c.b, len(na), len(nb))
		}
		_ = ea
	}
	// Rust lifetimes are not char literals that swallow the line.
	if n := count("fn f<'a>(x: &'a str) -> &'a str { x }", synRust); n < 18 {
		t.Errorf("rust lifetimes: %d tokens", n)
	}
	if n := count("let c = 'x'; let d = 'y';", synRust); n != 10 {
		t.Errorf("rust chars: %d tokens, want 10", n)
	}
}

func TestTokenizeNeverHangsOrPanics(t *testing.T) {
	inputs := []string{"\"unterminated", "/* never closed", "`raw", "'''py", "x = '\\", "\xff\xfe\x00", "#!", "=begin"}
	for _, in := range inputs {
		for _, sx := range []*syntax{synGo, synC, synJS, synPython, synRust, synRuby, synShell, synSQL, synLua, synPHP} {
			tokenize([]byte(in), sx)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		b := make([]byte, rng.Intn(400))
		rng.Read(b)
		for _, sx := range []*syntax{synGo, synPython, synJS, synRust, synRuby} {
			toks := tokenize(b, sx)
			winnow(toks, sx)
		}
	}
}

func FuzzTokenize(f *testing.F) {
	f.Add([]byte("func f() { return \"x\" } // c"), 0)
	f.Add([]byte("def f():\n    '''d'''\n    return 1"), 1)
	f.Fuzz(func(t *testing.T, data []byte, which int) {
		langs := []*syntax{synGo, synPython, synJS, synRust, synRuby, synShell}
		sx := langs[(which%len(langs)+len(langs))%len(langs)]
		toks := tokenize(data, sx)
		for i := 1; i < len(toks); i++ {
			if toks[i].line < toks[i-1].line {
				t.Fatalf("lines go backwards")
			}
		}
		winnow(toks, sx)
	})
}

func TestNamesAreNumberedNotErased(t *testing.T) {
	// Same flow of names, different names: same k-grams.
	a := "a, b := f(a, c); if b != nil { return a, b }; g(c, a, b)"
	b := "x, y := h(x, z); if y != nil { return x, y }; k(z, x, y)"
	// Same shape, different flow of names: different k-grams.
	c := "a, b := f(b, c); if a != nil { return b, a }; g(a, c, b)"
	ha, _ := rawKgramHashes(tokenize([]byte(a), synGo))
	hb, _ := rawKgramHashes(tokenize([]byte(b), synGo))
	hc, _ := rawKgramHashes(tokenize([]byte(c), synGo))
	if len(ha) == 0 || !equalU64(ha, hb) {
		t.Fatalf("consistent renaming changed the hashes")
	}
	if equalU64(ha, hc) {
		t.Fatalf("a different flow of names gave the same hashes")
	}
}

// Any shared run of at least W+K-1 tokens produces a shared fingerprint.
func TestWinnowingGuarantee(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	randomToks := func(n int) []token {
		out := make([]token, n)
		for i := range out {
			v := uint64(rng.Int63()) | 1
			out[i] = token{norm: v, exact: v, line: int32(i/8 + 1)}
		}
		return out
	}
	need := windowSize + kgramSize - 1
	for trial := 0; trial < 200; trial++ {
		shared := randomToks(need + rng.Intn(10))
		a := append(append(randomToks(rng.Intn(200)), shared...), randomToks(rng.Intn(200))...)
		b := append(append(randomToks(rng.Intn(200)), shared...), randomToks(rng.Intn(200))...)
		fa, fb := winnow(a, synC), winnow(b, synC)
		set := map[uint64]bool{}
		for _, f := range fa {
			set[f.hash] = true
		}
		found := false
		for _, f := range fb {
			if set[f.hash] {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("trial %d: a shared run of %d tokens produced no shared fingerprint", trial, len(shared))
		}
	}
}

// A table of near-identical lines only gets fingerprints where it starts and
// ends, so a longer table adds nothing that could chain into a match.
func TestRepetitiveCodeIsNotFingerprinted(t *testing.T) {
	count := func(rows int) int {
		var b strings.Builder
		b.WriteString("package p\nfunc table() {\n")
		for i := 0; i < rows; i++ {
			b.WriteString("\tregister(\"name\", handlerFor(\"name\"), 10, true)\n")
		}
		b.WriteString("}\n")
		return len(winnow(tokenize([]byte(b.String()), synGo), synGo))
	}
	short, long := count(12), count(80)
	if long != short || long > 4 {
		t.Errorf("12 rows gave %d fingerprints, 80 rows gave %d", short, long)
	}
}

func TestSortInterfaceIdiomIsIgnored(t *testing.T) {
	src := `type byAge []Person

func (a byAge) Len() int           { return len(a) }
func (a byAge) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byAge) Less(i, j int) bool { return a[i].Age < a[j].Age }
`
	if fps := winnow(tokenize([]byte(src), synGo), synGo); len(fps) != 0 {
		t.Errorf("sort.Interface boilerplate gave %d fingerprints", len(fps))
	}
}

func TestDetectLicense(t *testing.T) {
	text := func(id string) string {
		b, err := licenseTextFS.ReadFile("licensetexts/" + id + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct{ want, text string }{
		{"MIT", text("MIT")},
		{"BSD-2-Clause", text("BSD-2-Clause")},
		{"BSD-3-Clause", text("BSD-3-Clause")},
		{"ISC", text("ISC")},
		{"Zlib", text("Zlib")},
		{"BSL-1.0", text("BSL-1.0")},
		{"Apache-2.0", text("Apache-2.0")},
		{"MPL-2.0", text("MPL-2.0")},
		{"GPL-2.0-only", "This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License version 2 as published by the Free Software Foundation."},
		{"GPL-2.0-or-later", "under the terms of the GNU General Public License as published by the Free Software Foundation; either version 2 of the License, or (at your option) any later version."},
		{"GPL-3.0-or-later", "under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version."},
		{"GPL-3.0-only", "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n...\nTERMS AND CONDITIONS\n... either version 3 of the License, or (at your option) any later version."},
		{"LGPL-2.1-or-later", "under the terms of the GNU Lesser General Public License as published by the Free Software Foundation; either version 2.1 of the License, or (at your option) any later version. See the GNU General Public License"},
		{"LGPL-3.0-only", "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\nThis version of the GNU Lesser General Public License incorporates the terms and conditions of version 3 of the GNU General Public License"},
		{"AGPL-3.0-or-later", "it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version."},
		{"Unlicense", "This is free and unencumbered software released into the public domain."},
		{"SSPL-1.0", "Server Side Public License\nVERSION 1, OCTOBER 16, 2018"},
		{"BUSL-1.1", "Business Source License 1.1\nLicensor: Example"},
		{"MIT-0", "Permission is hereby granted, free of charge, to any person obtaining a copy of this software, to deal in the Software without restriction."},
		{"0BSD", "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted.\nTHE SOFTWARE IS PROVIDED \"AS IS\""},
		{"Apache-2.0", "// SPDX-License-Identifier: Apache-2.0\npackage x"},
		{"MIT OR Apache-2.0", "# SPDX-License-Identifier: MIT OR Apache-2.0"},
		{"", "Copyright 2020 Someone. All rights reserved."},
		{"", "Use of this source code is governed by a BSD-style license that can be found in the LICENSE file."},
	}
	for _, c := range cases {
		if got := DetectLicense(c.text); got != c.want {
			t.Errorf("DetectLicense(%.60q...) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestLicenseExpressions(t *testing.T) {
	cases := map[string]string{
		"mit":                                  "MIT",
		"MIT or Apache-2.0":                    "MIT OR Apache-2.0",
		"GPL-2.0+":                             "GPL-2.0-or-later",
		"GPL-3.0":                              "GPL-3.0-only",
		"(MIT AND BSD-3-Clause) OR Apache-2.0": "(MIT AND BSD-3-Clause) OR Apache-2.0",
		"GPL-2.0-only WITH Classpath-exception-2.0": "GPL-2.0-only WITH Classpath-exception-2.0",
		"MIT/Apache-2.0":      "MIT OR Apache-2.0",
		"UNLICENSED":          LicenseProprietary,
		"Some Custom Licence": "Some Custom Licence",
	}
	for in, want := range cases {
		if got := NormalizeLicense(in); got != want {
			t.Errorf("NormalizeLicense(%q) = %q, want %q", in, got, want)
		}
	}
	if got := joinLicenses([]string{"MIT", "MIT", "Apache-2.0"}); got != "Apache-2.0 AND MIT" {
		t.Errorf("joinLicenses = %q", got)
	}
}

func TestCompatibility(t *testing.T) {
	cases := []struct{ src, proj, want string }{
		{"MIT", "MIT", baseAttribution},
		{"MIT", "NONE", baseAttribution},
		{"Unlicense", "GPL-2.0-only", baseOK},
		{"Apache-2.0", "GPL-2.0-only", baseConflict},
		{"Apache-2.0", "GPL-3.0-or-later", baseAttribution},
		{"GPL-3.0-or-later", "MIT", baseConflict},
		{"GPL-3.0-only", "GPL-2.0-only", baseConflict},
		{"GPL-2.0-or-later", "GPL-3.0-only", baseAttribution},
		{"GPL-2.0-only", "GPL-3.0-only", baseConflict},
		{"GPL-2.0-only", "GPL-2.0-or-later", baseReview},
		{"GPL-3.0-only", "GPL-2.0-or-later", baseReview},
		{"AGPL-3.0-or-later", "MIT", baseConflict},
		{"AGPL-3.0-only", "GPL-3.0-only", baseReview},
		{"AGPL-3.0-only", "AGPL-3.0-or-later", baseAttribution},
		{"LGPL-2.1-only", "MIT", baseReview},
		{"LGPL-2.1-only", "GPL-3.0-only", baseAttribution},
		{"LGPL-3.0-only", "GPL-2.0-only", baseConflict},
		{"MPL-2.0", "Apache-2.0", baseReview},
		{"MPL-2.0", "GPL-2.0-only", baseAttribution},
		{"EPL-1.0", "GPL-3.0-only", baseConflict},
		{"BSD-4-Clause", "GPL-2.0-only", baseConflict},
		{"SSPL-1.0", "MIT", baseConflict},
		{"BUSL-1.1", "AGPL-3.0-only", baseConflict},
		{"NONE", "MIT", baseNoLicense},
		{"NOASSERTION", "MIT", baseReview},
		{"MIT OR GPL-3.0-only", "Apache-2.0", baseAttribution},
		{"MIT AND GPL-3.0-only", "Apache-2.0", baseConflict},
		{"MIT", "MIT OR Apache-2.0", baseAttribution},
		{"Apache-2.0", "MIT OR GPL-2.0-only", baseConflict},
	}
	for _, c := range cases {
		if got := evaluate(c.src, c.proj); got.status != c.want {
			t.Errorf("%s into %s: %s (%s), want %s", c.src, c.proj, got.status, got.reason, c.want)
		}
	}
	if r := evaluate("GPL-3.0-only", "MIT").reason; !strings.Contains(r, "an MIT project") {
		t.Errorf("reason reads %q", r)
	}
}

func TestCopyrightLines(t *testing.T) {
	text := `MIT License

Copyright (c) <year> <copyright holders>
Copyright (c) 2019-2024 Ada Lovelace
// Copyright 2009 The Go Authors. All rights reserved.
THE SOFTWARE IS PROVIDED "AS IS"... COPYRIGHT HOLDERS BE LIABLE
The above copyright notice and this permission notice shall be included
Copyright [yyyy] [name of copyright owner]
(c) 2001 Charles Babbage
Copyright 2014 CloudFlare. All rights reserved. Use of this source code is
`
	got := copyrightLines(text, 10)
	want := []string{"Copyright (c) 2019-2024 Ada Lovelace", "Copyright 2009 The Go Authors. All rights reserved.", "(c) 2001 Charles Babbage",
		"Copyright 2014 CloudFlare. All rights reserved."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("copyrightLines = %q", got)
	}
	if h := copyrightHolder("Copyright (c) 2019-2024 Ada Lovelace. All rights reserved."); h != "Ada Lovelace" {
		t.Errorf("copyrightHolder = %q", h)
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestManifests(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		file, content               string
		name, version, license, url string
	}{
		{"a/package.json", `{"name":"@acme/x","version":"1.2.3","license":"MIT","repository":"github:acme/x"}`, "@acme/x", "1.2.3", "MIT", "https://github.com/acme/x"},
		{"b/package.json", `{"name":"y","license":{"type":"ISC"},"repository":{"url":"git+https://gitlab.com/a/y.git"}}`, "y", "", "ISC", "https://gitlab.com/a/y"},
		{"c/package.json", `{"name":"z","licenses":[{"type":"MIT"},{"type":"Apache-2.0"}]}`, "z", "", "MIT OR Apache-2.0", ""},
		{"d/package.json", `{"name":"w","license":"SEE LICENSE IN LICENSE.txt"}`, "w", "", "", ""},
		{"e/Cargo.toml", "[package]\nname = \"crab\"\nversion = \"0.3.0\"\nlicense = \"MIT OR Apache-2.0\"\nrepository = \"https://github.com/r/crab\"\n[dependencies]\nlicense = \"nope\"\n", "crab", "0.3.0", "MIT OR Apache-2.0", "https://github.com/r/crab"},
		{"f/pyproject.toml", "[project]\nname = \"snake\"\nversion = \"2.0\"\nlicense = {text = \"BSD-3-Clause\"}\n", "snake", "2.0", "BSD-3-Clause", ""},
		{"g/composer.json", `{"name":"vendor/pkg","license":["MIT","GPL-2.0-or-later"]}`, "vendor/pkg", "", "MIT OR GPL-2.0-or-later", ""},
		{"github.com/user/mod@v1.4.0/go.mod", "module github.com/user/mod/v2 // comment\n\ngo 1.21\n", "github.com/user/mod/v2", "v1.4.0", "", "https://github.com/user/mod"},
	}
	for _, c := range cases {
		p := writeTemp(t, dir, c.file, c.content)
		m := readManifest(filepath.Dir(p), filepath.Base(p))
		if m == nil {
			t.Errorf("%s: no manifest read", c.file)
			continue
		}
		if m.name != c.name || m.version != c.version || m.license != c.license || m.url != c.url {
			t.Errorf("%s: got %q %q %q %q", c.file, m.name, m.version, m.license, m.url)
		}
	}
	di := filepath.Dir(writeTemp(t, dir, "site/pkg-1.0.dist-info/METADATA", "Name: pkg\nVersion: 1.0\nClassifier: License :: OSI Approved :: GNU General Public License v3 or later (GPLv3+)\n\nBody text\n"))
	m, _ := readDistInfo(di)
	if m.name != "pkg" || m.license != "GPL-3.0-or-later" || len(m.topLevel) != 1 || m.topLevel[0] != "pkg" {
		t.Errorf("dist-info: %+v", m)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"testdata/**", "testdata/a/b.go", true},
		{"testdata/", "testdata/x.go", true},
		{"*.min.js", "web/app.min.js", true},
		{"web/*.js", "web/app.js", true},
		{"web/*.js", "web/sub/app.js", false},
		{"**/gen/**", "a/gen/b/c.go", true},
		{"vendor", "src/vendor/x.go", true},
		{"internal/legacy/**", "internal/legacy2/x.go", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.path); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v", c.pattern, c.path, got)
		}
	}
}

func TestParseRanges(t *testing.T) {
	if r := parseRanges("12-40"); len(r) != 1 || r[0] != (Range{12, 40}) {
		t.Errorf("12-40: %v", r)
	}
	if r := parseRanges("1-10,25-30"); len(r) != 2 || span(r) != (Range{1, 30}) {
		t.Errorf("multi: %v", r)
	}
	if r := parseRanges("all"); len(r) != 0 {
		t.Errorf("all: %v", r)
	}
	if r := parseRanges("7"); len(r) != 1 || r[0] != (Range{7, 7}) {
		t.Errorf("single: %v", r)
	}
	if r := parseRanges("x-3,40-10"); len(r) != 0 {
		t.Errorf("bad: %v", r)
	}
}

func TestSkipReason(t *testing.T) {
	if r := skipReason([]byte("// Code generated by protoc. DO NOT EDIT.\npackage x\n")); r != "generated" {
		t.Errorf("generated: %q", r)
	}
	if r := skipReason([]byte("a\x00b")); r != "binary" {
		t.Errorf("binary: %q", r)
	}
	if r := skipReason([]byte(strings.Repeat("var a=1;", 2000))); r != "minified" {
		t.Errorf("minified: %q", r)
	}
	if r := skipReason([]byte("package x\n")); r != "" {
		t.Errorf("plain: %q", r)
	}
}

func TestLicenceFiles(t *testing.T) {
	dir := t.TempDir()
	mit, _ := licenseTextFS.ReadFile("licensetexts/MIT.txt")
	cases := []struct {
		name, text, license string
		copyrights          int
	}{
		{"LICENSE", string(mit), "MIT", 0},
		{"LICENSE", "MIT", "MIT", 0},
		{"LICENSE", "Apache-2.0\n", "Apache-2.0", 0},
		{"license.rst", "BSD-3-Clause License\n====================\n\n.. include:: ../LICENSE.rst\n", "", 0},
		{"COPYING", "This application and all the files in this repository are\n  Copyright (C) 2015-2021 The Example Authors\n", "", 1},
		{"LICENSE.md", "BSD-3-Clause License\n\nSee the repository root for the full text.\n", "BSD-3-Clause", 0},
		{"LICENSE", "You may use this software only for good, under the terms below.\nNo warranty.", LicenseUnrecognised, 0},
		{"COPYRIGHT", "Copyright 2009 The Go Authors. All rights reserved. Use of this source code\nis governed by a BSD-style license that can be found in the LICENSE file.\nExtensions of the original work are copyright (c) 2011 Miek Gieben\n\nCopyright 2014 CloudFlare. All rights reserved. Use of this source code is\ngoverned by a BSD-style license that can be found in the LICENSE file.\n", "", 2},
	}
	for i, c := range cases {
		p := writeTemp(t, dir, filepath.Join(string(rune('a'+i)), c.name), c.text)
		expr, _, _, cr := licenseFromFiles([]string{p})
		if expr != c.license || len(cr) != c.copyrights {
			t.Errorf("%s %.40q: licence %q with %d copyright lines, want %q with %d", c.name, c.text, expr, len(cr), c.license, c.copyrights)
		}
	}
}

func TestComponentNames(t *testing.T) {
	dir := t.TempDir()
	mit, _ := licenseTextFS.ReadFile("licensetexts/MIT.txt")
	code := "package x\n\nfunc F(a, b int) int {\n\tif a > b {\n\t\treturn a - b\n\t}\n\treturn b - a\n}\n"
	writeTemp(t, dir, "app/LICENSE", string(mit))
	writeTemp(t, dir, "app/main.go", code)
	writeTemp(t, dir, "app/docs/license.rst", "License\n=======\n\n.. include:: ../LICENSE\n")
	writeTemp(t, dir, "app/docs/conf.go", code)
	writeTemp(t, dir, "app/vendor/github.com/acme/tool/LICENSE", string(mit))
	writeTemp(t, dir, "app/vendor/github.com/acme/tool/tool.go", code)
	writeTemp(t, dir, "app/third_party/logrus/LICENSE", string(mit))
	writeTemp(t, dir, "app/third_party/logrus/a.go", code+"// a\n")
	writeTemp(t, dir, "lib/vendor/github.com/acme/tool/LICENSE", string(mit))
	writeTemp(t, dir, "lib/vendor/github.com/acme/tool/other.go", code+"// other\n")
	ix, err := BuildIndex([]string{filepath.Join(dir, "app"), filepath.Join(dir, "lib")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range ix.Components {
		keys = append(keys, c.Key)
	}
	sort.Strings(keys)
	want := []string{"app", "app/third_party/logrus",
		"github.com/acme/tool (app/vendor/github.com/acme/tool)", "github.com/acme/tool (lib/vendor/github.com/acme/tool)"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Errorf("keys:\n%q\nwant:\n%q", keys, want)
	}
	for _, f := range ix.Files {
		if strings.HasSuffix(f.Path, "conf.go") && ix.Components[f.Comp].Name != "app" {
			t.Errorf("docs/conf.go belongs to %s", ix.Components[f.Comp].Name)
		}
	}
}

// A file checked out with Windows line endings tokenizes like the original.
func TestWindowsLineEndings(t *testing.T) {
	cases := []struct {
		sx  *syntax
		src string
	}{
		{synGo, "package p\n\n// c\nfunc F(a int) int {\n\treturn a + 1 // x\n}\n\nvar s = `raw\nline`\n"},
		{synPython, "def f(x):\n    '''doc\n    more'''\n    return x # c\n"},
		{synC, "/* a\n   b */\nint f(void) {\n    return 1;\n}\n"},
	}
	for _, c := range cases {
		a := tokenize([]byte(c.src), c.sx)
		b := tokenize([]byte(strings.ReplaceAll(c.src, "\n", "\r\n")), c.sx)
		if len(a) != len(b) {
			t.Fatalf("%q: %d vs %d tokens", c.src, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("%q: token %d differs", c.src, i)
			}
		}
	}
	mit, _ := licenseTextFS.ReadFile("licensetexts/MIT.txt")
	if DetectLicense(strings.ReplaceAll(string(mit), "\n", "\r\n")) != "MIT" {
		t.Errorf("MIT text with Windows line endings not detected")
	}
}

func TestCredit(t *testing.T) {
	plain := Source{Key: "errors", Component: "errors", URL: "https://github.com/pkg/errors", License: "BSD-2-Clause",
		Copyrights: []string{"Copyright (c) 2015, Dave Cheney <dave@cheney.net>"}}
	cases := []struct {
		notice string
		want   bool
	}{
		{"This program reports errors to the log.", false},
		{"## errors\n\n- Source: errors\n- Licence: BSD-2-Clause", true},
		{"Includes code from https://github.com/pkg/errors under the BSD licence.", true},
		{"Copyright (c) 2015, Dave Cheney <dave@cheney.net>\nBSD 2-Clause", true},
		{"Thanks to Dave Cheney.", false},
	}
	for _, c := range cases {
		if got := credited(strings.ToLower(c.notice), plain); got != c.want {
			t.Errorf("%q: credited %v, want %v", c.notice, got, c.want)
		}
	}
	versioned := Source{Key: "click@8.1.7", Component: "click", License: "BSD-3-Clause"}
	if !credited("uses click@8.1.7 (bsd-3-clause)", versioned) || credited("click the button", versioned) {
		t.Errorf("versioned key credit wrong")
	}
}

func TestJoinPieces(t *testing.T) {
	piece := func(a, b, sa, sb, tokens int) Finding {
		return Finding{File: "x.go", Lines: Range{a, b}, Match: Match{Kind: "verbatim", Tokens: tokens, Coverage: 1},
			Source: Source{Key: "k", Component: "k", File: "s.go", Lines: Range{sa, sb}, Via: "index"}}
	}
	cases := []struct {
		name  string
		in    []Finding
		lines []Range
	}{
		{"touching", []Finding{piece(1, 20, 1, 20, 100), piece(21, 40, 21, 40, 100)}, []Range{{1, 40}}},
		{"lines removed from the copy", []Finding{piece(1, 20, 1, 20, 100), piece(22, 40, 50, 68, 100)}, []Range{{1, 40}}},
		{"lines added to the copy", []Finding{piece(1, 20, 1, 20, 100), piece(50, 70, 21, 41, 100)}, []Range{{1, 70}}},
		{"overlapping edges, lined up", []Finding{piece(1, 31, 1, 29, 175), piece(27, 36, 29, 36, 56)}, []Range{{1, 36}}},
		{"same code twice in the source", []Finding{piece(36, 47, 335, 346, 54), piece(36, 47, 357, 368, 54)}, []Range{{36, 47}, {36, 47}}},
		{"far apart", []Finding{piece(1, 20, 1, 20, 100), piece(200, 220, 200, 220, 100)}, []Range{{1, 20}, {200, 220}}},
		{"source order reversed", []Finding{piece(1, 20, 100, 120, 100), piece(21, 40, 1, 20, 100)}, []Range{{1, 20}, {21, 40}}},
	}
	for _, c := range cases {
		out := joinPieces(c.in)
		var got []Range
		for _, f := range out {
			got = append(got, f.Lines)
		}
		if len(got) != len(c.lines) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.lines)
			continue
		}
		for i := range got {
			if got[i] != c.lines[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.lines)
			}
		}
		if c.name == "lines removed from the copy" && out[0].Match.Kind != "edited" {
			t.Errorf("%s: kind %s, want edited", c.name, out[0].Match.Kind)
		}
	}
}
