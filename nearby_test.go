package remixware

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// The case studies on remixware.com ran Remixware on typer, coyim and pip,
// which keep the licences of the code they took next to it. These tests
// keep what they taught.

const ownMIT = "MIT License\n\nCopyright (c) 2026 Project Owner\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software, to deal in the Software without restriction, subject to the following conditions: The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.\n"

func upstreamFile(t *testing.T, rel string) string {
	t.Helper()
	return string(mustRead(t, filepath.Join("testdata", "upstream", filepath.FromSlash(rel))))
}

// typer keeps Click's licence in typer/_click/LICENSE.txt, next to the code
// it took from Click.
func TestCreditKeptWithTheCopy(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "pkg/_csv/LICENSE.txt", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, dir, "pkg/_csv/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	res := scanProject(t, dir, nil, ScanOptions{})
	f := findingOf(res, "pkg/_csv/split.go")
	if f == nil || f.Status != StatusAttributed || !strings.Contains(f.Reason, "Credited in pkg/_csv/LICENSE.txt, kept with the copy.") {
		t.Fatalf("finding %+v", f)
	}
}

// coyim keeps xmpp-client's licence in a second licence file at its root,
// LICENSE.xmpp-client.
func TestCreditInASecondRootLicenceFile(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "LICENSE.fastcsv", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, dir, "internal/csv/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	res := scanProject(t, dir, nil, ScanOptions{})
	if f := findingOf(res, "internal/csv/split.go"); f == nil || f.Status != StatusAttributed || !strings.Contains(f.Reason, "Credited in LICENSE.fastcsv") {
		t.Fatalf("finding %+v", f)
	}
}

// A licence file elsewhere in the project that names the source's
// copyright line ships with the project, so with the copy too: typer's
// copies of Click outside typer/_click.
func TestCreditElsewhereInTheProject(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "pkg/_csv/LICENSE.txt", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, dir, "pkg/core/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	res := scanProject(t, dir, nil, ScanOptions{})
	f := findingOf(res, "pkg/core/split.go")
	if f == nil || f.Status != StatusAttributed || !strings.Contains(f.Reason, "Credited in pkg/_csv/LICENSE.txt. ") {
		t.Fatalf("finding %+v", f)
	}
}

// A licence in vendor/ belongs to a dependency the package manager may
// update or drop: it doesn't credit code copied out of it (the shopfront
// fixture checks the same with its vendored fastcsv), and nor does a folder
// reached through a symbolic link.
func TestCreditNotFromDependencyFoldersOrLinks(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "node_modules/fastcsv/LICENSE", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, dir, "internal/csv/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		writeTemp(t, outside, "LICENSE", upstreamFile(t, "fastcsv/LICENSE"))
		if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
		n := newNearbyNotices(dir, nil)
		if got := n.chain("linked/sub/x.go"); len(got) != 1 || got[0] != "." {
			t.Errorf("chain through a link: %q", got)
		}
	}
	res := scanProject(t, dir, nil, ScanOptions{})
	if f := findingOf(res, "internal/csv/split.go"); f == nil || f.Status != StatusAttributionMissing {
		t.Fatalf("finding %+v", f)
	}
}

// A licence that names no copyright holder, as the bare Mozilla text
// doesn't, is told apart only by where it is kept: in the nearest folder
// with a licence file below the root, as pip keeps certifi's. At the root,
// or further up, the same text may be any other library's.
func TestBareLicenceTextKeptWithTheCopy(t *testing.T) {
	lru, mpl := upstreamFile(t, "mplcache/lru.go"), upstreamFile(t, "mplcache/LICENSE")
	check := func(name string, files map[string]string, copy, want, reason string) {
		t.Helper()
		dir := t.TempDir()
		writeTemp(t, dir, "LICENSE", ownMIT)
		for rel, text := range files {
			writeTemp(t, dir, rel, text)
		}
		writeTemp(t, dir, copy, lru)
		res := scanProject(t, dir, nil, ScanOptions{})
		f := findingOf(res, copy)
		if f == nil || f.Status != want || !strings.Contains(f.Reason, reason) {
			t.Errorf("%s: %+v", name, f)
		}
	}
	check("own folder", map[string]string{"_vendor/cache/LICENSE": mpl}, "_vendor/cache/lru.go", StatusReview,
		"Credited in _vendor/cache/LICENSE, kept with the copy.")
	check("root", map[string]string{"LICENSE.mpl": mpl}, "_vendor/cache/lru.go", StatusAttributionMissing, "")
	check("further up", map[string]string{"_vendor/LICENSE.mpl": mpl, "_vendor/cache/LICENSE": "MIT License\n\nCopyright (c) 2020 Someone Else\n\n" + ownMIT[36:]},
		"_vendor/cache/lru.go", StatusAttributionMissing, "")
}

// A source that ships a NOTICE needs its NOTICE text kept too, as
// Apache-2.0 asks: pip keeps requests' LICENSE but not its NOTICE.
func TestNoticeTextKeptWithTheCopy(t *testing.T) {
	// retrykit without the copyright header its file carries: a source
	// whose only copyright line is in its NOTICE, as requests' is.
	code := upstreamFile(t, "retrykit/retry.go")
	code = code[strings.Index(code, "// Package retrykit"):]
	up := t.TempDir()
	writeTemp(t, up, "retrykit/LICENSE", upstreamFile(t, "retrykit/LICENSE"))
	writeTemp(t, up, "retrykit/NOTICE", upstreamFile(t, "retrykit/NOTICE"))
	writeTemp(t, up, "retrykit/go.mod", "module example.com/retrykit\n")
	writeTemp(t, up, "retrykit/retry.go", code)
	ix, err := BuildIndex([]string{filepath.Join(up, "retrykit")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "_vendor/retrykit/LICENSE", upstreamFile(t, "retrykit/LICENSE"))
	writeTemp(t, dir, "_vendor/retrykit/retry.go", code)
	res := scanWith(t, ix, dir)
	f := findingOf(res, "_vendor/retrykit/retry.go")
	if f == nil || f.Status != StatusAttributionMissing ||
		!strings.Contains(f.Reason, "Its licence text is kept with the copy, in _vendor/retrykit/LICENSE, but its NOTICE text isn't.") {
		t.Fatalf("without NOTICE: %+v", f)
	}
	writeTemp(t, dir, "_vendor/retrykit/NOTICE", upstreamFile(t, "retrykit/NOTICE"))
	res = scanWith(t, ix, dir)
	if f := findingOf(res, "_vendor/retrykit/retry.go"); f == nil || f.Status != StatusAttributed {
		t.Fatalf("with NOTICE: %+v", f)
	}
}

// An entry remixware notice left unfinished holds the licence text already,
// but credits nothing until it's finished, wherever the notices file is.
func TestUnfinishedEntryKeptWithTheCopy(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "_vendor/cache/lru.go", upstreamFile(t, "mplcache/lru.go"))
	writeTemp(t, dir, "_vendor/cache/LICENSE", "## example.net/mplcache\n\n- Source: example.net/mplcache\n\nCopyright: not known. Copy it here "+unfinishedMark+".\n\n```text\n"+upstreamFile(t, "mplcache/LICENSE")+"\n```\n")
	res := scanProject(t, dir, nil, ScanOptions{})
	if f := findingOf(res, "_vendor/cache/lru.go"); f == nil || f.Status != StatusAttributionMissing {
		t.Fatalf("finding %+v", f)
	}
}

// pip's tests hold wheels called license.dist-0.1-py2.py3-none-any.whl:
// archives, not licence texts.
func TestBinaryFilesAreNotLicences(t *testing.T) {
	for _, name := range []string{"license.dist-0.1-py2.py3-none-any.whl", "LICENSE.zip", "license.png", "LICENSE.pdf"} {
		if isLicenseFileName(name) {
			t.Errorf("%s read as a licence file", name)
		}
	}
	dir := t.TempDir()
	p := writeTemp(t, dir, "LICENSE.dat", "PK\x03\x04\x00\x00 permission is granted license terms")
	if expr, _, _, _ := licenseFromFiles([]string{p}); expr != "" {
		t.Errorf("binary licence file read as %q", expr)
	}
}

// packaging's LICENSE only says which of its two other licence files apply.
func TestLicenceFileThatPointsToOthers(t *testing.T) {
	apache, bsd := upstreamFile(t, "retrykit/LICENSE"), "Copyright (c) Donald Stufft and individual contributors.\nAll rights reserved.\n\nRedistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:\n\n1. Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.\n\n2. Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.\n\nTHIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS \"AS IS\" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.\n"
	cases := []struct{ pointer, a, b, want string }{
		{"This software is made available under the terms of *either* of the licenses\nfound in LICENSE.APACHE or LICENSE.BSD. Contributions to this software is made\nunder the terms of *both* these licenses.\n",
			"LICENSE.APACHE", "LICENSE.BSD", "Apache-2.0 OR BSD-2-Clause"},
		// Files that don't name their licence: the pointer's "either ... or"
		// makes them a choice.
		{"You may use this under either COPYING.one or COPYING.two, as you choose.\n", "COPYING.one", "COPYING.two", "Apache-2.0 OR BSD-2-Clause"},
		{"Use it under COPYING.one or COPYING.two, at your option.\n", "COPYING.one", "COPYING.two", "Apache-2.0 OR BSD-2-Clause"},
		// Any other wording is read as before, a licence text of its own
		// that may add terms: a stray "either" or "choice" makes no choice.
		{"This software is covered by the terms in COPYING.one and in COPYING.two.\n", "COPYING.one", "COPYING.two",
			"Apache-2.0 AND BSD-2-Clause AND LicenseRef-Unrecognised"},
		{"The client is under COPYING.one. The server is under COPYING.two. Files in either folder keep the licence of that folder.\n",
			"COPYING.one", "COPYING.two", "Apache-2.0 AND BSD-2-Clause AND LicenseRef-Unrecognised"},
		{"There's no choice between COPYING.one and COPYING.two: both apply.\n", "COPYING.one", "COPYING.two",
			"Apache-2.0 AND BSD-2-Clause"},
		{"Either way, every file is under COPYING.one or COPYING.two, as its header says.\n", "COPYING.one", "COPYING.two",
			"Apache-2.0 AND BSD-2-Clause"},
		{"Each file is under COPYING.one or COPYING.two, as its header says. Patches are welcome at your option.\n", "COPYING.one", "COPYING.two",
			"Apache-2.0 AND BSD-2-Clause"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		paths := []string{writeTemp(t, dir, "LICENSE", c.pointer), writeTemp(t, dir, c.a, apache), writeTemp(t, dir, c.b, bsd)}
		expr, _, _, cr := licenseFromFiles(paths)
		if expr != c.want {
			t.Errorf("%q: %q, want %q", c.pointer, expr, c.want)
		}
		if len(cr) != 1 {
			t.Errorf("%q: copyrights %q", c.pointer, cr)
		}
	}
}

// A source file named like a notice file (attribution.go, credits.js) is
// code: a comment in it naming the source is no notice.
func TestCodeNamedLikeANoticeIsNoCredit(t *testing.T) {
	for _, name := range []string{"internal/csv/thirdparty_split.go", "internal/ads/attribution.go", "attribution.go",
		"cmake/ThirdParty.cmake", "bazel/third_party_repos.bzl"} {
		dir := t.TempDir()
		writeTemp(t, dir, "LICENSE", ownMIT)
		writeTemp(t, dir, name, "// Adapted from https://example.com/fastcsv, Copyright (c) 2021 Mira Okafor\npackage x\n")
		writeTemp(t, dir, "internal/csv/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
		res := scanProject(t, dir, nil, ScanOptions{})
		if f := findingOf(res, "internal/csv/split.go"); f == nil || f.Status != StatusAttributionMissing || f.Credited {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

// The walk for licence files leaves out what the scan leaves out: the
// policy's ignore list and --exclude.
func TestCreditNotFromIgnoredPaths(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "testdata/upstream/fastcsv/LICENSE", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, dir, "internal/csv/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	res, err := Scan(dir, ScanOptions{Indexes: []*Index{upstream(t)}, Exclude: []string{"testdata/"}})
	if err != nil {
		t.Fatal(err)
	}
	Evaluate(res, nil, "")
	if f := findingOf(res, "internal/csv/split.go"); f == nil || f.Status != StatusAttributionMissing {
		t.Errorf("--exclude: %+v", f)
	}
	res, err = Scan(dir, ScanOptions{Indexes: []*Index{upstream(t)}})
	if err != nil {
		t.Fatal(err)
	}
	Evaluate(res, &Policy{Ignore: []string{"testdata/**"}}, "")
	if f := findingOf(res, "internal/csv/split.go"); f == nil || f.Status != StatusAttributionMissing {
		t.Errorf("ignore: %+v", f)
	}
}

// What one copy finds kept with it doesn't depend on what the walk for
// another copy read first, and unread files are counted.
func TestNearbyBudgets(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "docs/a/NOTICE", strings.Repeat("filler notice text\n", 64))
	writeTemp(t, dir, "docs/b/NOTICE", strings.Repeat("filler notice text\n", 64))
	writeTemp(t, dir, "z/_csv/LICENSE", upstreamFile(t, "fastcsv/LICENSE"))
	n := newNearbyNotices(dir, nil)
	n.walkBudget = 1 // spent on the first file the walk reads
	src := Source{Key: "example.com/fastcsv", Copyrights: []string{"Copyright (c) 2021 Mira Okafor"}}
	if by, _, _ := n.credit("a/lru.go", Source{Key: "example.net/mplcache"}, nil); by != "" {
		t.Errorf("credited by %s", by)
	}
	if by, withCopy, _ := n.credit("z/_csv/split.go", src, nil); by != "z/_csv/LICENSE" || !withCopy {
		t.Errorf("own folder after the walk: %q %v", by, withCopy)
	}
	if len(n.unread) == 0 {
		t.Errorf("no unread files counted")
	}
}

// A project reached through a symbolic link is read like any other, with
// --from results alone too.
func TestSymlinkedProjectRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	real := t.TempDir()
	writeTemp(t, real, "LICENSE", ownMIT)
	writeTemp(t, real, "pkg/_csv/LICENSE", upstreamFile(t, "fastcsv/LICENSE"))
	writeTemp(t, real, "pkg/core/split.go", upstreamFile(t, "fastcsv/fastcsv.go"))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	res, err := NewResults(link)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(real); res.Project != want {
		t.Errorf("project %q, want %q", res.Project, want)
	}
	n := newNearbyNotices(link, nil)
	src := Source{Key: "example.com/fastcsv", Copyrights: []string{"Copyright (c) 2021 Mira Okafor"}}
	if by, _, _ := n.credit("pkg/core/split.go", src, nil); by != "pkg/_csv/LICENSE" {
		t.Errorf("credited by %q", by)
	}
}

// A copy that needs a review and is credited already gets no notice entry
// to finish by hand.
func TestNoticeSkipsCreditedCopies(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", ownMIT)
	writeTemp(t, dir, "_vendor/cache/LICENSE", upstreamFile(t, "mplcache/LICENSE"))
	writeTemp(t, dir, "_vendor/cache/lru.go", upstreamFile(t, "mplcache/lru.go"))
	res := scanProject(t, dir, nil, ScanOptions{})
	if f := findingOf(res, "_vendor/cache/lru.go"); f == nil || f.Status != StatusReview || !f.Credited {
		t.Fatalf("finding %+v", f)
	}
	if out := Notices(res, "", false); len(out.Written) != 0 {
		t.Errorf("notice wrote %v", out.Written)
	}
}

// Only a distribution read from its metadata takes the texts of the folder
// above it: an npm package without a licence file may have other holders,
// and nothing crosses a _vendor folder.
func TestOnlyReleaseMetadataTakesTexts(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "LICENSE", "MIT License\n\nCopyright (c) 2020 Big Corp\n\n"+ownMIT[strings.Index(ownMIT, "Permission"):])
	writeTemp(t, dir, "lib/leftpad/package.json", `{"name": "leftpad", "version": "1.0.0", "license": "MIT", "author": "Someone Else"}`)
	writeTemp(t, dir, "lib/leftpad/index.js", "module.exports = function (s) { return s }\n")
	writeTemp(t, dir, "outer/_vendor/inner-2.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: inner\nVersion: 2.0\nLicense: MIT\n\n")
	writeTemp(t, dir, "outer/_vendor/inner-2.0.dist-info/top_level.txt", "inner\n")
	writeTemp(t, dir, "outer/_vendor/inner/__init__.py", "def f():\n    return 1\n")
	ix, err := BuildIndex([]string{dir}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ix.Components {
		if (c.Name == "leftpad" || c.Name == "inner") && len(c.Copyrights) > 0 {
			t.Errorf("%s took copyrights %q", c.Name, c.Copyrights)
		}
	}
}

// A findings file from elsewhere can't hand Evaluate a text past what an
// index keeps.
func TestImportedTextsCapped(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "x.go", "package x\n")
	// Past three times what an index keeps (cleaning can grow a text that
	// much), a licence text is left out and a NOTICE is cut; up to it, both
	// are kept as they came.
	big := strings.Repeat("licence text ", 3*maxLicenseText/12+10)
	notice := strings.Repeat("é", 3*maxNoticeText/2+10)
	data := `{"tool": "remixware 0.2.1", "project": ".", "findings": [], "texts": {"k": {"license_text": "` + big +
		`", "notice_text": "` + notice + `"}, "ok": {"license_text": "` + big[:maxLicenseText+100] + `", "notice_text": "short"}}}`
	p := writeTemp(t, t.TempDir(), "findings.json", data)
	_, _, texts, err := ImportResults(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if tx := texts["k"]; tx == nil || tx.LicenseText != "" || len(tx.NoticeText) > 3*maxNoticeText || !utf8.ValidString(tx.NoticeText) || tx.NoticeText == "" {
		t.Errorf("too long: licence %d bytes, notice %d bytes", len(tx.LicenseText), len(tx.NoticeText))
	}
	if tx := texts["ok"]; tx == nil || len(tx.LicenseText) != maxLicenseText+100 || tx.NoticeText != "short" {
		t.Errorf("kept: %d bytes", len(tx.LicenseText))
	}
}

// A source release from PyPI keeps its licence file at the top and its
// metadata in an .egg-info next to the package. The package's component
// takes the release's licence text, copyright lines and NOTICE.
func TestReleaseMetadataTakesTheReleaseTexts(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(dir, "widget-1.2.0")
	writeTemp(t, rel, "LICENSE", "MIT License\n\nCopyright (c) 2019 Widget Makers\n\n"+ownMIT[strings.Index(ownMIT, "Permission"):])
	writeTemp(t, rel, "NOTICE", "Widget\nCopyright 2019 Widget Makers\n")
	writeTemp(t, rel, "widget.egg-info/PKG-INFO", "Metadata-Version: 2.1\nName: widget\nVersion: 1.2.0\nLicense: MIT\n\n")
	writeTemp(t, rel, "widget.egg-info/top_level.txt", "widget\n")
	writeTemp(t, rel, "widget/__init__.py", "def f():\n    return 1\n")
	ix, err := BuildIndex([]string{rel}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var c *Component
	for _, x := range ix.Components {
		if x.Name == "widget" && x.Version == "1.2.0" {
			c = x
		}
	}
	if c == nil {
		t.Fatalf("no widget@1.2.0 in %v", ix.Components)
	}
	if len(c.Copyrights) != 1 || c.Copyrights[0] != "Copyright (c) 2019 Widget Makers" || !strings.Contains(c.LicenseText, "Permission is hereby granted") ||
		!strings.Contains(c.NoticeText, "Widget Makers") {
		t.Errorf("widget@1.2.0: copyrights %q, text %d bytes, notice %q", c.Copyrights, len(c.LicenseText), c.NoticeText)
	}
}

func TestPyprojectLicenceNames(t *testing.T) {
	for in, want := range map[string]string{
		"ISC License":                "ISC",
		"MIT":                        "MIT",
		"MIT OR Apache-2.0":          NormalizeLicense("MIT OR Apache-2.0"),
		"Mozilla Public License 2.0": "MPL-2.0",
		"Dual License":               "",
		"":                           "",
	} {
		if got := pyLicense(in); got != want {
			t.Errorf("pyLicense(%q) = %q, want %q", in, got, want)
		}
	}
}

// An index cuts a long NOTICE at a character boundary, so the text a
// findings file carries reads back the same, and a licence text with its
// separators stays within what an index keeps.
func TestTextCapsMatchOnImport(t *testing.T) {
	up := t.TempDir()
	notice := "Widget\nCopyright 2019 Widget Makers\n" + strings.Repeat("x", maxNoticeText-40) + strings.Repeat("é", 1000)
	writeTemp(t, up, "widget/LICENSE", upstreamFile(t, "retrykit/LICENSE"))
	writeTemp(t, up, "widget/NOTICE", notice)
	writeTemp(t, up, "widget/go.mod", "module example.com/widget\n")
	writeTemp(t, up, "widget/w.go", upstreamFile(t, "retrykit/retry.go"))
	ix, err := BuildIndex([]string{filepath.Join(up, "widget")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var c *Component
	for _, x := range ix.Components {
		if x.Name == "example.com/widget" {
			c = x
		}
	}
	if c == nil || len(c.NoticeText) > maxNoticeText || !utf8.ValidString(c.NoticeText) || !strings.HasPrefix(c.NoticeText, "Widget") {
		t.Fatalf("notice %d bytes, valid %v", len(c.NoticeText), utf8.ValidString(c.NoticeText))
	}
	dir := t.TempDir()
	parts := []string{writeTemp(t, dir, "LICENSE-A", strings.Repeat("Permission is granted under these terms. ", maxLicenseText/82)),
		writeTemp(t, dir, "LICENSE-B", "MIT License\n\n"+ownMIT[strings.Index(ownMIT, "Permission"):]+strings.Repeat(" ", maxLicenseText/2-600))}
	if _, _, text, _ := licenseFromFiles(parts); len(text) > maxLicenseText {
		t.Errorf("licence text %d bytes, more than %d", len(text), maxLicenseText)
	}
}

// A file left unread by both budgets is counted once.
func TestUnreadCountedOnce(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "a/LICENSE", ownMIT)
	writeTemp(t, dir, "b/LICENSE", ownMIT)
	n := newNearbyNotices(dir, nil)
	n.chainBudget, n.walkBudget = 0, 0
	n.credit("a/x.go", Source{Key: "k"}, nil)
	n.credit("b/x.go", Source{Key: "k2"}, nil)
	if len(n.unread) != 2 {
		t.Errorf("unread %v", n.unread)
	}
}
