package remixware

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Remixware's fingerprints have to be the ones scanoss.py makes, or the
// SCANOSS service won't match them. testdata/scanoss/wfp/expected.wfp was
// written by scanoss.py itself (scripts/wfp-golden.py), for files chosen for
// the edge cases: Windows and old Mac line endings, a file of fewer than 257
// characters but more bytes, text that starts like JSON, a very long first
// line, a minified file, non-ASCII letters, no newline at the end.
func TestWFPMatchesScanossPy(t *testing.T) {
	dir := filepath.Join("testdata", "scanoss", "wfp")
	want, err := os.ReadFile(filepath.Join(dir, "expected.wfp"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split("\n"+string(want), "\nfile=")[1:]
	if len(blocks) < 10 {
		t.Fatalf("expected.wfp has %d files", len(blocks))
	}
	for _, blk := range blocks {
		blk = "file=" + blk
		if !strings.HasSuffix(blk, "\n") {
			blk += "\n"
		}
		name := strings.SplitN(strings.SplitN(blk, "\n", 2)[0], ",", 3)[2]
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := WFP(name, name, data); got != blk {
			t.Errorf("%s: fingerprints differ from scanoss.py's\ngot:\n%s\nwant:\n%s", name, got, blk)
		}
	}
}

func TestPySuffix(t *testing.T) {
	for in, want := range map[string]string{"a/b.go": ".go", "x.tar.gz": ".gz", ".bashrc": "", "file.": "", "Makefile": "", "d.x/f": ""} {
		if got := pySuffix(in); got != want {
			t.Errorf("pySuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSCANOSSURL(t *testing.T) {
	cases := []struct{ url, key, want, dropped string }{
		{"", "", "https://api.osskb.org/scan/direct", ""},
		{"", "k", "https://api.scanoss.com/scan/direct", ""},
		{"https://scan.example", "", "https://scan.example/scan/direct", ""},
		{" https://scan.example/ ", "k", "https://scan.example/scan/direct", ""},
		{"https://scan.example/scan/direct", "", "https://scan.example/scan/direct", ""},
		// As SCANOSS's scanner does, which reads the same SCANOSS_SCAN_URL:
		// the host, then /scan/direct.
		{"http://localhost:8080/custom/scan", "", "http://localhost:8080/scan/direct", "/custom/scan"},
		{"https://bot:pw@scan.example/scan/direct?token=t#x", "", "https://bot:pw@scan.example/scan/direct", ""},
	}
	for _, c := range cases {
		got, dropped, err := scanossURL(c.url, c.key)
		if err != nil || got != c.want || dropped != c.dropped {
			t.Errorf("scanossURL(%q, %q) = %q, %q, %v; want %q, %q", c.url, c.key, got, dropped, err, c.want, c.dropped)
		}
	}
	for _, bad := range []string{"ftp://scan.example", "scan.example/scan", "https://", "https://bot:secret@/x"} {
		if _, _, err := scanossURL(bad, ""); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("scanossURL(%q) = %v", bad, err)
		}
	}
	if got := printable("a\x1b]0;x\x07\nb\u202e c\t\td", 100); got != "a ]0;x b c d" {
		t.Errorf("printable = %q", got)
	}
	if got := printable(strings.Repeat("é", 10), 4); got != "éééé..." {
		t.Errorf("printable cut = %q", got)
	}
}

// fakeSCANOSS stands in for the SCANOSS service. It records each request
// and answers every name it was sent with answer(name, md5), or with no
// match.
type fakeSCANOSS struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []fakeRequest
	answer func(name, md5 string) string
	extra  map[string]string // answers about names that weren't sent
	status func(n int) (int, http.Header, string)
	drop   func(n int, name string) bool // leave a name out of answer n
	delay  time.Duration
}

type fakeRequest struct {
	wfp, format string
	names       []string
	header      http.Header
}

var wfpFileLine = regexp.MustCompile(`(?m)^file=([0-9a-f]{32}),(\d+),(.*)$`)

func (f *fakeSCANOSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-r.Context().Done():
			return
		}
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		f.t.Errorf("request isn't multipart: %v", err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		f.t.Errorf("no file field: %v", err)
		return
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := file.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	req := fakeRequest{wfp: sb.String(), format: r.FormValue("format"), header: r.Header.Clone()}
	f.mu.Lock()
	n := len(f.reqs) + 1
	f.mu.Unlock()
	answer := map[string]json.RawMessage{}
	for _, m := range wfpFileLine.FindAllStringSubmatch(req.wfp, -1) {
		req.names = append(req.names, m[3])
		if f.drop != nil && f.drop(n, m[3]) {
			continue
		}
		a := `[{"id": "none"}]`
		if f.answer != nil {
			if s := f.answer(m[3], m[1]); s != "" {
				a = s
			}
		}
		answer[m[3]] = json.RawMessage(a)
	}
	for k, v := range f.extra {
		answer[k] = json.RawMessage(v)
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if f.status != nil {
		if code, h, body := f.status(n); code != 0 {
			for k, vs := range h {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(code)
			fmt.Fprint(w, body)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func md5Of(t *testing.T, path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func noSleep(context.Context, time.Duration) error { return nil }

const fastcsvMatch = `[{"id": "snippet", "lines": "2-45", "oss_lines": "2-45", "matched": "100%",
  "purl": ["pkg:github/mokafor/fastcsv"], "vendor": "mokafor", "component": "fastcsv", "version": "1.4.0",
  "url": "https://example.com/mokafor/fastcsv", "file": "fastcsv.go",
  "licenses": [{"name": "MIT", "source": "component_declared"}],
  "copyrights": [{"name": "Copyright (c) 2021 Mira Okafor"}]}]`

func TestSCANOSSService(t *testing.T) {
	shop := project("shopfront")
	split := md5Of(t, filepath.Join(shop, "internal", "csvio", "split.go"))
	fake := &fakeSCANOSS{t: t, answer: func(name, sum string) string {
		if sum == split {
			return fastcsvMatch
		}
		return ""
	}, extra: map[string]string{"../../../etc/passwd": fastcsvMatch, "9999.go": fastcsvMatch}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(shop, SCANOSSOptions{URL: srv.URL, sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	files, _, _, _ := projectFiles(mustReal(t, shop), nil, false, isSource)
	if r.Files != len(files) || r.Files == 0 {
		t.Errorf("sent %d files, the shop has %d source files", r.Files, len(files))
	}
	if r.URL != srv.URL+"/scan/direct" {
		t.Errorf("URL %s", r.URL)
	}
	// Only numbers and endings go out, never the project's paths.
	sent := 0
	for _, q := range fake.reqs {
		if q.format != "plain" {
			t.Errorf("format %q", q.format)
		}
		if ua := q.header.Get("User-Agent"); !strings.HasPrefix(ua, "remixware/") {
			t.Errorf("User-Agent %q", ua)
		}
		if q.header.Get("x-request-id") == "" || q.header.Get("x-api-key") != "" || q.header.Get("X-Session") != "" {
			t.Errorf("headers %v", q.header)
		}
		for _, n := range q.names {
			if !regexp.MustCompile(`^\d+(\.[A-Za-z0-9]+)?$`).MatchString(n) {
				t.Errorf("sent the name %q", n)
			}
		}
		for _, f := range files {
			rel := relSlash(mustReal(t, shop), f)
			if strings.Contains(q.wfp, rel) || strings.Contains(q.wfp, filepath.Base(rel)) {
				t.Errorf("the fingerprints name %s", rel)
			}
		}
		sent += len(q.names)
	}
	if sent != r.Files {
		t.Errorf("%d names sent for %d files", sent, r.Files)
	}
	// The answers come back under the project's paths, and answers about
	// names that weren't sent are dropped.
	if len(r.Results) != r.Files {
		t.Errorf("%d results for %d files", len(r.Results), r.Files)
	}
	for k := range r.Results {
		if _, ok := projectPath(k); !ok || strings.Contains(k, "passwd") || k == "9999.go" {
			t.Errorf("result for %q", k)
		}
	}
	found, in, err := r.Findings(shop)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "internal/csvio/split.go" || found[0].Source.Component != "mokafor/fastcsv" ||
		found[0].Source.License != "MIT" || found[0].Lines != (Range{2, 45}) {
		t.Fatalf("findings %+v", found)
	}
	if in.Kind != "scanoss-api" || in.Files != r.Files || in.Matches != 1 {
		t.Errorf("input %+v", in)
	}
	// Saved, the answers read back with --from as the same findings.
	data, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "scanoss.json")
	os.WriteFile(saved, data, 0o644)
	again, ain, _, err := ImportResults(saved, mustReal(t, shop))
	if err != nil || len(again) != 1 || again[0].File != found[0].File || again[0].Source.Key != found[0].Source.Key || ain.Kind != "scanoss" {
		t.Errorf("read back: %+v, %+v, %v", again, ain, err)
	}
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := realPath(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSCANOSSKey(t *testing.T) {
	fake := &fakeSCANOSS{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if _, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, APIKey: "secret-key", sleep: noSleep}); err != nil {
		t.Fatal(err)
	}
	for _, q := range fake.reqs {
		if q.header.Get("x-api-key") != "secret-key" || q.header.Get("X-Session") != "secret-key" {
			t.Errorf("key headers %v", q.header)
		}
	}
}

func TestSCANOSSWhatIsSent(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("func f() int { return 42 } // padding for the snippet threshold\n", 8)
	for i := 0; i < 70; i++ {
		writeTemp(t, dir, fmt.Sprintf("pkg/f%02d.go", i), fmt.Sprintf("package pkg\n\n// file %d\n%s", i, long))
	}
	writeTemp(t, dir, "web/lib.min.js", strings.Repeat("function a(b){return b*2}", 400))
	writeTemp(t, dir, "gen/x.pb.go", "// Code generated by protoc-gen-go. DO NOT EDIT.\npackage gen\n"+long)
	writeTemp(t, dir, "bin/blob.go", "package bin\n\x00\x01\x02"+long)
	writeTemp(t, dir, "vendor/lib/v.go", "package lib\n"+long)
	writeTemp(t, dir, "skipme/s.go", "package skipme\n"+long)
	writeTemp(t, dir, "notes.txt", long)
	fake := &fakeSCANOSS{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(dir, SCANOSSOptions{URL: srv.URL, Exclude: []string{"skipme/**"}, Workers: 2, sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	// 70 Go files and the minified library; not the generated, binary,
	// vendored, excluded or non-source files.
	if r.Files != 71 {
		t.Errorf("sent %d files, want 71", r.Files)
	}
	if r.Requests != len(fake.reqs) || r.Requests < 3 {
		t.Errorf("%d requests made, %d counted", len(fake.reqs), r.Requests)
	}
	for _, q := range fake.reqs {
		if len(q.names) > scanossPostFiles || (len(q.names) > 1 && len(q.wfp) > scanossPostSize) {
			t.Errorf("a request with %d files and %d bytes", len(q.names), len(q.wfp))
		}
	}
	if _, ok := r.Results["web/lib.min.js"]; !ok {
		t.Error("the minified library wasn't sent")
	}
	for k := range r.Results {
		if strings.HasPrefix(k, "gen/") || strings.HasPrefix(k, "bin/") || strings.HasPrefix(k, "vendor/") || strings.HasPrefix(k, "skipme/") {
			t.Errorf("%s was sent", k)
		}
	}
	r2, err := ScanSCANOSS(dir, SCANOSSOptions{URL: srv.URL, IncludeDeps: true, Exclude: []string{"skipme/**"}, sleep: noSleep})
	if err != nil || r2.Files != 72 {
		t.Errorf("with --include-deps: %v files, %v", r2.Files, err)
	}
	empty := t.TempDir()
	r3, err := ScanSCANOSS(empty, SCANOSSOptions{URL: srv.URL, sleep: noSleep})
	if err != nil || r3.Files != 0 || r3.Requests != 0 {
		t.Errorf("empty project: %+v, %v", r3, err)
	}
}

func TestSCANOSSWaitsAndTriesAgain(t *testing.T) {
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	fake := &fakeSCANOSS{t: t, status: func(n int) (int, http.Header, string) {
		switch n {
		case 1:
			return http.StatusServiceUnavailable, http.Header{"Retry-After": {"2"}}, "down for a moment"
		case 2:
			return http.StatusTooManyRequests, nil, `{"retry_after": 3}`
		case 3:
			return http.StatusTooManyRequests, http.Header{"Retry-After": {"90"}}, ""
		case 4:
			return http.StatusBadGateway, nil, "bad gateway"
		}
		return 0, nil, ""
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, Workers: 1, sleep: sleep})
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{2 * time.Second, 3 * time.Second, time.Minute, 5 * time.Second}
	if fmt.Sprint(waits) != fmt.Sprint(want) {
		t.Errorf("waited %v, want %v", waits, want)
	}
	if r.Files == 0 || len(r.Results) != r.Files {
		t.Errorf("results %d for %d files", len(r.Results), r.Files)
	}
}

// SCANOSS's own sample of its service's answers, from scanoss.py's tests,
// goes through the whole path: the fingerprints go out under numbers, the
// answers come back about those numbers, and each turns into a finding with
// the licence from the most specific place SCANOSS names. Entries with no
// licence, a null copyright list and "lines": "all" are in the sample.
func TestSCANOSSRealAnswers(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "scanoss", "scanoss-py-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sample map[string]json.RawMessage
	if err := json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	byMD5 := map[string]json.RawMessage{}
	for name, answer := range sample {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "int %s_%d = %d;\n", strings.NewReplacer("/", "_", ".", "_").Replace(name), i, i)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		byMD5[md5Of(t, path)] = answer
	}
	fake := &fakeSCANOSS{t: t, answer: func(_, sum string) string { return string(byMD5[sum]) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(dir, SCANOSSOptions{URL: srv.URL, sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	// Eleven C files go out; package.json isn't source.
	if r.Files != 11 || len(r.Results) != 11 {
		t.Errorf("sent %d files, %d answers", r.Files, len(r.Results))
	}
	found, in, err := r.Findings(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewResults(dir)
	if err != nil {
		t.Fatal(err)
	}
	res.Add(found, in, nil)
	Evaluate(res, nil, "MIT")
	whole := Range{1, 40}
	checkFindings(t, res, []want{
		{StatusConflict, "src/winnowing.c", Range{32, 161}, "scanoss/engine", "GPL-2.0-or-later"},
		{StatusConflict, "inc/winnowing.h", whole, "scanoss/engine", "GPL-2.0-only"},
		{StatusConflict, "inc/log.h", whole, "scanoss/scanner.c", "GPL-2.0-only"},
		{StatusConflict, "src/log.c", whole, "scanoss/scanner.c", "GPL-2.0-only"},
		{StatusAttributionMissing, "inc/json.h", whole, "scanoss/scanner.c", "BSD-2-Clause"},
		{StatusAttributionMissing, "src/json.c", whole, "scanoss/scanner.c", "BSD-2-Clause"},
		{StatusAttributionMissing, "src/crc32c.c", whole, "scanoss/wfp", "Zlib"},
		{StatusReview, "inc/component.c", whole, "scanoss/scanner.c", LicenseNoAssertion},
		{StatusReview, "inc/helper.c", whole, "scanoss/scanner.c", LicenseNoAssertion},
		{StatusReview, "inc/log.c", whole, "scanoss/scanner.c", LicenseNoAssertion},
	})
	for _, f := range res.Findings {
		if f.File == "src/winnowing.c" && (f.Source.Lines != Range{29, 158} || f.Source.LicenseFrom != "SCANOSS (file spdx tag)" || f.Match.Kind != "snippet") {
			t.Errorf("snippet: %+v", f)
		}
	}
}

// Quirks of the live service's answers, as it gave them on 2026-10-26: a
// missing vendor written as "None", a blank file_url, no copyrights key, a
// hits count and two package URLs.
func TestSCANOSSServiceQuirks(t *testing.T) {
	answer := `[{"id": "snippet","hits": 10,"lines": "7-31","oss_lines": "126-150","matched": "77%",
  "file_hash": "00000000000000000000000000000002","source_hash": "00000000000000000000000000000003","file_url": " ",
  "purl": ["pkg:npm/tinyfmt","pkg:github/example/tinyfmt"],"vendor": "None","component": "tinyfmt","version": "2.0.1",
  "latest": "2.1.0","url": "https://example.com/tinyfmt","status": "pending","release_date": "2018-07-21","file": "fmt.js",
  "url_hash": "00000000000000000000000000000004","licenses": [{"name": "MIT","patent_hints": "no", "copyleft": "no",
  "checklist_url": "https://example.com/MIT.txt","osadl_updated": "2026-02-12T16:31:00+0000","source": "component_declared",
  "url": "https://spdx.org/licenses/MIT.html"}],"server": {"version": "5.4.27","kb_version": {"monthly":"26.10", "daily":"26.10.19"}}}]`
	found, err := scanossFindings(map[string]json.RawMessage{"web/fmt.js": json.RawMessage(answer)}, "quirks", t.TempDir(), false)
	if err != nil || len(found) != 1 {
		t.Fatalf("%v, %d findings", err, len(found))
	}
	f := found[0]
	if f.Source.Component != "tinyfmt" || f.Source.PURL != "pkg:npm/tinyfmt" || f.Source.Version != "2.0.1" ||
		f.Lines != (Range{7, 31}) || f.Source.Lines != (Range{126, 150}) || f.Source.License != "MIT" || f.Match.Coverage != 0.77 {
		t.Errorf("finding %+v", f)
	}
}

// A service that asks for a longer wait than the tries left could cover
// gets no more requests, and the error says how long and until when. The
// free service answers a used-up monthly limit this way.
func TestSCANOSSAsksToWait(t *testing.T) {
	now := time.Date(2026, 10, 26, 11, 50, 22, 300e6, time.UTC)
	clock := func() time.Time { return now }
	cases := []struct {
		name    string
		status  int
		header  http.Header
		body    string
		key     string
		retries int
		want    []string
		not     string
	}{
		{"monthly limit", 503, nil, `{"error":"Rate limit exceeded","retry_after":475777.699103489}`, "", 0,
			[]string{"asks Remixware to wait 5.5 days, until 2026-11-01 00:00 UTC (HTTP 503): " +
				`{"error":"Rate limit exceeded","retry_after":475777.699103489}. Try again then, or use a SCANOSS API key (--scanoss-key)`}, ""},
		{"with a key", 429, http.Header{"Retry-After": {"86400"}}, "", "k", 0,
			[]string{"asks Remixware to wait 24 hours, until 2026-10-27 11:50 UTC (HTTP 429). Try again then"}, "--scanoss-key"},
		{"a date", 503, http.Header{"Retry-After": {now.Add(3 * time.Hour).Format(http.TimeFormat)}}, "", "", 0,
			[]string{"asks Remixware to wait 3 hours, until 2026-10-26 14:50 UTC (HTTP 503). Try"}, ""},
		{"last try", 429, http.Header{"Retry-After": {"2"}}, "", "", -1,
			[]string{"asks Remixware to wait 2 seconds (HTTP 429). Try again then"}, ""},
	}
	for _, c := range cases {
		var waits []time.Duration
		fake := &fakeSCANOSS{t: t, status: func(int) (int, http.Header, string) { return c.status, c.header, c.body }}
		srv := httptest.NewServer(fake)
		_, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, APIKey: c.key, Retries: c.retries, Workers: 1, now: clock,
			sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }})
		srv.Close()
		for _, w := range c.want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %v, want %q", c.name, err, w)
			}
		}
		if c.not != "" && strings.Contains(fmt.Sprint(err), c.not) {
			t.Errorf("%s: error %v mentions %q", c.name, err, c.not)
		}
		if len(fake.reqs) != 1 || len(waits) != 0 {
			t.Errorf("%s: %d requests and waits %v, want one request and no wait", c.name, len(fake.reqs), waits)
		}
	}

	// A wait the tries left can cover is waited, a minute at a time.
	var waits []time.Duration
	fake := &fakeSCANOSS{t: t, status: func(n int) (int, http.Header, string) {
		if n == 1 {
			return 429, http.Header{"Retry-After": {"150"}}, ""
		}
		return 0, nil, ""
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if _, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, Retries: 3, Workers: 1, now: clock,
		sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(waits) != fmt.Sprint([]time.Duration{time.Minute}) {
		t.Errorf("waited %v, want 1m", waits)
	}
}

func TestServiceWait(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		header, body string
		want         time.Duration
		ok           bool
	}{
		{"7", "", 7 * time.Second, true},
		{" 1.5 ", "", 1500 * time.Millisecond, true},
		{"-3", `{"retry_after": 4}`, 4 * time.Second, true},
		{"", `{"retry_after": 0.25}`, 250 * time.Millisecond, true},
		{"Inf", "", 365 * 24 * time.Hour, true},
		{"1e300", "", 365 * 24 * time.Hour, true},
		{"NaN", "", 0, false},
		{"soon", `{"retry_after": "4"}`, 0, false},
		{"", `{"retry_after": -1}`, 0, false},
		{"", "not json", 0, false},
		{now.Add(-time.Hour).Format(http.TimeFormat), "", 0, true},
		{now.Add(90 * time.Second).Format(http.TimeFormat), "", 90 * time.Second, true},
	}
	for _, c := range cases {
		got, ok := serviceWait(c.header, []byte(c.body), now)
		if got != c.want || ok != c.ok {
			t.Errorf("serviceWait(%q, %q) = %v %v, want %v %v", c.header, c.body, got, ok, c.want, c.ok)
		}
	}
	texts := map[time.Duration]string{
		time.Second:             "1 second",
		1400 * time.Millisecond: "1 second",
		89 * time.Second:        "89 seconds",
		90 * time.Second:        "2 minutes",
		time.Minute * 61:        "61 minutes",
		90 * time.Minute:        "1.5 hours, until 2026-10-26 13:30 UTC",
		47 * time.Hour:          "47 hours, until 2026-10-28 11:00 UTC",
		48 * time.Hour:          "2 days, until 2026-10-28 12:00 UTC",
		24 * 365 * time.Hour:    "365 days, until 2027-10-26 12:00 UTC",
	}
	for d, want := range texts {
		if got := waitText(d, now); got != want {
			t.Errorf("waitText(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSCANOSSErrors(t *testing.T) {
	clean := project("clean")
	cases := []struct {
		name     string
		fake     *fakeSCANOSS
		retries  int
		want     string
		requests int
	}{
		{"bad key", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 401, nil, "invalid key" }}, 0, "Check the API key", 1},
		{"bad request", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 400, nil, "bad\x1b[31m wfp" }}, 0, "refused the request (HTTP 400): bad [31m wfp", 1},
		{"rate limit", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 429, nil, "slow down" }}, 2, "rate limit was reached (HTTP 429", 3},
		{"server error", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 500, nil, "oops" }}, 1, "unavailable (HTTP 500", 2},
		{"not JSON", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 200, nil, "<html>hello</html>" }}, 0, "isn't SCANOSS results", 1},
		{"not a list", &fakeSCANOSS{answer: func(string, string) string { return `{"id": "none"}` }}, 0, "isn't a list of matches", 1},
	}
	for _, c := range cases {
		c.fake.t = t
		srv := httptest.NewServer(c.fake)
		retries := c.retries
		if retries == 0 {
			retries = -1
		}
		_, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Retries: retries, Workers: 1, sleep: noSleep})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
		if strings.ContainsRune(fmt.Sprint(err), '\x1b') {
			t.Errorf("%s: the error carries a control character", c.name)
		}
		if len(c.fake.reqs) != c.requests {
			t.Errorf("%s: %d requests, want %d", c.name, len(c.fake.reqs), c.requests)
		}
	}

	// An answer too big to read.
	old := scanossMaxResponse
	scanossMaxResponse = 100
	srv := httptest.NewServer(&fakeSCANOSS{t: t})
	_, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Retries: -1, sleep: noSleep})
	srv.Close()
	scanossMaxResponse = old
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Errorf("big answer: %v", err)
	}

	// No service there.
	srv = httptest.NewServer(&fakeSCANOSS{t: t})
	srv.Close()
	if _, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Retries: 1, sleep: noSleep}); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("closed server: %v", err)
	}

	// A service that doesn't answer in time.
	slow := &fakeSCANOSS{t: t, delay: time.Second}
	srv = httptest.NewServer(slow)
	defer srv.Close()
	start := time.Now()
	if _, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Timeout: 50 * time.Millisecond, Retries: 1, sleep: noSleep}); err == nil ||
		!strings.Contains(err.Error(), "no answer") || time.Since(start) > 900*time.Millisecond {
		t.Errorf("slow server: %v after %v", err, time.Since(start))
	}
}

// An answer that leaves out files that were sent is asked again, and a
// scan never passes on files the service didn't answer about: the service
// drops the files of any part of its work that fails and still answers 200.
func TestSCANOSSIncompleteAnswers(t *testing.T) {
	clean := project("clean")
	cases := []struct {
		name string
		fake *fakeSCANOSS
	}{
		{"empty object", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 200, nil, "{}" }}},
		{"null", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 200, nil, "null" }}},
		{"another object", &fakeSCANOSS{status: func(int) (int, http.Header, string) { return 200, nil, `{"status":"ok","version":"1.0"}` }}},
		{"one file left out", &fakeSCANOSS{drop: func(_ int, name string) bool { return name == "1.go" }}},
		{"null entries", &fakeSCANOSS{answer: func(string, string) string { return "null" }}},
		{"empty lists", &fakeSCANOSS{answer: func(string, string) string { return "[]" }}},
	}
	for _, c := range cases {
		c.fake.t = t
		srv := httptest.NewServer(c.fake)
		var said []string
		_, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Retries: 1, Workers: 1, sleep: noSleep,
			Progress: func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "files sent, after 2 tries") || !strings.Contains(err.Error(), "covers") {
			t.Errorf("%s: error %v", c.name, err)
		}
		if len(c.fake.reqs) != 2 || len(said) < 2 || !strings.Contains(said[len(said)-1], "asking again in 5 seconds (try 2 of 2)") {
			t.Errorf("%s: %d requests, said %q", c.name, len(c.fake.reqs), said)
		}
	}

	// A file left out of the first answer and answered on the second try.
	fake := &fakeSCANOSS{t: t, drop: func(n int, name string) bool { return n == 1 && strings.HasPrefix(name, "2.") }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(clean, SCANOSSOptions{URL: srv.URL, Workers: 1, sleep: noSleep})
	if err != nil || len(r.Results) != r.Files || len(fake.reqs) != 2 {
		t.Errorf("second try: %v, %d answers for %d files, %d requests", err, len(r.Results), r.Files, len(fake.reqs))
	}
}

// A redirect isn't followed: it could take the key and the fingerprints to
// any host, over plain http too.
func TestSCANOSSRedirect(t *testing.T) {
	var got []string
	var mu sync.Mutex
	collect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("x-api-key"))
		mu.Unlock()
	}))
	defer collect.Close()
	for _, code := range []int{301, 302, 307, 308} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://bot:pw@"+collect.Listener.Addr().String()+"/collect?token=T", code)
		}))
		client := srv.Client()
		client.CheckRedirect = nil
		// The test server's client trusts its certificate; Remixware's own
		// client settings are what's under test, so only its transport is
		// borrowed.
		_, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, APIKey: "SECRET", Retries: -1, sleep: noSleep,
			Client: noRedirects(client)})
		srv.Close()
		want := "answered with a redirect to http://" + collect.Listener.Addr().String() + "/collect (HTTP "
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "token") {
			t.Errorf("HTTP %d: error %v", code, err)
		}
	}
	if len(got) != 0 {
		t.Errorf("the redirect target got %d requests, keys %q", len(got), got)
	}
}

// noRedirects is the client ScanSCANOSS makes when it's given none, with
// another client's transport.
func noRedirects(c *http.Client) *http.Client {
	return &http.Client{Transport: c.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// The default client doesn't follow redirects either.
func TestSCANOSSDefaultClientNoRedirects(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/scan/direct", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: srv.URL, APIKey: "SECRET", Retries: -1, sleep: noSleep})
	if err == nil || !strings.Contains(err.Error(), "--scanoss-url") || hits != 0 {
		t.Errorf("error %v, %d requests at the redirect's target", err, hits)
	}
}

// A user name, password or query in the URL is used for the request but
// never shown or recorded.
func TestSCANOSSURLSecrets(t *testing.T) {
	fake := &fakeSCANOSS{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	u := strings.Replace(srv.URL, "http://", "http://bot:s3cret@", 1) + "/scan/direct?token=TOKEN"
	r, err := ScanSCANOSS(project("clean"), SCANOSSOptions{URL: u, sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != srv.URL+"/scan/direct" {
		t.Errorf("recorded URL %q", r.URL)
	}
	if user, pw, ok := (&http.Request{Header: fake.reqs[0].header}).BasicAuth(); !ok || user != "bot" || pw != "s3cret" {
		t.Errorf("the request didn't carry the URL's user name and password")
	}
	_, in, err := r.Findings(project("clean"))
	if err != nil || strings.Contains(in.Path, "s3cret") || strings.Contains(in.Path, "TOKEN") {
		t.Errorf("input %+v, %v", in, err)
	}
	// No answer: the error names the host only.
	srv.Close()
	_, err = ScanSCANOSS(project("clean"), SCANOSSOptions{URL: u, Retries: -1, sleep: noSleep})
	if err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "bot") {
		t.Errorf("closed server: %v", err)
	}
}

// Text in an answer reaches terminals, reports and notice files: control
// characters and line breaks don't get through.
func TestSCANOSSAnswerText(t *testing.T) {
	evil := "\x1b]0;owned\x07\x1b[2J\x1b[1;1HPASS: all clear\x1b[8m"
	answer, _ := json.Marshal([]map[string]any{{
		"id": "file", "lines": "all", "matched": "100%", "component": "lib" + evil, "vendor": "v‮", "version": "1.0" + evil,
		"file": "a.go\n\n## Injected heading", "url": "https://example.com/" + evil, "purl": []string{"pkg:github/x/lib" + evil},
		"licenses":   []map[string]string{{"name": "MIT" + evil, "source": "license_file"}},
		"copyrights": []map[string]string{{"name": "Copyright (c) 2020 X\n\n## Injected heading\n"}},
	}})
	found, err := scanossFindings(map[string]json.RawMessage{"a.go": answer}, "evil", t.TempDir(), false)
	if err != nil || len(found) != 1 {
		t.Fatalf("%v, %d findings", err, len(found))
	}
	text := fmt.Sprintf("%+v", found[0])
	for _, bad := range []string{"\x1b", "\x07", "\n", "‮"} {
		if strings.Contains(text, bad) {
			t.Errorf("finding carries %q: %s", bad, text)
		}
	}
	if c := found[0].Source.Copyrights; len(c) != 1 || c[0] != "Copyright (c) 2020 X ## Injected heading" {
		t.Errorf("copyrights %q", c)
	}
}

// A file entry with more matches than SCANOSS gives is refused, and the
// project file behind "lines": "all" matches is read once.
func TestSCANOSSTooManyMatches(t *testing.T) {
	one := `{"id": "file", "lines": "all", "matched": "100%", "component": "c", "file": "f"}`
	many := "[" + strings.TrimSuffix(strings.Repeat(one+",", scanossMaxMatches+1), ",") + "]"
	if _, err := scanossFindings(map[string]json.RawMessage{"a.go": json.RawMessage(many)}, "big", t.TempDir(), false); err == nil ||
		!strings.Contains(err.Error(), "more than 100 matches") {
		t.Errorf("error %v", err)
	}
	ok := "[" + strings.TrimSuffix(strings.Repeat(one+",", scanossMaxMatches), ",") + "]"
	found, err := scanossFindings(map[string]json.RawMessage{"a.go": json.RawMessage(ok)}, "ok", t.TempDir(), false)
	if err != nil || len(found) != scanossMaxMatches {
		t.Errorf("%v, %d findings", err, len(found))
	}
}

// Remixware's own paths are used as they are: on systems where a file name
// can hold a backslash or start like a drive, its finding isn't dropped.
func TestSCANOSSOddFileNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file names can't hold a backslash on Windows")
	}
	dir := t.TempDir()
	names := []string{"ok.go", `..\gpl.go`, "c:gpl2.go", `a\..\..\gpl3.go`}
	for i, n := range names {
		body := fmt.Sprintf("package p\n\n// File %d.\nfunc F%d() int { return %d }\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gpl := `[{"id": "file", "lines": "all", "matched": "100%", "component": "gplthing", "file": "x.go",
  "licenses": [{"name": "GPL-3.0-only", "source": "license_file"}]}]`
	fake := &fakeSCANOSS{t: t, answer: func(string, string) string { return gpl }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(dir, SCANOSSOptions{URL: srv.URL, sleep: noSleep})
	if err != nil {
		t.Fatal(err)
	}
	found, _, err := r.Findings(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range found {
		files = append(files, f.File)
	}
	sort.Strings(files)
	want := append([]string{}, names...)
	sort.Strings(want)
	if fmt.Sprint(files) != fmt.Sprint(want) {
		t.Errorf("findings for %q, want %q", files, want)
	}
}

// Symbolic links are passed over and counted, as a scan does.
func TestSCANOSSSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	fake := &fakeSCANOSS{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	r, err := ScanSCANOSS(dir, SCANOSSOptions{URL: srv.URL, sleep: noSleep})
	if err != nil || r.Files != 1 || r.Symlinks != 1 {
		t.Errorf("%v: %d files sent, %d symlinks", err, r.Files, r.Symlinks)
	}
}

// A findings file from elsewhere is cleaned the same way: one-line
// printable text, licence texts with their line breaks, and no file whose
// name a terminal could take for commands.
func TestImportedFindingsText(t *testing.T) {
	dir := t.TempDir()
	esc := "\x1b[2J\x1b[1;1HPASS"
	file := map[string]any{
		"tool": "remixware 0.2.0",
		"findings": []map[string]any{
			{"file": "a.go", "lines": []int{1, 9}, "status": "conflict", "match": map[string]any{"kind": "verbatim" + esc},
				"source": map[string]any{"key": "k" + esc, "component": "lib" + esc, "license": "MIT" + esc, "via": "index",
					"copyrights": []string{"Copyright X\n\n## Injected"}}},
			{"file": "b" + esc + ".go", "lines": []int{1, 9}, "status": "conflict", "source": map[string]any{"component": "lib", "license": "MIT"}},
		},
		"texts": map[string]any{"k" + esc: map[string]string{"license_text": "Line one\nLine\x1b[8m two\n", "notice_text": "N\x07"}},
	}
	data, _ := json.Marshal(file)
	path := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	found, in, texts, err := ImportResults(path, dir)
	if err != nil || in.Kind != "remixware" || len(found) != 1 {
		t.Fatalf("%v, %+v, %d findings", err, in, len(found))
	}
	got := fmt.Sprintf("%+v %+v", found[0], texts)
	for _, bad := range []string{"\x1b", "\x07", "\n\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("imported text carries %q: %s", bad, got)
		}
	}
	tx := texts["k [2J [1;1HPASS"]
	if tx == nil || tx.LicenseText != "Line one\nLine[8m two\n" || tx.NoticeText != "N" {
		t.Errorf("texts %+v", texts)
	}
}
