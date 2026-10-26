package remixware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
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
		// A quote, // or /* inside a regular expression must not start a
		// string or a comment that swallows the code after it.
		{"js regex with a quote", synJS, "x = s.replace(/'/g, \"\\\\'\");\ny = a / b;\n", "x = s.replace(/q/g, 'z');\ny = a / b;\n"},
		{"js regex with slashes", synJS, "ok = /^https?:\\/\\//.test(u);\nn = 2;\n", "ok = /x/.test(u);\nn = 2;\n"},
		{"js regex with a comment start", synJS, "p = p.replace(/\\/*$/, '');\nq = 1;\n", "p = p.replace(/x/, '');\nq = 1;\n"},
		{"js regex class and keyword", synJS, "return /[/\"]/i.test(s);\n", "return /x/.test(s);\n"},
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
	// Division stays division: after a name, a number, a closing bracket or n++.
	for _, src := range []string{"a = b / 2; c = d / 3;\n", "x = (a) / b / c;\n", "y = arr[i] / n++ / 2;\n", "z = this / 2 / 3;\n"} {
		for _, k := range tokenize([]byte(src), synJS) {
			if k.norm == hashRegex {
				t.Errorf("%q: division read as a regular expression", src)
			}
		}
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
			out[i] = token{norm: v, exact: v, line: int32(i/8 + 1), keyword: true}
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
		// Licences that name the GPL further on, in a compatibility list.
		{"EUPL-1.2", "EUROPEAN UNION PUBLIC LICENCE v. 1.2\nEUPL © the European Union 2007, 2016\nThis European Union Public Licence (the 'EUPL') applies to the Work.\nAppendix\n'Compatible Licences' according to Article 5 EUPL are:\n- GNU General Public License (GPL) v. 2, v. 3"},
		{"EUPL-1.1", "European Union Public Licence\nV. 1.1\nEUPL © the European Community 2007\nAppendix\n'Compatible Licences' are: GNU General Public License (GNU GPL) v. 2"},
		{"CeCILL-2.1", "CeCILL FREE SOFTWARE LICENSE AGREEMENT\n\nVersion 2.1 dated 2013-06-21\n\nNotice\n5.3.4. COMPATIBILITY WITH THE GNU GPL: the GNU General Public License version 2 or 3"},
		{"CeCILL-B", "CeCILL-B FREE SOFTWARE LICENSE AGREEMENT\n\nNotice\nThis Agreement is a Free Software license agreement"},
		{"CeCILL-C", "CONTRAT DE LICENCE DE LOGICIEL LIBRE CeCILL-C\n\nAvertissement"},
		// Licences that start like others.
		{"Apache-1.1", "The Apache Software License, Version 1.1\n\nCopyright (c) 2000 The Apache Software Foundation.  All rights reserved.\n\nRedistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:"},
		{"CDDL-1.0", "COMMON DEVELOPMENT AND DISTRIBUTION LICENSE (CDDL) Version 1.0\n\n1. Definitions.\n\n1.1. Contributor means each individual or entity that creates Modifications."},
		{"CDDL-1.1", "COMMON DEVELOPMENT AND DISTRIBUTION LICENSE (CDDL)\nVersion 1.1\n\n1. Definitions.\n\n1.1. Contributor means each individual or entity that creates Modifications."},
		{"MIT", "Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the \"Software\"), to deal in the Software without restriction.\n\nThe above copyright notice and this permission notice (including the next paragraph) shall be included in all copies or substantial portions of the Software."},
		{"X11", "Permission is hereby granted, free of charge, to any person obtaining a copy of this software. The above copyright notice and this permission notice shall be included in all copies.\nExcept as contained in this notice, the name of the X Consortium shall not be used in advertising."},
		{"JSON", "Copyright (c) 2002 JSON.org\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software. The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.\n\nThe Software shall be used for Good, not Evil."},
		{"Unicode-3.0", "UNICODE LICENSE V3\n\nCOPYRIGHT AND PERMISSION NOTICE\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of data files and any associated documentation (the \"Data Files\") or software, provided that either (a) this copyright and permission notice appear with all copies of the Data Files or Software, or (b) this copyright and permission notice appear in associated Documentation."},
		{"Unicode-DFS-2016", "UNICODE, INC. LICENSE AGREEMENT - DATA FILES AND SOFTWARE\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of the Unicode data files and any associated documentation (the \"Data Files\"), provided that either (a) this copyright and permission notice appear with all copies of the Data Files or Software."},
		{"HPND", "Permission to use, copy, modify and distribute this software and its documentation for any purpose and without fee is hereby granted, provided that the above copyright notice appear in all copies."},
		// A full GPL text says "or any later version" in its "how to apply"
		// appendix; only a preface before the terms makes it "or later".
		{"GPL-2.0-only", "GNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991\nPreamble\nTERMS AND CONDITIONS FOR COPYING, DISTRIBUTION AND MODIFICATION\n0. This License applies\nHow to Apply These Terms\neither version 2 of the License, or (at your option) any later version."},
		{"GPL-2.0-or-later", "This program is licensed under the GNU General Public License, version 2 or (at your option) any later version.\n\nGNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991\nTERMS AND CONDITIONS FOR COPYING, DISTRIBUTION AND MODIFICATION\nHow to Apply These Terms\neither version 2 of the License, or (at your option) any later version."},
		// The Commons Clause is a condition on the licence it comes with,
		// wherever it is in the file.
		{"Apache-2.0 AND LicenseRef-Commons-Clause", text("Apache-2.0") + "\n\n" + commonsClauseText},
		{"Apache-2.0 AND LicenseRef-Commons-Clause", commonsClauseText + "\n\n" + text("Apache-2.0")},
		{"LicenseRef-Commons-Clause", commonsClauseText},
	}
	for _, c := range cases {
		if got := DetectLicense(c.text); got != c.want {
			t.Errorf("DetectLicense(%.60q...) = %q, want %q", c.text, got, c.want)
		}
	}
}

const commonsClauseText = `"Commons Clause" License Condition v1.0

The Software is provided to you by the Licensor under the License, as defined
below, subject to the following condition.

Without limiting other conditions in the License, the grant of rights under the
License will not include, and the License does not grant to you, the right to
Sell the Software.`

// Expressions compare by what they say, not how they are written, and a
// licence joined to itself is there once.
func TestCanonicalLicences(t *testing.T) {
	same := [][2]string{
		{"MIT OR Apache-2.0", "Apache-2.0 OR MIT"},
		{"(MIT OR Apache-2.0) AND Zlib", "Zlib AND (Apache-2.0 OR MIT)"},
		{"MIT AND MIT", "MIT"},
		{"MIT AND (MIT OR GPL-2.0-only)", "MIT"},
		{"MIT OR (MIT AND GPL-2.0-only)", "MIT"},
		{"(Apache-2.0 OR MIT) AND (Apache-2.0 OR MIT OR Zlib)", "Apache-2.0 OR MIT"},
		{"mit or apache-2.0", "MIT OR Apache-2.0"},
	}
	for _, c := range same {
		if !sameLicense(c[0], c[1]) {
			t.Errorf("%q and %q differ: %q, %q", c[0], c[1], canonicalLicense(c[0]), canonicalLicense(c[1]))
		}
	}
	if sameLicense("MIT", "MIT OR Apache-2.0") || sameLicense("MIT AND GPL-2.0-only", "MIT") {
		t.Errorf("different licences compare equal")
	}
	joins := map[string][]string{
		"BSD-3-Clause AND GPL-2.0-only":     {"BSD-3-Clause AND GPL-2.0-only", "BSD-3-Clause"},
		"BSD-3-Clause":                      {"BSD-3-Clause", "BSD-3-Clause OR GPL-2.0-only"},
		"(Apache-2.0 OR MIT) AND Zlib":      {"Zlib", "MIT OR Apache-2.0", "Apache-2.0 OR MIT"},
		LicenseNone:                         {"", " "},
		"LicenseRef-Commons-Clause AND MIT": {"MIT", "", "LicenseRef-Commons-Clause"},
	}
	for want, in := range joins {
		if got := joinLicenses(in); got != want {
			t.Errorf("joinLicenses(%q) = %q, want %q", in, got, want)
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
		{"Apache-2.0", "GPL-2.0-or-later", baseReview},  // fits only under GPL-3.0
		{"Apache-2.0", "LGPL-2.1-or-later", baseReview}, // fits only under LGPL-3.0
		{"Apache-2.0", "LGPL-2.1-only", baseConflict},
		{"Apache-2.0", "LGPL-3.0-only", baseAttribution},
		{"Apache-1.1", "GPL-3.0-only", baseConflict},
		{"Apache-1.1", "MIT", baseAttribution},
		{"GPL-3.0-only", "LGPL-2.1-or-later", baseConflict},
		{"GPL-3.0-or-later", "MIT", baseConflict},
		{"GPL-3.0-only", "GPL-2.0-only", baseConflict},
		{"GPL-2.0-or-later", "GPL-3.0-only", baseAttribution},
		{"GPL-2.0-only", "GPL-3.0-only", baseConflict},
		{"GPL-2.0-only", "GPL-2.0-or-later", baseReview},
		{"GPL-3.0-only", "GPL-2.0-or-later", baseReview},
		{"AGPL-3.0-or-later", "MIT", baseConflict},
		{"AGPL-3.0-only", "GPL-3.0-only", baseReview},
		{"AGPL-3.0-only", "AGPL-3.0-or-later", baseReview}, // the project can't move past 3.0
		{"AGPL-3.0-or-later", "AGPL-3.0-only", baseAttribution},
		{"GPL-3.0-only", "GPL-3.0-or-later", baseReview},
		{"LGPL-2.1-only", "LGPL-2.1-or-later", baseReview},
		{"GPL-1.0-only", "GPL-2.0-only", baseConflict},
		{"GPL-2.0-only", "GPL-1.0-only", baseConflict},
		{"GPL-1.0-or-later", "GPL-3.0-only", baseAttribution},
		{"GPL-2.0-or-later", "GPL-1.0-or-later", baseReview},
		{"GPL-3.0-only", "EUPL-1.2", baseConflict},
		{"EUPL-1.2", "EUPL-1.2", baseAttribution},
		{"EUPL-1.2", "GPL-3.0-only", baseReview},
		{"CeCILL-2.1", "GPL-3.0-only", baseReview},
		{"CeCILL-2.1", "MIT", baseConflict},
		{"CeCILL-B", "MIT", baseAttribution},
		{"LicenseRef-Proprietary", "LicenseRef-Proprietary", baseReview},
		{"LicenseRef-Proprietary", "NONE", baseReview},
		{"LicenseRef-Proprietary", "MIT", baseConflict},
		{"Apache-2.0 WITH Commons-Clause", "MIT", baseConflict},
		{"Apache-2.0 WITH LLVM-exception", "MIT", baseAttribution},
		{"JSON", "MIT", baseReview},
		{"LGPL-2.1-only", "LGPL-3.0-only", baseReview},
		{"LGPL-3.0-only", "LGPL-2.1-only", baseConflict},
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
	if r := evaluate("Apache-2.0", "GPL-2.0-or-later").reason; !strings.Contains(r, "version 3") || !strings.Contains(r, "GPL-3.0 accepts") {
		t.Errorf("Apache into GPL-2.0-or-later reads %q", r)
	}
	if r := evaluate("Apache-2.0", "LGPL-2.1-or-later").reason; !strings.Contains(r, "LGPL-3.0 accepts") {
		t.Errorf("Apache into LGPL-2.1-or-later reads %q", r)
	}
	if r := evaluate("GPL-3.0-only", "LGPL-2.1-or-later").reason; !strings.Contains(r, "unless the whole program is released under GPL-3.0-only") {
		t.Errorf("GPL into LGPL reads %q", r)
	}
	if r := evaluate("GPL-3.0-only", "EUPL-1.2").reason; strings.Contains(r, "can't be combined") {
		t.Errorf("GPL into EUPL reads %q", r)
	}
	if r := evaluate("LicenseRef-Proprietary", "NONE").reason; !strings.Contains(r, "same owner") {
		t.Errorf("proprietary into unlicensed reads %q", r)
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

// A file's header can add to its component's licence or make it more
// precise, but never hide a stricter one. Only comments count.
func TestFileHeaderLicences(t *testing.T) {
	const rust = "// Licensed under the Apache License, Version 2.0 <LICENSE-APACHE or\n" +
		"// http://www.apache.org/licenses/LICENSE-2.0> or the MIT license\n" +
		"// <LICENSE-MIT or http://opensource.org/licenses/MIT>, at your\n" +
		"// option. This file may not be copied, modified, or distributed\n" +
		"// except according to those terms.\npub fn f() {}\n"
	const gplLater = "/* This program is free software; you can redistribute it and/or modify it under the terms of\n" +
		" * the GNU General Public License as published by the Free Software Foundation; either version 2\n" +
		" * of the License, or (at your option) any later version. */\nint x;\n"
	cases := []struct{ name, comp, file, src, want string }{
		{"a mention of where the code came from", "GPL-3.0-or-later", "a.go",
			"// Based on code from fastsort, released under the MIT license.\npackage x\n", "GPL-3.0-or-later AND MIT"},
		{"a GPL file inside an MIT project", "MIT", "b.c", gplLater, "GPL-2.0-or-later AND MIT"},
		{"a dual header that matches Cargo.toml", "MIT OR Apache-2.0", "c.rs", rust, ""},
		{"a dual header and no other licence", "NONE", "d.rs", rust, "Apache-2.0 OR MIT"},
		{"or later makes the full text precise", "GPL-2.0-only", "e.c", gplLater, "GPL-2.0-or-later"},
		{"the component's own licence", "Apache-2.0", "f.go", "// Licensed under the Apache License, Version 2.0 (the \"License\");\npackage x\n", ""},
		{"the only licence there is", "NONE", "g.py", "# Licensed under the MIT License.\nx = 1\n", "MIT"},
		{"only where the code came from", "NONE", "h.py", "# Adapted from code released under the MIT License.\nx = 1\n", ""},
		{"licence words in code", "MIT", "i.go", "package x\n\nconst notice = \"Licensed under the GNU General Public License\"\n", ""},
		{"an SPDX tag in code", "MIT", "j.go", "package x\n\nconst tag = \"SPDX-License-Identifier: GPL-3.0-only\"\n", ""},
		{"an SPDX tag in the header", "MIT", "k.go", "// SPDX-License-Identifier: GPL-2.0-only OR MIT\npackage x\n", "GPL-2.0-only OR MIT"},
		{"a python docstring after a shebang", "NONE", "l.py", "#!/usr/bin/env python\n\"\"\"Licensed under the Apache License, Version 2.0.\"\"\"\nimport os\n", "Apache-2.0"},
		{"a PHP header", "NONE", "m.php", "<?php\n/*\n * Licensed under the MIT License.\n */\nfunction f() {}\n", "MIT"},
		{"a C header after an include guard", "NONE", "n.h", "#ifndef X_H\n#define X_H\n/* Licensed under the MIT License. */\nint x;\n", "MIT"},
		{"the old Mozilla tri-licence", "MPL-2.0", "p.c", mozillaTri, "(GPL-2.0-or-later OR LGPL-2.1-or-later OR MPL-1.1) AND MPL-2.0"},
		{"BSD, or alternatively the GPL", "BSD-3-Clause", "q.c", bsdOrGPL, "BSD-3-Clause OR GPL-2.0-only"},
		{"either the GPL or the CDDL", "CDDL-1.0", "r.java", gplOrCDDL, "CDDL-1.0 OR GPL-2.0-only"},
		{"a word about origins in another sentence", "NONE", "s.js",
			"/*!\n * oldjs v1.2\n * Copyright (c) 2012 Jane Roe\n * Originally written for the Foo project.\n * Released under the MIT license.\n */\nvar x = 1;\n", "MIT"},
		{"portions of the copyright", "NONE", "t.go",
			"// Copyright (c) 2015 Sam Poe. Portions copyright (c) 2014 Lee Doe.\n// Licensed under the MIT License.\npackage x\n", "MIT"},
		{"a header after 'use strict'", "NONE", "u.js",
			"'use strict';\n/**\n * tinylib v2.1.0\n * Released under the MIT license.\n */\nfunction f() {}\n", "MIT"},
		{"Apache's warranty wording is no choice", "NONE", "v.go",
			"// Licensed under the Apache License, Version 2.0 (the \"License\"). Distributed on an \"AS IS\" BASIS,\n" +
				"// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n// Its tables are under the MIT License.\npackage x\n",
			"Apache-2.0 AND MIT"},
	}
	dir := t.TempDir()
	for _, c := range cases {
		p := writeTemp(t, dir, c.file, c.src)
		r := fingerprintSourceFile(pendingFile{abs: p, rel: c.file}, c.comp, false)
		if r.sf == nil {
			t.Fatalf("%s: not fingerprinted (%s)", c.name, r.skip)
		}
		if r.sf.License != c.want {
			t.Errorf("%s: file licence %q, want %q", c.name, r.sf.License, c.want)
		}
	}
	// --license sets the component's licence; a file's own header still
	// counts, SPDX tags included.
	up := filepath.Join(dir, "up")
	writeTemp(t, up, "o.c", gplLater)
	writeTemp(t, up, "w.go", "// SPDX-License-Identifier: GPL-2.0-only\npackage x\n")
	ix, err := BuildIndex([]string{up}, IndexOptions{License: "LicenseRef-Proprietary"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range ix.Files {
		got[f.Path] = f.License
	}
	if got["o.c"] != "GPL-2.0-or-later AND LicenseRef-Proprietary" || got["w.go"] != "GPL-2.0-only AND LicenseRef-Proprietary" {
		t.Errorf("--license: file licences %q", got)
	}
	// What --license names can't be swapped for a laxer tag either.
	writeTemp(t, up, "w.go", "// SPDX-License-Identifier: MIT\npackage x\n")
	ix, err = BuildIndex([]string{up}, IndexOptions{License: "GPL-3.0-only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ix.Files {
		if f.Path == "w.go" && f.License != "GPL-3.0-only AND MIT" {
			t.Errorf("--license GPL-3.0-only, tag MIT: %q", f.License)
		}
	}
}

const mozillaTri = `/* ***** BEGIN LICENSE BLOCK *****
 * Version: MPL 1.1/GPL 2.0/LGPL 2.1
 *
 * The contents of this file are subject to the Mozilla Public License Version
 * 1.1 (the "License"); you may not use this file except in compliance with
 * the License. You may obtain a copy of the License at
 * http://www.mozilla.org/MPL/
 *
 * Alternatively, the contents of this file may be used under the terms of
 * either the GNU General Public License Version 2 or later (the "GPL"), or
 * the GNU Lesser General Public License Version 2.1 or later (the "LGPL"),
 * in which case the provisions of the GPL or the LGPL are applicable instead
 * of those above.
 * ***** END LICENSE BLOCK ***** */
int x;
`

const bsdOrGPL = `/*
 * Copyright (c) 2004, Example Networks
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions
 * are met:
 * 1. Redistributions of source code must retain the above copyright
 *    notice, this list of conditions and the following disclaimer.
 * 2. Neither the name of the copyright holder nor the names of its
 *    contributors may be used to endorse or promote products derived from
 *    this software without specific prior written permission.
 *
 * Alternatively, this software may be distributed under the terms of the
 * GNU General Public License ("GPL") version 2 as published by the Free
 * Software Foundation.
 */
int x;
`

const gplOrCDDL = `/*
 * Copyright (c) 1997-2010 Oracle and/or its affiliates. All rights reserved.
 *
 * The contents of this file are subject to the terms of either the GNU
 * General Public License Version 2 only ("GPL") or the Common Development
 * and Distribution License("CDDL") (collectively, the "License").  You
 * may not use this file except in compliance with the License.
 */
class X {}
`

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

	// Several licence files: all apply, unless each names one licence of a
	// choice in its file name, or a statement says the project is dual
	// licensed.
	apache, _ := licenseTextFS.ReadFile("licensetexts/Apache-2.0.txt")
	unlicense := "This is free and unencumbered software released into the public domain.\n"
	gpl := "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\nTERMS AND CONDITIONS\n0. Definitions.\n"
	sets := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"rust style", map[string]string{"LICENSE-MIT": string(mit), "LICENSE-APACHE": string(apache)}, "Apache-2.0 OR MIT"},
		{"dual statement", map[string]string{"COPYING": "This project is dual-licensed under the Unlicense and MIT licenses.\n\nYou may use this code under the terms of either license.\n",
			"LICENSE-MIT": string(mit), "UNLICENSE": unlicense}, "MIT OR Unlicense"},
		{"licence and third-party licence", map[string]string{"LICENSE": gpl, "LICENSE-THIRD-PARTY": string(mit)}, "GPL-3.0-only AND MIT"},
		{"two plain files", map[string]string{"LICENSE": string(mit), "COPYING": gpl}, "GPL-3.0-only AND MIT"},
	}
	for i, set := range sets {
		var paths []string
		for name, text := range set.files {
			paths = append(paths, writeTemp(t, dir, filepath.Join(fmt.Sprintf("set%d", i), name), text))
		}
		sort.Strings(paths)
		if expr, _, _, _ := licenseFromFiles(paths); expr != set.want {
			t.Errorf("%s: licence %q, want %q", set.name, expr, set.want)
		}
	}
}

// A Cargo workspace member takes the workspace's licence.
func TestCargoWorkspaceLicence(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "Cargo.toml", "[workspace]\nmembers = [\"crates/foo\"]\n\n[workspace.package]\nlicense = \"MIT OR Apache-2.0\"\nversion = \"0.4.0\"\n")
	p := writeTemp(t, dir, "crates/foo/Cargo.toml", "[package]\nname = \"foo\"\nversion.workspace = true\nlicense = { workspace = true }\n")
	writeTemp(t, dir, "crates/foo/src/lib.rs", "pub fn f() -> u32 { 1 }\n")
	m := readManifest(filepath.Dir(p), "Cargo.toml")
	m.fromWorkspace(dir)
	if m == nil || m.license != "MIT OR Apache-2.0" || m.version != "0.4.0" {
		t.Errorf("workspace member: %+v", m)
	}
	licenceOf := func(root, name string) *Component {
		t.Helper()
		ix, err := BuildIndex([]string{root}, IndexOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range ix.Components {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no component %s in %+v", name, ix.Components)
		return nil
	}
	if c := licenceOf(dir, "foo"); c.License != "MIT OR Apache-2.0" || c.Version != "0.4.0" {
		t.Errorf("indexing the workspace: %s %s", c.License, c.Version)
	}
	// Indexing the crates folder alone reads nothing above it: the crate's
	// own licence file is what there is.
	writeTemp(t, dir, "crates/foo/COPYING", "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n\nTERMS AND CONDITIONS\n")
	if c := licenceOf(filepath.Join(dir, "crates"), "foo"); c.License != "GPL-3.0-only" {
		t.Errorf("indexing below the workspace: %s from %s", c.License, c.LicenseFrom)
	}
	// A licence file named in a manifest counts only inside the indexed
	// folder.
	writeTemp(t, dir, "secret/license.key", "Licensed to ACME. Permission granted to XXXX-SECRET-KEY.\n")
	writeTemp(t, dir, "vendor/evil/Cargo.toml", "[package]\nname = \"evil\"\nlicense-file = \"../../secret/license.key\"\n")
	writeTemp(t, dir, "vendor/evil/src/lib.rs", "pub fn g() -> u32 { 2 }\n")
	if c := licenceOf(filepath.Join(dir, "vendor"), "evil"); strings.Contains(c.LicenseText, "SECRET") || c.License != LicenseNone {
		t.Errorf("a licence file outside the indexed folder was read: %s %q", c.License, c.LicenseText)
	}
}

// Two Python distributions that install into one namespace folder each get
// their own files, by their RECORD; files no RECORD lists take both
// licences.
func TestPythonNamespacePackages(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "site-packages")
	body := "def read_token(path):\n    with open(path) as f:\n        data = f.read()\n    return data.strip()\n"
	writeTemp(t, sp, "google_auth-2.0.0.dist-info/METADATA", "Name: google-auth\nVersion: 2.0.0\nLicense: Apache 2.0\n\n")
	writeTemp(t, sp, "google_auth-2.0.0.dist-info/top_level.txt", "google\n")
	writeTemp(t, sp, "google_auth-2.0.0.dist-info/RECORD", "google/auth/creds.py,sha256=x,10\ngoogle_auth-2.0.0.dist-info/METADATA,,\n")
	writeTemp(t, sp, "protobuf-4.0.0.dist-info/METADATA", "Name: protobuf\nVersion: 4.0.0\nLicense: BSD-3-Clause\n\n")
	writeTemp(t, sp, "protobuf-4.0.0.dist-info/top_level.txt", "google\n")
	writeTemp(t, sp, "protobuf-4.0.0.dist-info/RECORD", "google/protobuf/msg.py,sha256=y,10\n")
	writeTemp(t, sp, "google/auth/creds.py", body)
	writeTemp(t, sp, "google/protobuf/msg.py", body+"\n# protobuf\n")
	writeTemp(t, sp, "google/stray.py", body+"\n# stray\n")
	ix, err := BuildIndex([]string{sp}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range ix.Files {
		got[filepath.ToSlash(f.Path)] = ix.Components[f.Comp].Name + " " + ix.Components[f.Comp].License
	}
	want := map[string]string{"auth/creds.py": "google-auth Apache-2.0", "protobuf/msg.py": "protobuf BSD-3-Clause",
		"stray.py": "site-packages/google Apache-2.0 AND BSD-3-Clause"}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("%s: %q, want %q (all: %v)", f, got[f], w, got)
		}
	}
}

// Indexing never follows a symbolic link out of the tree: a NOTICE that
// links to a secret must not end up in the index or in a notice.
func TestSymlinkedLicenceFilesAreNotRead(t *testing.T) {
	dir := t.TempDir()
	secret := writeTemp(t, dir, "secret.txt", "AWS_SECRET_ACCESS_KEY=hunter2\nPermission is hereby granted, free of charge\n")
	up := filepath.Join(dir, "up")
	writeTemp(t, up, "tool/main.go", "package main\n\nfunc main() {}\n")
	for _, name := range []string{"NOTICE", "LICENSE"} {
		if err := os.Symlink(secret, filepath.Join(up, "tool", name)); err != nil {
			t.Skip("no symbolic links here:", err)
		}
	}
	ix, err := BuildIndex([]string{up}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ix.Components {
		if strings.Contains(c.NoticeText+c.LicenseText, "hunter2") || c.License == "MIT-0" || c.License == "MIT" {
			t.Errorf("%s read the linked file: licence %q, notice %q", c.Name, c.License, c.NoticeText)
		}
	}
	if readSmall("/dev/zero", 1<<20) != "" {
		t.Error("readSmall read a device")
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
	// A longer name that starts the same is another project.
	tool := Source{Key: "github.com/acme/tool", Component: "github.com/acme/tool", URL: "https://github.com/acme/tool", License: "Apache-2.0"}
	for notice, want := range map[string]bool{
		"## github.com/acme/tool-extras\n- source: github.com/acme/tool-extras": false,
		"see https://github.com/acme/tool.":                                     true,
		"code from github.com/acme/tool/v2/internal":                            true,
		"evil.github.com/acme/tool":                                             false,
	} {
		if got := credited(notice, tool); got != want {
			t.Errorf("%q: credited %v, want %v", notice, got, want)
		}
	}
	// An entry remixware notice left unfinished isn't credit yet.
	unfinished := "## errors\n\n- source: errors\n\ncopyright: not known. copy the copyright line from the source before you ship this.\n"
	if credited(unfinished, plain) || !credited(unfinished+"\n## errors again\n- source: errors\n", plain) {
		t.Errorf("unfinished entries counted as credit, or finished ones didn't")
	}
}

// A notice carries the text of every licence the copy is under, not just
// the component's own licence file.
func TestNoticeCarriesEveryLicenceText(t *testing.T) {
	mit := standardLicenseText("MIT")
	res := &Results{Findings: []Finding{{File: "a.go", Lines: Range{1, 20}, Status: StatusAttributionMissing,
		Source: Source{Key: "x", Component: "x", License: "Apache-2.0 AND MIT", Copyrights: []string{"Copyright 2020 X Ltd"}}}},
		Texts: map[string]*ComponentTexts{"x": {LicenseText: mit}}}
	nr := Notices(res, "", false)
	if !strings.Contains(nr.Text, "Permission is hereby granted") || !strings.Contains(nr.Text, "Apache License") {
		t.Errorf("notice lacks a licence text:\n%s", nr.Text)
	}
}

func TestJoinPieces(t *testing.T) {
	piece := func(a, b, sa, sb, tokens int) Finding {
		return Finding{File: "x.go", Lines: Range{a, b}, Match: Match{Kind: "verbatim", Tokens: tokens, Coverage: 1},
			Source: Source{Key: "k", Component: "k", File: "s.go", Lines: Range{sa, sb}, Via: "index"}}
	}
	other := func(a, b int) Finding {
		return Finding{File: "x.go", Lines: Range{a, b}, Match: Match{Kind: "verbatim", Tokens: 150, Coverage: 1},
			Source: Source{Key: "gpl", Component: "gpl", File: "n.go", Lines: Range{10, 40}, Via: "index"}}
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
		// A test file of look-alike cases: pieces lined up with other cases
		// of the source sit between the pieces of the copy.
		{"look-alikes in between", []Finding{piece(1, 680, 1, 680, 2800), piece(522, 548, 485, 513, 140),
			piece(548, 740, 548, 740, 950), piece(700, 760, 740, 800, 300), piece(735, 900, 735, 900, 800)},
			[]Range{{1, 900}, {522, 548}, {700, 760}}},
		// Functions A, Z, B of one source copied with Z in the middle: A and
		// B are next to each other in the source, but Z is between them here.
		{"copied in another order", []Finding{piece(1, 20, 1, 20, 120), piece(22, 40, 300, 318, 110), piece(42, 60, 22, 40, 110)},
			[]Range{{1, 20}, {22, 40}, {42, 60}}},
		// Code from another source between two pieces isn't lines added to
		// one copy.
		{"another source in between", []Finding{piece(3, 40, 645, 682, 200), other(45, 75), piece(80, 110, 684, 719, 180)},
			[]Range{{3, 40}, {45, 75}, {80, 110}}},
	}
	for _, c := range cases {
		out := joinPieces(c.in)
		var got []Range
		for _, f := range out {
			got = append(got, f.Lines)
		}
		sort.SliceStable(got, func(i, j int) bool { return got[i].Start < got[j].Start })
		sort.SliceStable(c.lines, func(i, j int) bool { return c.lines[i].Start < c.lines[j].Start })
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

// fingerprintDigest hashes the fingerprints of every fixture file, so a
// change to tokenizing or fingerprinting shows up as a new digest.
func fingerprintDigest(t *testing.T) string {
	t.Helper()
	var paths []string
	filepath.WalkDir(filepath.Join("testdata", "upstream"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && languageFor(p) != nil {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sx := languageFor(p)
		for _, f := range winnow(tokenize(data, sx), sx) {
			fmt.Fprintf(h, "%x %x %d %d %d\n", f.hash, f.exact, f.pos, f.line, f.end)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Indexes record normVersion, and a scan refuses an index made with
// another one. Any change to how files are tokenized or fingerprinted must
// bump normVersion, or old indexes would load and quietly stop matching.
// This test fails on such a change until both are updated.
func TestFingerprintsMatchNormVersion(t *testing.T) {
	want := map[int]string{2: "e2e87159540efb43"}[normVersion]
	if got := fingerprintDigest(t); got != want {
		t.Fatalf("the fingerprints of testdata/upstream changed (digest %s, want %q for normVersion %d). "+
			"If tokenizing or fingerprinting changed, bump normVersion in fingerprint.go; then, or if only the fixtures changed, record the new digest here",
			got, want, normVersion)
	}
}

// Real-world headers that name several licences. GNU notices offer a
// choice of versions, not of licences, and warranty or file wording with
// "either ... or" isn't a choice either: those licences all apply.
func TestHeaderChoices(t *testing.T) {
	want := map[string]string{
		"gcc-form+isc.c":          "GPL-2.0-or-later AND ISC",
		"gnu-form+isc.c":          "GPL-2.0-or-later AND ISC",
		"mpl-exhibit-b+bsd.c":     "BSD-3-Clause AND MPL-2.0",
		"apache+mit-either-in.c":  "Apache-2.0 AND MIT",
		"nosa-warranty+gpl.c":     "GPL-2.0-only AND MIT",
		"gpl+alternatively-see.c": "GPL-2.0-only AND MIT",
		"kde-form+mit.c":          "LGPL-2.1-or-later AND MIT",
		"gcc-runtime.c":           "GPL-3.0-or-later",
	}
	for name, w := range want {
		src := mustRead(t, filepath.Join("testdata", "headers", name))
		head := leadingComments(src, languageFor(name), 16<<10)
		if got := headerLicense(head); got != w {
			t.Errorf("%s: %q, want %q", name, got, w)
		}
	}
	// A choice offered in one sentence doesn't swallow a licence named in
	// another.
	head := "Licensed under the Apache License, Version 2.0 or the MIT license, at your option.\n\n" +
		"The tables come from Foo, under the GNU General Public License version 2.\n"
	if got := headerLicense(head); got != "(Apache-2.0 OR MIT) AND GPL-2.0-only" {
		t.Errorf("choice plus another licence: %q", got)
	}
	// "Dual licensed." on its own offers the licences of the next sentence.
	head = "This file is dual licensed.\nYou may use it under the MIT License or under the Apache License, Version 2.0.\n"
	if got := headerLicense(head); got != "Apache-2.0 OR MIT" {
		t.Errorf("dual licensed, then the licences: %q", got)
	}
}

// For code with no licence of its own, a licence named for the code it came
// from isn't the file's.
func TestOriginSentences(t *testing.T) {
	cases := map[string]bool{
		"Based on code from Foo. Foo is released under the MIT license.\n":                                        true,
		"Adapted from Bar (https://example.com/bar).\n\nBar is licensed under the Apache License, Version 2.0.\n": true,
		"Based on code from fastsort, released under the MIT license.\n":                                          true,
		"Originally written for the Foo project.\nReleased under the MIT license.\n":                              false,
		"Copyright (c) 2015 Sam Poe. Portions copyright (c) 2014 Lee Doe.\nLicensed under the MIT License.\n":     false,
		"This file is a port of Foo. This file is licensed under the MIT License.\n":                              false,
		"Licensed under the MIT License.\n":                                                                       false,
	}
	for head, want := range cases {
		if got := originOnly(head); got != want {
			t.Errorf("%q: origin only %v, want %v", head, got, want)
		}
	}
}
