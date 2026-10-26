package remixware

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The fixtures: testdata/upstream holds small open source projects under
// different licences. The projects in testdata/projects copy code from them
// (with names changed or lines edited in some copies) next to code of their
// own that only looks like it. The tests check that every copy is found,
// with the right source, licence, lines and status, and nothing else is.

var (
	upOnce  sync.Once
	upIndex *Index
	upErr   error
)

// upstream returns the index of testdata/upstream, built once per run.
func upstream(t *testing.T) *Index {
	t.Helper()
	upOnce.Do(func() {
		upIndex, upErr = BuildIndex([]string{filepath.Join("testdata", "upstream")}, IndexOptions{})
	})
	if upErr != nil {
		t.Fatal(upErr)
	}
	return upIndex
}

func project(name string) string { return filepath.Join("testdata", "projects", name) }

// scanProject scans a fixture project against the upstream index and
// evaluates it with the policy (nil: no policy).
func scanProject(t *testing.T, dir string, pol *Policy, opt ScanOptions) *Results {
	t.Helper()
	if opt.Indexes == nil {
		opt.Indexes = []*Index{upstream(t)}
	}
	res, err := Scan(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	Evaluate(res, pol, "")
	return res
}

type want struct {
	status, file string
	lines        Range
	component    string
	license      string
}

func checkFindings(t *testing.T, res *Results, wants []want) {
	t.Helper()
	byFile := map[string]Finding{}
	for _, f := range res.Findings {
		if _, dup := byFile[f.File]; dup {
			t.Errorf("two findings for %s", f.File)
		}
		byFile[f.File] = f
	}
	for _, w := range wants {
		f, ok := byFile[w.file]
		if !ok {
			t.Errorf("%s: not found (want %s from %s)", w.file, w.status, w.component)
			continue
		}
		delete(byFile, w.file)
		if f.Status != w.status || f.Lines != w.lines || f.Source.Component != w.component || f.Source.License != w.license {
			t.Errorf("%s: got %s lines %s from %s (%s), want %s lines %s from %s (%s)", w.file,
				f.Status, f.Lines, f.Source.Component, f.Source.License, w.status, w.lines, w.component, w.license)
		}
		if f.Reason == "" || !strings.HasPrefix(f.ID, "RW-") {
			t.Errorf("%s: no reason or id: %+v", w.file, f)
		}
	}
	for file, f := range byFile {
		t.Errorf("unexpected finding %s:%s from %s (%s)", file, f.Lines, f.Source.Component, f.Status)
	}
}

func TestIndexReadsComponentsAndLicences(t *testing.T) {
	ix := upstream(t)
	type comp struct{ version, license, copyright string }
	wants := map[string]comp{
		"example.com/fastcsv":  {"", "MIT", "Copyright (c) 2021 Mira Okafor"},
		"example.org/gplsort":  {"", "GPL-3.0-or-later", "Copyright (C) 2019 Tomas Lindqvist"},
		"example.com/retrykit": {"", "Apache-2.0", ""},
		"example.net/mplcache": {"", "MPL-2.0", ""},
		"upstream":             {"", LicenseNone, ""},
		"agplwiki":             {"0.4.0", "AGPL-3.0-or-later", ""},
		"jsutil":               {"3.1.0", "BSD-3-Clause", ""},
		"upstream/cstr":        {"", "Zlib", ""},
		"leftpadx":             {"1.0.4", "ISC", ""},
		"tinyjson":             {"1.2.0", "MIT", ""},
	}
	got := map[string]*Component{}
	for _, c := range ix.Components {
		got[c.Name] = c
	}
	for name, w := range wants {
		c, ok := got[name]
		if !ok {
			t.Errorf("component %s missing", name)
			continue
		}
		if c.Version != w.version || c.License != w.license {
			t.Errorf("%s: version %q licence %q, want %q %q (licence from %s)", name, c.Version, c.License, w.version, w.license, c.LicenseFrom)
		}
		if w.copyright != "" && !containsString(c.Copyrights, w.copyright) {
			t.Errorf("%s: copyrights %q, want %q", name, c.Copyrights, w.copyright)
		}
	}
	if len(ix.Components) != len(wants) {
		var names []string
		for _, c := range ix.Components {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		t.Errorf("components: %v", names)
	}
	if rk := got["example.com/retrykit"]; rk == nil || !strings.Contains(rk.NoticeText, "Harbor Lane Software") {
		t.Errorf("retrykit's NOTICE file wasn't kept")
	}
}

func TestScanShopfront(t *testing.T) {
	res := scanProject(t, project("shopfront"), nil, ScanOptions{})
	if res.ProjectLicense != "MIT" {
		t.Errorf("project licence %q", res.ProjectLicense)
	}
	checkFindings(t, res, []want{
		{StatusReview, "internal/cache/lru.go", Range{2, 52}, "example.net/mplcache", "MPL-2.0"},
		{StatusAttributionMissing, "internal/csvio/split.go", Range{2, 45}, "example.com/fastcsv", "MIT"},
		{StatusConflict, "internal/files/order.go", Range{7, 46}, "example.org/gplsort", "GPL-3.0-or-later"},
		{StatusNoLicense, "internal/format/bytes.go", Range{2, 24}, "upstream", LicenseNone},
		{StatusAttributed, "internal/net/retry.go", Range{13, 46}, "example.com/retrykit", "Apache-2.0"},
		{StatusConflict, "scripts/slug.py", Range{1, 36}, "agplwiki", "AGPL-3.0-or-later"},
		{StatusAttributionMissing, "tools/prettyjson.py", Range{1, 32}, "tinyjson", "MIT"},
		{StatusAttributionMissing, "web/pad.js", Range{1, 28}, "leftpadx", "ISC"},
		{StatusAttributionMissing, "web/util.js", Range{3, 25}, "jsutil", "BSD-3-Clause"},
	})
	// The vendored copy of fastcsv is a dependency, not a copy.
	for _, f := range res.Findings {
		if strings.HasPrefix(f.File, "vendor/") {
			t.Errorf("vendor folder scanned: %s", f.File)
		}
	}
	if len(res.NoticeFiles) != 1 || res.NoticeFiles[0] != "THIRD_PARTY_NOTICES.md" {
		t.Errorf("notice files %v", res.NoticeFiles)
	}
}

func TestScanFirmware(t *testing.T) {
	res := scanProject(t, project("firmware"), nil, ScanOptions{})
	if res.ProjectLicense != "GPL-2.0-only" {
		t.Errorf("project licence %q", res.ProjectLicense)
	}
	checkFindings(t, res, []want{
		{StatusAttributionMissing, "src/strbuf.c", Range{2, 30}, "upstream/cstr", "Zlib"},
		{StatusConflict, "tools/retry/retry.go", Range{5, 39}, "example.com/retrykit", "Apache-2.0"},
		{StatusConflict, "tools/sortnames/main.go", Range{10, 37}, "example.org/gplsort", "GPL-3.0-or-later"},
	})
}

// The clean project holds code that looks like the upstream code (flag
// definitions, sort.Interface, test tables, argument pass-through) but
// copies none of it.
func TestScanCleanFindsNothing(t *testing.T) {
	res := scanProject(t, project("clean"), nil, ScanOptions{})
	checkFindings(t, res, nil)
	if res.FilesScanned != 5 {
		t.Errorf("scanned %d files, want 5", res.FilesScanned)
	}
}

// A project checked out with Windows line endings gives the same findings.
func TestWindowsCheckout(t *testing.T) {
	dir := copyTree(t, project("shopfront"))
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(path)
			os.WriteFile(path, bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")), 0o644)
		}
		return nil
	})
	a := scanProject(t, project("shopfront"), nil, ScanOptions{})
	b := scanProject(t, dir, nil, ScanOptions{})
	if len(a.Findings) != len(b.Findings) {
		t.Fatalf("%d findings with LF, %d with CRLF", len(a.Findings), len(b.Findings))
	}
	for i := range a.Findings {
		x, y := a.Findings[i], b.Findings[i]
		if x.File != y.File || x.Lines != y.Lines || x.Status != y.Status || x.Match.Kind != y.Match.Kind {
			t.Errorf("LF %s:%s %s %s, CRLF %s:%s %s %s", x.File, x.Lines, x.Status, x.Match.Kind, y.File, y.Lines, y.Status, y.Match.Kind)
		}
	}
}

func TestIncludeDepsScansVendor(t *testing.T) {
	res := scanProject(t, project("shopfront"), nil, ScanOptions{IncludeDeps: true})
	found := false
	for _, f := range res.Findings {
		if f.File == "vendor/example.com/fastcsv/fastcsv.go" && f.Source.Component == "example.com/fastcsv" {
			found = true
		}
	}
	if !found {
		t.Errorf("vendored fastcsv not reported with IncludeDeps")
	}
}

// An index of the project's own vendor folder covers it: those files are
// declared dependencies, not copies.
func TestDeclaredFoldersAreSkipped(t *testing.T) {
	vix, err := BuildIndex([]string{filepath.Join(project("shopfront"), "vendor")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res := scanProject(t, project("shopfront"), nil, ScanOptions{Indexes: []*Index{vix}, IncludeDeps: true})
	for _, f := range res.Findings {
		if strings.HasPrefix(f.File, "vendor/") {
			t.Errorf("declared folder reported: %s", f.File)
		}
	}
	if res.Skipped["declared"] == 0 {
		t.Errorf("no declared folder skipped: %v", res.Skipped)
	}
	// The copy outside vendor/ is still found.
	ok := false
	for _, f := range res.Findings {
		ok = ok || f.File == "internal/csvio/split.go"
	}
	if !ok {
		t.Errorf("copy outside the declared folder missed")
	}
}

func TestIndexRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "up.rmx")
	if err := upstream(t).Save(path); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Fingerprints() != upstream(t).Fingerprints() || len(ix.Files) != len(upstream(t).Files) {
		t.Fatalf("loaded %d fingerprints and %d files, saved %d and %d", ix.Fingerprints(), len(ix.Files),
			upstream(t).Fingerprints(), len(upstream(t).Files))
	}
	a := scanProject(t, project("shopfront"), nil, ScanOptions{})
	b := scanProject(t, project("shopfront"), nil, ScanOptions{Indexes: []*Index{ix}})
	if len(a.Findings) != len(b.Findings) {
		t.Fatalf("%d findings with the loaded index, %d with the built one", len(b.Findings), len(a.Findings))
	}
	for i := range a.Findings {
		if a.Findings[i].ID != b.Findings[i].ID || a.Findings[i].Lines != b.Findings[i].Lines {
			t.Errorf("finding %d differs: %+v vs %+v", i, a.Findings[i], b.Findings[i])
		}
	}
}

// A damaged or foreign index file is refused with a clear error, never
// read as an index that matches nothing.
func TestDamagedIndexIsRefused(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.rmx")
	if err := upstream(t).Save(good); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flipped := append([]byte(nil), data...)
	flipped[len(flipped)/2] ^= 0x55

	// An index from a build with other matching settings.
	var other bytes.Buffer
	other.WriteString(indexMagic)
	zw := gzip.NewWriter(&other)
	head, _ := json.Marshal(indexHeader{Format: 1, Tool: "remixware 0.0.1", K: kgramSize + 1, W: windowSize, MinDistinct: minDistinct, Norm: normVersion})
	var vb [binary.MaxVarintLen64]byte
	zw.Write(vb[:binary.PutUvarint(vb[:], uint64(len(head)))])
	zw.Write(head)
	zw.Write([]byte{0})
	zw.Close()

	cases := map[string]struct {
		path, msg string
	}{
		"not an index":    {write("notes.rmx", []byte("hello, world\n")), "not a Remixware index"},
		"empty":           {write("empty.rmx", nil), "not a Remixware index"},
		"cut short":       {write("short.rmx", data[:len(data)*2/3]), "damaged"},
		"header only":     {write("head.rmx", data[:len(indexMagic)+5]), "damaged"},
		"flipped byte":    {write("flipped.rmx", flipped), "damaged"},
		"other settings":  {write("other.rmx", other.Bytes()), "other settings"},
		"trailing data":   {write("trailing.rmx", append(append([]byte(nil), data...), "junk"...)), "damaged"},
		"missing file":    {filepath.Join(dir, "nope.rmx"), "not-exist"},
		"gzip, not index": {write("plain.rmx", append([]byte(indexMagic), 0x1f, 0x8b, 0, 0)), "nope"},
	}
	for name, c := range cases {
		_, err := LoadIndex(c.path)
		if err == nil {
			t.Errorf("%s: loaded without error", name)
			continue
		}
		if c.msg == "not-exist" {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: error %q isn't a missing-file error", name, err)
			}
			continue
		}
		if c.msg != "nope" && !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: error %q, want it to mention %q", name, err, c.msg)
		}
	}
}

// An index from somewhere else is data, not instructions: a made-up one is
// refused when it doesn't hold together, and its file paths can't make a
// scan read files outside the folders it was made from.
func TestCraftedIndexes(t *testing.T) {
	dir := t.TempDir()
	fresh := func() *Index {
		ix, err := BuildIndex([]string{filepath.Join("testdata", "upstream")}, IndexOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return ix
	}
	refused := map[string]func(ix *Index){
		"a missing file":              func(ix *Index) { ix.Files = append(ix.Files, nil) },
		"a missing component":         func(ix *Index) { ix.Components = append(ix.Components, nil) },
		"another version that isn't":  func(ix *Index) { ix.Files[0].Also = []int{999} },
		"a component that isn't":      func(ix *Index) { ix.Files[0].Comp = -1 },
		"a fingerprint past its file": func(ix *Index) { ix.Files[ix.fps[0].file].Tokens = 3 },
	}
	for name, spoil := range refused {
		ix := fresh()
		spoil(ix)
		p := filepath.Join(dir, "spoiled.rmx")
		if err := ix.Save(p); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadIndex(p); err == nil || !strings.Contains(err.Error(), "damaged") {
			t.Errorf("%s: loaded (error %v)", name, err)
		}
	}

	// A short file that claims a huge header or billions of fingerprints is
	// refused without allocating for them.
	for name, n := range map[string][2]uint64{"huge header": {1 << 30, 0}, "huge count": {0, 1 << 34}} {
		var b bytes.Buffer
		b.WriteString(indexMagic)
		zw := gzip.NewWriter(&b)
		head, _ := json.Marshal(indexHeader{Format: 1, K: kgramSize, W: windowSize, MinDistinct: minDistinct, Norm: normVersion})
		var vb [binary.MaxVarintLen64]byte
		if n[0] > 0 {
			zw.Write(vb[:binary.PutUvarint(vb[:], n[0])])
		} else {
			zw.Write(vb[:binary.PutUvarint(vb[:], uint64(len(head)))])
			zw.Write(head)
			zw.Write(vb[:binary.PutUvarint(vb[:], n[1])])
		}
		zw.Close()
		p := filepath.Join(dir, "huge.rmx")
		os.WriteFile(p, b.Bytes(), 0o644)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := LoadIndex(p); err == nil {
			t.Errorf("%s: loaded", name)
		}
		runtime.ReadMemStats(&after)
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
			t.Errorf("%s: loading allocated %d MB", name, grew>>20)
		}
	}

	// Paths that point outside the indexed folders, at a file that isn't
	// source code, or at a device, are never read: the scan still finds
	// the copy, but the report doesn't show the "source".
	secret := writeTemp(t, dir, "local_settings.py", "SECRET_KEY = 'hunter2'\n")
	for name, abs := range map[string]string{"outside the folders": secret, "not source code": "/etc/hostname",
		"a device": "/dev/zero", "not clean": filepath.Join(dir, "x", "..", "local_settings.py")} {
		ix := fresh()
		for _, f := range ix.Files {
			f.Abs = abs
		}
		if name == "outside the folders" {
			ix.Roots = append(ix.Roots, "relative/root")
		}
		p := filepath.Join(dir, "paths.rmx")
		if err := ix.Save(p); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadIndex(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res, err := Scan(project("shopfront"), ScanOptions{Indexes: []*Index{loaded}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		Evaluate(res, nil, "")
		if len(res.Findings) == 0 {
			t.Errorf("%s: no findings", name)
		}
		var html bytes.Buffer
		if err := WriteHTML(&html, res); err != nil {
			t.Fatal(err)
		}
		for _, f := range res.Findings {
			if f.Source.Abs != "" {
				t.Errorf("%s: finding keeps the path %s", name, f.Source.Abs)
			}
		}
		if strings.Contains(html.String(), "hunter2") {
			t.Errorf("%s: the report shows the local file", name)
		}
	}
}

func TestSCANOSSImport(t *testing.T) {
	dir := project("shopfront")
	found, in, _, err := ImportResults(filepath.Join("testdata", "scanoss", "shopfront.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "scanoss" || in.Matches != 4 {
		t.Errorf("input %+v", in)
	}
	res, err := NewResults(dir)
	if err != nil {
		t.Fatal(err)
	}
	res.Add(found, in, nil)
	Evaluate(res, nil, "")
	checkFindings(t, res, []want{
		{StatusConflict, "scripts/slug.py", Range{9, 36}, "nhaddad/agplwiki", "AGPL-3.0-or-later"},
		{StatusReview, "web/vendor.min.js", Range{1, 1}, "tinychart", LicenseNoAssertion},
		{StatusAttributionMissing, "internal/csvio/split.go", Range{14, 48}, "mokafor/fastcsv", "MIT"},
		{StatusAttributionMissing, "web/pad.js", Range{1, 28}, "leftpadx", "ISC"},
	})
}

// Index and SCANOSS results together: the same copy found by both is one
// finding.
func TestIndexAndSCANOSSTogether(t *testing.T) {
	dir := project("shopfront")
	res, err := Scan(dir, ScanOptions{Indexes: []*Index{upstream(t)}})
	if err != nil {
		t.Fatal(err)
	}
	found, in, texts, err := ImportResults(filepath.Join("testdata", "scanoss", "shopfront.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	res.Add(found, in, texts)
	Evaluate(res, nil, "")
	if len(res.Findings) != 10 {
		for _, f := range res.Findings {
			t.Logf("%s %s:%s %s", f.Status, f.File, f.Lines, f.Source.Component)
		}
		t.Fatalf("%d findings, want 10", len(res.Findings))
	}
	wantCounts := map[string]int{StatusConflict: 2, StatusNoLicense: 1, StatusReview: 2, StatusAttributionMissing: 4, StatusAttributed: 1}
	for s, n := range wantCounts {
		if res.Counts[s] != n {
			t.Errorf("%s: %d, want %d (%v)", s, res.Counts[s], n, res.Counts)
		}
	}
}

// The JSON findings file can be read back, so a scan can be checked later
// or combined with other results.
func TestFindingsFileRoundTrip(t *testing.T) {
	res := scanProject(t, project("shopfront"), nil, ScanOptions{})
	path := filepath.Join(t.TempDir(), "findings.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(f, res); err != nil {
		t.Fatal(err)
	}
	f.Close()
	found, in, texts, err := ImportResults(path, project("shopfront"))
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "remixware" || len(found) != len(res.Findings) {
		t.Fatalf("read back %d findings as %q, wrote %d", len(found), in.Kind, len(res.Findings))
	}
	back, _ := NewResults(project("shopfront"))
	back.Add(found, in, texts)
	Evaluate(back, nil, "")
	for i := range res.Findings {
		a, b := res.Findings[i], back.Findings[i]
		if a.ID != b.ID || a.Status != b.Status || a.Lines != b.Lines || a.Source.License != b.Source.License {
			t.Errorf("finding %d: wrote %s %s %s, read %s %s %s", i, a.File, a.Status, a.Lines, b.File, b.Status, b.Lines)
		}
	}
}

func writePolicy(t *testing.T, body string) *Policy {
	t.Helper()
	p := filepath.Join(t.TempDir(), PolicyFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pol, err := LoadPolicy(p)
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

func statusOf(res *Results, file string) string {
	for _, f := range res.Findings {
		if f.File == file {
			return f.Status
		}
	}
	return ""
}

func TestPolicyRules(t *testing.T) {
	pol := writePolicy(t, `{
		"allow": [
			{"component": "example.net/*", "reason": "MPL files are kept unchanged in their own folder"},
			{"license": "ISC", "file": "web/*.js", "reason": "ISC code in the web bundle is credited on the about page"}
		],
		"ignore": ["scripts/**"],
		"fail_on": ["conflict"]
	}`)
	res := scanProject(t, project("shopfront"), pol, ScanOptions{Exclude: pol.Ignore})
	if s := statusOf(res, "internal/cache/lru.go"); s != StatusAllowed {
		t.Errorf("lru.go: %s, want allowed", s)
	}
	if s := statusOf(res, "web/pad.js"); s != StatusAllowed {
		t.Errorf("pad.js: %s, want allowed", s)
	}
	if s := statusOf(res, "web/util.js"); s != StatusAttributionMissing {
		t.Errorf("util.js (BSD, not ISC): %s", s)
	}
	if s := statusOf(res, "scripts/slug.py"); s != "" {
		t.Errorf("ignored path reported: %s", s)
	}
	for _, f := range res.Findings {
		if f.Status == StatusAllowed && !strings.Contains(f.Reason, "Allowed by policy") {
			t.Errorf("%s: reason %q", f.File, f.Reason)
		}
	}
	fails := Failures(res, pol.FailOn)
	if len(fails) != 1 || fails[0].File != "internal/files/order.go" {
		t.Errorf("failures %v", fails)
	}
}

func TestPolicyLicenseAndNoticeFiles(t *testing.T) {
	// The policy's notice file is the only one read, so retrykit, credited
	// in THIRD_PARTY_NOTICES.md, now counts as missing.
	pol := writePolicy(t, `{"license": "GPL-3.0-or-later", "notice_files": ["../credits/CREDITS.txt"]}`)
	res := scanProject(t, project("shopfront"), pol, ScanOptions{})
	if res.ProjectLicense != "GPL-3.0-or-later" || res.ProjectLicenseFrom != PolicyFile {
		t.Errorf("project licence %q from %q", res.ProjectLicense, res.ProjectLicenseFrom)
	}
	// GPL-3.0-or-later code in a GPL-3.0-or-later project is fine with attribution.
	if s := statusOf(res, "internal/files/order.go"); s != StatusAttributionMissing {
		t.Errorf("order.go: %s", s)
	}
	// Apache-2.0 goes into GPL-3.0; the policy's notice file doesn't exist.
	if s := statusOf(res, "internal/net/retry.go"); s != StatusAttributionMissing {
		t.Errorf("retry.go: %s", s)
	}
	// AGPL code still can't go into a GPL-3.0 project unchanged.
	if s := statusOf(res, "scripts/slug.py"); s != StatusReview {
		t.Errorf("slug.py: %s", s)
	}
	// The --license flag wins over the policy.
	Evaluate(res, pol, "Apache-2.0")
	if res.ProjectLicense != "Apache-2.0" || statusOf(res, "internal/files/order.go") != StatusConflict {
		t.Errorf("override: %s, order.go %s", res.ProjectLicense, statusOf(res, "internal/files/order.go"))
	}
}

func TestPolicyErrors(t *testing.T) {
	cases := map[string]string{
		`{"fail-on": ["conflict"]}`:                        "unknown field",
		`{"fail_on": ["conflicts"]}`:                       "statuses are",
		`{"allow": [{"component": "x"}]}`:                  "no reason",
		`{"allow": [{"reason": "because"}]}`:               "names no component",
		`{"license": "MIT",}`:                              "invalid character",
		`{"min_tokens": "fifty"}`:                          "cannot unmarshal",
		`{"allow": [{"component": "x", "reason": " "}]}`:   "no reason",
		`{"ignore": ["a"], "notice_files": ["b"], "x": 1}`: "unknown field",
	}
	for body, msg := range cases {
		p := filepath.Join(t.TempDir(), PolicyFile)
		os.WriteFile(p, []byte(body), 0o644)
		_, err := LoadPolicy(p)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: error %v, want %q", body, err, msg)
		}
	}
}

// copyTree copies a fixture project so a test can change it.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// Writing the notices fixes every missing attribution, and doing it again
// adds nothing.
func TestNoticesFixAttribution(t *testing.T) {
	dir := copyTree(t, project("shopfront"))
	res := scanProject(t, dir, nil, ScanOptions{})
	notesPath := filepath.Join(dir, "THIRD_PARTY_NOTICES.md")
	existing, _ := os.ReadFile(notesPath)
	nr := Notices(res, string(existing), false)
	sort.Strings(nr.Written)
	wantWritten := []string{"example.com/fastcsv", "example.net/mplcache", "jsutil@3.1.0", "leftpadx@1.0.4", "tinyjson@1.2.0"}
	if strings.Join(nr.Written, " ") != strings.Join(wantWritten, " ") {
		t.Errorf("written %v, want %v", nr.Written, wantWritten)
	}
	if len(nr.Skipped) != 3 {
		t.Errorf("skipped %v, want the two conflicts and the no-license copy", nr.Skipped)
	}
	for _, s := range []string{"Copyright (c) 2021 Mira Okafor", "Permission is hereby granted, free of charge",
		"Permission to use, copy, modify, and/or distribute", "Redistribution and use in source and binary forms",
		"- Source: example.com/fastcsv", "`internal/csvio/split.go` lines 2-45"} {
		if !strings.Contains(nr.Text, s) {
			t.Errorf("notices lack %q", s)
		}
	}
	if err := os.WriteFile(notesPath, append(existing, nr.Text...), 0o644); err != nil {
		t.Fatal(err)
	}
	// leftpadx has no copyright line anywhere, so its entry waits for one.
	if len(nr.Warnings) != 1 || !strings.Contains(nr.Warnings[0], "leftpadx 1.0.4: copyright line not known") {
		t.Errorf("warnings %v", nr.Warnings)
	}
	res = scanProject(t, dir, nil, ScanOptions{})
	for _, f := range res.Findings {
		if want := f.File == "web/pad.js"; (f.Status == StatusAttributionMissing) != want {
			t.Errorf("%s: %s after notices were written", f.File, f.Status)
		}
		if f.File == "web/pad.js" && !strings.Contains(f.Reason, "isn't finished") {
			t.Errorf("web/pad.js: the reason doesn't say its entry waits: %s", f.Reason)
		}
	}
	finished := strings.Replace(string(existing)+nr.Text, "Copyright: not known. Copy the copyright line from the source before you ship this.",
		"Copyright (c) 2016 The leftpadx authors", 1)
	if err := os.WriteFile(notesPath, []byte(finished), 0o644); err != nil {
		t.Fatal(err)
	}
	res = scanProject(t, dir, nil, ScanOptions{})
	if fails := Failures(res, []string{StatusAttributionMissing}); len(fails) != 0 {
		t.Errorf("failures after notices were finished: %v", fails)
	}
	again, _ := os.ReadFile(notesPath)
	nr2 := Notices(res, string(again), true)
	if len(nr2.Written) != 0 {
		t.Errorf("second run wrote %v again", nr2.Written)
	}
}

// A whole file copied with its copyright header kept is attributed.
func TestCopyWithHeaderIsAttributed(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "upstream", "fastcsv", "fastcsv.go"))
	if err != nil {
		t.Fatal(err)
	}
	header := "// Copyright (c) 2021 Mira Okafor\n// SPDX-License-Identifier: MIT\n\n"
	writeTemp(t, dir, "csv/fastcsv.go", header+string(src))
	writeTemp(t, dir, "LICENSE", "Copyright 2026 Someone\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software, to deal in the Software without restriction, subject to the following conditions: The above copyright notice and this permission notice shall be included.")
	res := scanProject(t, dir, nil, ScanOptions{})
	if len(res.Findings) != 1 || res.Findings[0].Status != StatusAttributed || !res.Findings[0].Match.WholeFile {
		t.Fatalf("findings %+v", res.Findings)
	}
}

// A file the scan can't read is an error, not a quiet skip: the check
// would pass without having looked at it.
func TestUnreadableFilesAreErrors(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions don't stop this user")
	}
	dir := copyTree(t, project("shopfront"))
	locked := filepath.Join(dir, "internal", "files", "order.go")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o644)
	_, err := Scan(dir, ScanOptions{Indexes: []*Index{upstream(t)}})
	if err == nil || !strings.Contains(err.Error(), "internal/files/order.go") {
		t.Errorf("scan with an unreadable file: %v", err)
	}
}

// Look-alike lines must not shift a match: "let a = a.chars()" and "let b =
// b.chars()" have the same shape, and only the names say which is which.
// A whole file of near-identical functions copied as is is one finding.
func TestMatchesLineUpWithTheirSource(t *testing.T) {
	dir := t.TempDir()
	lev := "pub fn levenshtein(a: &str, b: &str) -> usize {\n" +
		"    let a: Vec<char> = a.chars().collect();\n" +
		"    let b: Vec<char> = b.chars().collect();\n" +
		"    let mut prev: Vec<usize> = (0..=b.len()).collect();\n" +
		"    let mut cur = vec![0; b.len() + 1];\n" +
		"    for i in 1..=a.len() {\n" +
		"        cur[0] = i;\n" +
		"        for j in 1..=b.len() {\n" +
		"            let cost = if a[i - 1] == b[j - 1] { 0 } else { 1 };\n" +
		"            cur[j] = (prev[j] + 1).min(cur[j - 1] + 1).min(prev[j - 1] + cost);\n" +
		"        }\n" +
		"        std::mem::swap(&mut prev, &mut cur);\n" +
		"    }\n" +
		"    prev[b.len()]\n" +
		"}\n"
	head := strings.Repeat("// Licensed under the MIT license.\n", 9) + "\n"
	writeTemp(t, dir, "up/strdist/Cargo.toml", "[package]\nname = \"strdist\"\nversion = \"0.3.0\"\nlicense = \"MIT\"\n")
	writeTemp(t, dir, "up/strdist/src/lib.rs", head+lev)
	var many strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&many, "func get%d(m map[string]int, key string) (int, bool) {\n\tv, ok := m[key+\"-%d\"]\n\tif !ok {\n\t\treturn 0, false\n\t}\n\treturn v * %d, true\n}\n\n", i, i, i+2)
	}
	writeTemp(t, dir, "up/getters/LICENSE", string(mustRead(t, filepath.Join("licensetexts", "MIT.txt"))))
	writeTemp(t, dir, "up/getters/get.go", "package getters\n\n"+many.String())
	writeTemp(t, dir, "proj/dist.rs", lev)
	writeTemp(t, dir, "proj/get.go", "package getters\n\n"+many.String())
	ix, err := BuildIndex([]string{filepath.Join(dir, "up")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res := scanProject(t, filepath.Join(dir, "proj"), nil, ScanOptions{Indexes: []*Index{ix}})
	got := map[string]string{}
	for _, f := range res.Findings {
		got[f.File] += fmt.Sprintf("%s<-%s ", f.Lines, f.Source.Lines)
	}
	if want := "1-15<-11-25 "; got["dist.rs"] != want {
		t.Errorf("dist.rs: %q, want %q", got["dist.rs"], want)
	}
	if want := "1-241<-1-241 "; got["get.go"] != want {
		t.Errorf("get.go: %q, want %q", got["get.go"], want)
	}
}

// Two copies from one source file into one project file are two findings
// with two IDs.
func TestFindingIDsAreUnique(t *testing.T) {
	dir := t.TempDir()
	src := mustRead(t, filepath.Join("testdata", "upstream", "fastcsv", "fastcsv.go"))
	writeTemp(t, dir, "csv/a.go", "package csv\n\n"+mergeFunc+"\n"+string(src[strings.Index(string(src), "func "):]))
	ixDir := t.TempDir()
	writeTemp(t, ixDir, "merge/LICENSE", string(mustRead(t, filepath.Join("licensetexts", "MIT.txt"))))
	writeTemp(t, ixDir, "merge/merge.go", "package merge\n\n"+mergeFunc+"\n\nvar x = 1\n\n"+strings.ReplaceAll(mergeFunc, "Merge", "MergeAgain"))
	ix, err := BuildIndex([]string{ixDir}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeTemp(t, dir, "csv/b.go", "package csv\n\n"+mergeFunc+"\n\nfunc unrelated() int {\n\treturn 42\n}\n\n"+strings.ReplaceAll(mergeFunc, "Merge", "MergeAgain"))
	res := scanProject(t, dir, nil, ScanOptions{Indexes: []*Index{ix, upstream(t)}})
	seen := map[string]bool{}
	for _, f := range res.Findings {
		if seen[f.ID] {
			t.Errorf("ID %s used twice", f.ID)
		}
		seen[f.ID] = true
	}
	if len(res.Findings) < 2 {
		t.Errorf("findings %+v", res.Findings)
	}
}

// An index with many near-identical files, like many versions of one
// generated file, can't make the work for one scanned file grow without
// bound: past the limit, the files sharing the most are followed up, and
// the copy is still found.
func TestManyNearIdenticalFilesStayBounded(t *testing.T) {
	old := maxHitsPerFile
	maxHitsPerFile = 300
	defer func() { maxHitsPerFile = old }()
	dir := t.TempDir()
	var body strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&body, "func step%d(x, y int) int {\n\tif x > %d {\n\t\treturn x*%d + y\n\t}\n\tfor k := 0; k < %d; k++ {\n\t\ty += k ^ x\n\t}\n\treturn y - %d\n}\n\n", i, i*7+3, i*5+2, i+9, i*3+1)
	}
	writeTemp(t, dir, "up/comp/LICENSE", string(mustRead(t, filepath.Join("licensetexts", "MIT.txt"))))
	for v := 0; v < 40; v++ {
		writeTemp(t, dir, fmt.Sprintf("up/comp/v%02d.go", v), fmt.Sprintf("package comp\n\nconst V = %d\n\n", v)+body.String())
	}
	writeTemp(t, dir, "proj/copy.go", "package proj\n\n"+body.String())
	ix, err := BuildIndex([]string{filepath.Join(dir, "up")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res := scanProject(t, filepath.Join(dir, "proj"), nil, ScanOptions{Indexes: []*Index{ix}})
	if len(res.Findings) != 1 || res.Findings[0].Lines.len() < 100 {
		t.Fatalf("findings %+v", res.Findings)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mergeFunc is a function long enough to be reported when copied.
const mergeFunc = "func Merge(a, b []int) []int {\n\tout := make([]int, 0, len(a)+len(b))\n\ti, j := 0, 0\n" +
	"\tfor i < len(a) && j < len(b) {\n\t\tif a[i] <= b[j] {\n\t\t\tout = append(out, a[i])\n\t\t\ti++\n" +
	"\t\t} else {\n\t\t\tout = append(out, b[j])\n\t\t\tj++\n\t\t}\n\t}\n\tout = append(out, a[i:]...)\n" +
	"\tout = append(out, b[j:]...)\n\treturn out\n}\n"

// A header that only says where the code came from can't make GPL code
// pass as MIT: the copy keeps both licences and is a conflict in an
// unlicensed project.
func TestHeaderCantHideCopyleft(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "up/gplmerge/COPYING", "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\nTERMS AND CONDITIONS\n0. Definitions.\n")
	writeTemp(t, dir, "up/gplmerge/merge.go", "// Based on code from fastmerge, released under the MIT license.\n\npackage merge\n\n"+mergeFunc)
	writeTemp(t, dir, "proj/merge.go", "package proj\n\n"+mergeFunc)
	ix, err := BuildIndex([]string{filepath.Join(dir, "up")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res := scanProject(t, filepath.Join(dir, "proj"), nil, ScanOptions{Indexes: []*Index{ix}})
	if len(res.Findings) != 1 {
		t.Fatalf("findings %+v", res.Findings)
	}
	if f := res.Findings[0]; f.Status != StatusConflict || f.Source.License != "GPL-3.0-only AND MIT" {
		t.Errorf("%s from %s: %s", f.Status, f.Source.License, f.Reason)
	}
}

// A table of constants is the same in every language that has it, like
// SHA-256's round constants in Go and in C: it isn't code that was copied.
func TestConstantTablesDontMatch(t *testing.T) {
	dir := t.TempDir()
	var rows strings.Builder
	for r := 0; r < 8; r++ {
		rows.WriteString("\t")
		for c := 0; c < 8; c++ {
			fmt.Fprintf(&rows, "0x%08x, ", uint32((r*8+c+1)*0x9e3779b9))
		}
		rows.WriteString("\n")
	}
	writeTemp(t, dir, "up/sha/COPYING", "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\nTERMS AND CONDITIONS\n0. Definitions.\n")
	writeTemp(t, dir, "up/sha/k.c", "static const unsigned int K[64] = {\n"+rows.String()+"};\n")
	writeTemp(t, dir, "proj/k.go", "package sha\n\nvar _K = [64]uint32{\n"+rows.String()+"}\n")
	ix, err := BuildIndex([]string{filepath.Join(dir, "up")}, IndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res := scanProject(t, filepath.Join(dir, "proj"), nil, ScanOptions{Indexes: []*Index{ix}}); len(res.Findings) > 0 {
		t.Errorf("a table of constants matched: %+v", res.Findings[0])
	}
}

func TestReports(t *testing.T) {
	res := scanProject(t, project("shopfront"), nil, ScanOptions{})
	var html bytes.Buffer
	if err := WriteHTML(&html, res); err != nil {
		t.Fatal(err)
	}
	page := html.String()
	for _, s := range []string{"<!doctype html>", "internal/files/order.go", "CONFLICT", "ATTRIBUTION MISSING",
		"example.org/gplsort", "not legal advice", "p &lt; len(left) &amp;&amp; q &lt; len(right)"} {
		if !strings.Contains(strings.ToLower(page), strings.ToLower(s)) {
			t.Errorf("HTML report lacks %q", s)
		}
	}
	if strings.Contains(page, "<script src") || strings.Contains(page, "<link rel=\"stylesheet\"") {
		t.Errorf("HTML report isn't self-contained")
	}

	var sp bytes.Buffer
	if err := WriteSPDX(&sp, res); err != nil {
		t.Fatal(err)
	}
	var doc spdxDoc
	if err := json.Unmarshal(sp.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SPDXVersion != "SPDX-2.3" || doc.SPDXID != "SPDXRef-DOCUMENT" || doc.DataLicense != "CC0-1.0" {
		t.Errorf("SPDX header %q %q %q", doc.SPDXVersion, doc.SPDXID, doc.DataLicense)
	}
	if len(doc.Snippets) != len(res.Findings) {
		t.Errorf("%d snippets for %d findings", len(doc.Snippets), len(res.Findings))
	}
	ids := map[string]bool{}
	for _, f := range doc.Files {
		ids[f.SPDXID] = true
		if len(f.Checksums) == 0 || f.Checksums[0].Algorithm != "SHA1" {
			t.Errorf("file %s has no SHA1", f.FileName)
		}
	}
	byFile := map[string]string{}
	for _, f := range doc.Files {
		byFile[f.SPDXID] = strings.TrimPrefix(f.FileName, "./")
	}
	for i, s := range doc.Snippets {
		if !ids[s.SnippetFromFile] {
			t.Errorf("snippet %s points to missing file %s", s.SPDXID, s.SnippetFromFile)
		}
		// SPDX 2.3 needs a byte range on every snippet; the line range
		// comes second. The bytes must hold exactly the reported lines.
		if s.LicenseConcluded == "" || len(s.Ranges) != 2 || s.Ranges[0].Start.Offset < 1 || s.Ranges[1].Start.LineNumber < 1 {
			t.Errorf("snippet %s: %+v", s.SPDXID, s)
			continue
		}
		data := mustRead(t, filepath.Join(project("shopfront"), filepath.FromSlash(byFile[s.SnippetFromFile])))
		got := string(data[s.Ranges[0].Start.Offset-1 : s.Ranges[0].End.Offset])
		lines := strings.SplitAfter(string(data), "\n")
		want := strings.Join(lines[res.Findings[i].Lines.Start-1:res.Findings[i].Lines.End], "")
		if got != want {
			t.Errorf("snippet %s: bytes %d-%d hold %.40q..., want lines %s", s.SPDXID, s.Ranges[0].Start.Offset, s.Ranges[0].End.Offset, got, res.Findings[i].Lines)
		}
	}

	var js bytes.Buffer
	clean := scanProject(t, project("clean"), nil, ScanOptions{})
	if err := WriteJSON(&js, clean); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"findings": []`) {
		t.Errorf("empty findings aren't an empty list:\n%s", js.String())
	}
}

func TestScanErrors(t *testing.T) {
	if _, err := Scan(filepath.Join("testdata", "nope"), ScanOptions{}); err == nil {
		t.Errorf("missing folder scanned")
	}
	if _, err := Scan(filepath.Join("testdata", "projects", "shopfront", "go.mod"), ScanOptions{}); err == nil {
		t.Errorf("file scanned as a project")
	}
	if _, err := BuildIndex([]string{filepath.Join("testdata", "nope")}, IndexOptions{}); err == nil {
		t.Errorf("missing folder indexed")
	}
	if _, _, _, err := ImportResults(filepath.Join("testdata", "nope.json"), "."); err == nil {
		t.Errorf("missing results file read")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`["not", "results"]`), 0o644)
	if _, _, _, err := ImportResults(bad, "."); err == nil {
		t.Errorf("foreign JSON read as results")
	}
}

// A results file can come from someone else. Its paths must not make
// Remixware read files outside the project into a report.
func TestResultsFilesCantReadOutsideTheProject(t *testing.T) {
	dir := t.TempDir()
	secret := writeTemp(t, dir, "secret.txt", "TOP-SECRET-LINE-1\nTOP-SECRET-LINE-2\nTOP-SECRET-LINE-3\n")
	match := `[{"id":"file","lines":"all","oss_lines":"all","matched":"100%","component":"x","licenses":[{"name":"MIT","source":"license_file"}]}]`
	scanoss := filepath.Join(dir, "scanoss.json")
	os.WriteFile(scanoss, []byte(`{"../secret.txt": `+match+`, "/etc/passwd": `+match+`, "C:\\Windows\\win.ini": `+match+
		`, "web/pad.js": `+match+`}`), 0o644)
	proj := project("shopfront")
	found, _, _, err := ImportResults(scanoss, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "web/pad.js" {
		t.Fatalf("findings %+v", found)
	}

	// A Remixware findings file whose source points at a private file.
	res := scanProject(t, proj, nil, ScanOptions{})
	res.Findings[0].Source.Abs = secret
	res.Findings[1].File = "../../" + filepath.Base(dir) + "/secret.txt"
	var js bytes.Buffer
	WriteJSON(&js, res)
	path := filepath.Join(dir, "findings.json")
	os.WriteFile(path, js.Bytes(), 0o644)
	back, in, texts, err := ImportResults(path, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(res.Findings)-1 {
		t.Errorf("read %d findings, want %d (the one outside the project dropped)", len(back), len(res.Findings)-1)
	}
	out, _ := NewResults(proj)
	out.Add(back, in, texts)
	Evaluate(out, nil, "")
	var html bytes.Buffer
	if err := WriteHTML(&html, out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "TOP-SECRET") {
		t.Errorf("the report holds a file from outside the project")
	}
}
