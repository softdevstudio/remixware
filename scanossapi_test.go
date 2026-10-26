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
	cases := []struct{ url, key, want string }{
		{"", "", "https://api.osskb.org/scan/direct"},
		{"", "k", "https://api.scanoss.com/scan/direct"},
		{"https://scan.example", "", "https://scan.example/scan/direct"},
		{"https://scan.example/", "k", "https://scan.example/scan/direct"},
		{"http://localhost:8080/custom/scan", "", "http://localhost:8080/custom/scan"},
	}
	for _, c := range cases {
		if got, err := scanossURL(c.url, c.key); err != nil || got != c.want {
			t.Errorf("scanossURL(%q, %q) = %q, %v; want %q", c.url, c.key, got, err, c.want)
		}
	}
	for _, bad := range []string{"ftp://scan.example", "scan.example/scan", "https://"} {
		if _, err := scanossURL(bad, ""); err == nil {
			t.Errorf("scanossURL(%q) gave no error", bad)
		}
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
	answer := map[string]json.RawMessage{}
	for _, m := range wfpFileLine.FindAllStringSubmatch(req.wfp, -1) {
		req.names = append(req.names, m[3])
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
	n := len(f.reqs)
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
			return http.StatusTooManyRequests, http.Header{"Retry-After": {"3600"}}, ""
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
