package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var testdata = filepath.Join("..", "..", "testdata")

func fixture(parts ...string) string { return filepath.Join(append([]string{testdata}, parts...)...) }

type result struct {
	code           int
	stdout, stderr string
}

func cli(args ...string) result {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return result{code, out.String(), errb.String()}
}

var (
	indexOnce sync.Once
	indexPath string
	indexRes  result
)

// upstreamIndex builds the fixture index once with "remixware index".
func upstreamIndex(t *testing.T) string {
	t.Helper()
	indexOnce.Do(func() {
		dir, err := os.MkdirTemp("", "remixware-cli")
		if err != nil {
			indexRes = result{code: -1, stderr: err.Error()}
			return
		}
		indexPath = filepath.Join(dir, "up.rmx")
		indexRes = cli("index", fixture("upstream"), "--out", indexPath)
	})
	if indexRes.code != 0 {
		t.Fatalf("index: exit %d\n%s%s", indexRes.code, indexRes.stdout, indexRes.stderr)
	}
	return indexPath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if indexPath != "" {
		os.RemoveAll(filepath.Dir(indexPath))
	}
	os.Exit(code)
}

func expect(t *testing.T, r result, code int, wants ...string) {
	t.Helper()
	if r.code != code {
		t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
	all := r.stdout + r.stderr
	for _, w := range wants {
		if !strings.Contains(all, w) {
			t.Errorf("output lacks %q\nstdout:\n%s\nstderr:\n%s", w, r.stdout, r.stderr)
		}
	}
}

func TestUsageAndVersion(t *testing.T) {
	expect(t, cli(), exitUsage, "Usage:")
	expect(t, cli("frobnicate"), exitUsage, `unknown command "frobnicate"`)
	expect(t, cli("version"), exitOK, "remixware 0.")
	expect(t, cli("help"), exitOK, "remixware index <folder>")
	expect(t, cli("help", "check"), exitOK, "-fail-on", "-index", "-from")
	expect(t, cli("scan", "-h"), exitOK, "-json", "-html", "-spdx")
	expect(t, cli("licenses"), exitOK, "GPL-3.0-only", "copyleft", "not legal advice")
}

func TestIndexCommand(t *testing.T) {
	upstreamIndex(t)
	expect(t, indexRes, exitOK, "Indexed 10 files", "10 components", "Licences: MIT 2", "1 component has no licence: copies of its code")
	if st, err := os.Stat(indexPath); err != nil || st.Size() == 0 {
		t.Fatalf("index file not written: %v", err)
	}
	expect(t, cli("index"), exitUsage, "name at least one folder")
	expect(t, cli("index", fixture("nope"), "--out", filepath.Join(t.TempDir(), "x.rmx")), exitUsage, "nope")
	expect(t, cli("index", fixture("upstream"), "--bogus"), exitUsage, "flag provided but not defined")
}

func TestScanCommand(t *testing.T) {
	ix := upstreamIndex(t)
	dir := t.TempDir()
	js, html, spdx := filepath.Join(dir, "f.json"), filepath.Join(dir, "r.html"), filepath.Join(dir, "s.spdx.json")
	r := cli("scan", fixture("projects", "shopfront"), "--index", ix, "--json", js, "--html", html, "--spdx", spdx)
	expect(t, r, exitOK, "Project licence: MIT (from LICENSE)", "CONFLICT", "internal/files/order.go:7-46",
		"9 findings: 2 conflict, 1 no license, 5 attribution missing, 1 attributed.")
	for _, p := range []string{js, html, spdx} {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Errorf("%s not written", p)
		}
	}
	var got struct {
		Findings []struct {
			File, Status string
		} `json:"findings"`
	}
	data, _ := os.ReadFile(js)
	if err := json.Unmarshal(data, &got); err != nil || len(got.Findings) != 9 {
		t.Errorf("JSON findings: %d, %v", len(got.Findings), err)
	}
	// Flags can come before the project folder too.
	expect(t, cli("scan", "--index", ix, "--quiet", fixture("projects", "clean")), exitOK, "No copied code found.")
	expect(t, cli("scan", fixture("projects", "shopfront")), exitUsage, "nothing to compare with")
	expect(t, cli("scan", "a", "b", "--index", ix), exitUsage, "one project folder")
	expect(t, cli("scan", fixture("nope"), "--index", ix), exitUsage, "nope")
	expect(t, cli("scan", fixture("projects", "clean"), "--index", fixture("scanoss", "shopfront.json")), exitUsage, "not a Remixware index")
}

func TestScanFromSCANOSS(t *testing.T) {
	r := cli("scan", fixture("projects", "shopfront"), "--from", fixture("scanoss", "shopfront.json"))
	expect(t, r, exitOK, "SCANOSS, 4 matches", "scripts/slug.py:9-36", "4 findings")
	r = cli("scan", fixture("projects", "shopfront"), "--from", fixture("scanoss", "shopfront.json"), "--index", upstreamIndex(t))
	expect(t, r, exitOK, "10 findings")
}

func TestCheckCommand(t *testing.T) {
	ix := upstreamIndex(t)
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix), exitFail,
		"FAIL: 8 findings break the policy (fail on: conflict, no-license, attribution-missing).", "remixware notice")
	expect(t, cli("check", fixture("projects", "clean"), "--index", ix), exitOK, "PASS")
	expect(t, cli("check", fixture("projects", "firmware"), "--index", ix, "--fail-on", "conflict"), exitFail, "FAIL: 2 findings")
	// The MPL-2.0 copy needs a review and its notice: until the notice is
	// there, it counts as attribution missing (see TestNoticeLoop).
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--fail-on", "review"), exitOK, "PASS")
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--fail-on", "nonsense"), exitUsage, `unknown status "nonsense"`)
	// The project's licence can be given on the command line.
	expect(t, cli("check", fixture("projects", "firmware"), "--index", ix, "--license", "GPL-3.0-or-later", "--fail-on", "conflict"),
		exitOK, "Project licence: GPL-3.0-or-later (from --license)")
}

// A check that couldn't look at anything must not pass: a wrong folder, an
// --exclude that covers everything, an index that holds the project itself.
func TestCheckThatScansNothingFails(t *testing.T) {
	ix := upstreamIndex(t)
	empty := t.TempDir()
	os.WriteFile(filepath.Join(empty, "README.md"), []byte("# docs only\n"), 0o644)
	expect(t, cli("check", empty, "--index", ix), exitUsage, "no source files to check")
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--exclude", "**"), exitUsage, "no source files to check")
	// A bad --fail-on is reported before any scanning starts.
	r := cli("check", fixture("projects", "shopfront"), "--index", ix, "--fail-on", "conflcit")
	expect(t, r, exitUsage, `unknown status "conflcit"`)
	if strings.Contains(r.stderr, "scanning") {
		t.Errorf("scanned before reporting the bad --fail-on:\n%s", r.stderr)
	}
	// An index of the whole testdata folder holds shopfront itself: its
	// files are still checked, and still copied from upstream.
	all := filepath.Join(t.TempDir(), "all.rmx")
	expect(t, cli("index", testdata, "--out", all, "--quiet"), exitOK)
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", all), exitFail, "FAIL: 8 findings break the policy")
}

func TestPolicyFile(t *testing.T) {
	ix := upstreamIndex(t)
	dir := t.TempDir()
	pol := filepath.Join(dir, "policy.json")
	os.WriteFile(pol, []byte(`{"fail_on": ["conflict"], "allow": [{"component": "example.org/gplsort", "reason": "relicensed to us by the author"}], "ignore": ["scripts/**"]}`), 0o644)
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--policy", pol), exitOK, "PASS", "fail on: conflict")
	os.WriteFile(pol, []byte(`{"fail_on": ["conflict"], "alow": []}`), 0o644)
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--policy", pol), exitUsage, `unknown field "alow"`)
	// An empty fail_on would pass everything: it's an error, not a default.
	os.WriteFile(pol, []byte(`{"fail_on": []}`), 0o644)
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--policy", pol), exitUsage, "fail_on is empty")

	// Without --policy, the project's own .remixware.json is read.
	proj := filepath.Join(dir, "shopfront")
	copyDir(t, fixture("projects", "shopfront"), proj)
	os.WriteFile(filepath.Join(proj, ".remixware.json"), []byte(`{"fail_on": ["no-license"], "ignore": ["internal/format/**"]}`), 0o644)
	expect(t, cli("check", proj, "--index", ix), exitOK, "PASS: no findings break the policy (fail on: no-license).")
}

// The loop the README describes: check fails on missing attribution,
// notice writes it, check passes.
func TestNoticeLoop(t *testing.T) {
	ix := upstreamIndex(t)
	proj := filepath.Join(t.TempDir(), "shopfront")
	copyDir(t, fixture("projects", "shopfront"), proj)
	notes := filepath.Join(proj, "THIRD_PARTY_NOTICES.md")
	before, _ := os.ReadFile(notes)

	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitFail, "FAIL: 5 findings", "internal/cache/lru.go")
	expect(t, cli("notice", proj, "--index", ix, "--out", notes), exitOK,
		"added 5 entries", "not written: example.org/gplsort", "finish by hand: leftpadx 1.0.4: copyright line not known",
		"finish by hand: example.net/mplcache: copyright line not known")
	after, _ := os.ReadFile(notes)
	if !bytes.HasPrefix(after, before) || len(after) <= len(before) {
		t.Fatalf("notices file not appended to")
	}
	// The entries notice couldn't finish don't count until someone does.
	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitFail, "FAIL: 2 findings break", "web/pad.js", "internal/cache/lru.go")
	const todo = "Copyright: not known. Copy the copyright line from the source before you ship this."
	if bytes.Count(after, []byte(todo)) != 2 {
		t.Fatalf("not two lines to finish in the notices:\n%s", after)
	}
	finished := finishEntry(after, "leftpadx@1.0.4", "Copyright (c) 2016 The leftpadx authors")
	finished = finishEntry(finished, "example.net/mplcache", "No copyright line in the source; its files carry only the MPL-2.0 notice.")
	if bytes.Contains(finished, []byte(todo)) {
		t.Fatalf("entries left unfinished:\n%s", finished)
	}
	os.WriteFile(notes, finished, 0o644)
	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitOK, "PASS")
	// With its notice there, the MPL-2.0 copy is left for review.
	expect(t, cli("check", proj, "--index", ix, "--fail-on", "review"), exitFail, "FAIL: 1 finding breaks", "internal/cache/lru.go")
	expect(t, cli("notice", proj, "--index", ix, "--out", notes), exitOK, "nothing to add")
	again, _ := os.ReadFile(notes)
	if !bytes.Equal(again, finished) {
		t.Errorf("second notice run changed the file")
	}
	// Without --out the entries go to standard output.
	r := cli("notice", fixture("projects", "shopfront"), "--index", ix, "--quiet")
	expect(t, r, exitOK, "# Third-party notices", "Copyright (c) 2021 Mira Okafor")
}

// finishEntry puts a line in place of the copyright line the notice entry
// for key asks for, as a person finishing the notices would.
func finishEntry(notices []byte, key, line string) []byte {
	parts := strings.Split(string(notices), "\n## ")
	for i, p := range parts {
		if strings.Contains(p, "- Source: "+key+"\n") {
			parts[i] = strings.Replace(p, "Copyright: not known. Copy the copyright line from the source before you ship this.", line, 1)
		}
	}
	return []byte(strings.Join(parts, "\n## "))
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
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
}

// fakeService answers like the SCANOSS service: a fastcsv match for the
// file whose MD5 is match, no match for the rest. It remembers the API key
// it was sent.
func fakeService(t *testing.T, match string, status int) (*httptest.Server, *string) {
	t.Helper()
	key := new(string)
	line := regexp.MustCompile(`(?m)^file=([0-9a-f]{32}),\d+,(.*)$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*key = r.Header.Get("x-api-key")
		if status != 0 {
			http.Error(w, "no", status)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(f)
		out := map[string]json.RawMessage{}
		for _, m := range line.FindAllStringSubmatch(string(data), -1) {
			out[m[2]] = json.RawMessage(`[{"id": "none"}]`)
			if m[1] == match {
				out[m[2]] = json.RawMessage(`[{"id": "snippet", "lines": "2-45", "oss_lines": "2-45", "matched": "100%",
					"purl": ["pkg:github/mokafor/fastcsv"], "vendor": "mokafor", "component": "fastcsv", "version": "1.4.0",
					"file": "fastcsv.go", "licenses": [{"name": "MIT", "source": "component_declared"}]}]`)
			}
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv, key
}

func TestSCANOSSFlags(t *testing.T) {
	shop := fixture("projects", "shopfront")
	data, err := os.ReadFile(filepath.Join(shop, "internal", "csvio", "split.go"))
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(data)
	srv, key := fakeService(t, hex.EncodeToString(sum[:]), 0)
	t.Setenv("SCANOSS_API_KEY", "")
	t.Setenv("SCANOSS_SCAN_URL", "")

	expect(t, cli("scan", shop, "--scanoss", "--scanoss-url", srv.URL), exitOK,
		"the SCANOSS service at 127.0.0.1", "files sent, 1 match", "internal/csvio/split.go:2-45", "mokafor/fastcsv 1.4.0", "snippet (SCANOSS)")
	expect(t, cli("check", shop, "--scanoss", "--scanoss-url", srv.URL), exitFail, "FAIL: 1 finding breaks", "same --index, --from or --scanoss flags")

	// The answers saved with --scanoss-out read back with --from.
	saved := filepath.Join(t.TempDir(), "scanoss.json")
	expect(t, cli("scan", shop, "--scanoss", "--scanoss-url", srv.URL, "--scanoss-out", saved, "--quiet"), exitOK, "internal/csvio/split.go:2-45")
	expect(t, cli("scan", shop, "--from", saved), exitOK, "internal/csvio/split.go:2-45", "SCANOSS, 1 match")

	// The key comes from the flag or from SCANOSS_API_KEY.
	expect(t, cli("scan", shop, "--scanoss", "--scanoss-url", srv.URL, "--scanoss-key", "flag-key", "--quiet"), exitOK)
	if *key != "flag-key" {
		t.Errorf("key sent %q", *key)
	}
	t.Setenv("SCANOSS_API_KEY", "env-key")
	t.Setenv("SCANOSS_SCAN_URL", srv.URL)
	expect(t, cli("scan", shop, "--scanoss", "--quiet"), exitOK, "internal/csvio/split.go")
	if *key != "env-key" {
		t.Errorf("key sent %q", *key)
	}
	t.Setenv("SCANOSS_API_KEY", "")

	// Mistakes end with exit 2: options without --scanoss, a refused key,
	// nothing to send.
	expect(t, cli("scan", shop, "--index", upstreamIndex(t), "--scanoss-key", "k"), exitUsage, "go with --scanoss")
	bad, _ := fakeService(t, "", http.StatusUnauthorized)
	expect(t, cli("check", shop, "--scanoss", "--scanoss-url", bad.URL), exitUsage, "Check the API key")
	// A used-up limit: the service asks for a wait no retry could cover, and
	// the check stops at once with the wait and what to do.
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":"Rate limit exceeded","retry_after":475777.7}`)
	}))
	t.Cleanup(limited.Close)
	expect(t, cli("check", shop, "--scanoss", "--scanoss-url", limited.URL), exitUsage, "asks Remixware to wait 5.5 days")
	expect(t, cli("check", shop, "--scanoss", "--scanoss-url", limited.URL), exitUsage, "use a SCANOSS API key (--scanoss-key)")
	expect(t, cli("check", t.TempDir(), "--scanoss", "--scanoss-url", srv.URL), exitUsage, "no source files to send to SCANOSS")
	expect(t, cli("check", shop), exitUsage, "--scanoss (the SCANOSS service)")
}

// Everything the command line prints goes through the terminal filter: a
// file name holding an escape sequence shows as U+FFFD.
func TestTerminalFilter(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(fixture("projects", "shopfront", "internal", "csvio", "split.go"))
	if err != nil {
		t.Fatal(err)
	}
	evil := "split\x1b[2J\x1b[1;1HPASS.go"
	if err := os.WriteFile(filepath.Join(dir, evil), data, 0o644); err != nil {
		t.Skip("no such file names here:", err)
	}
	r := cli("check", dir, "--index", upstreamIndex(t), "--license", "MIT")
	expect(t, r, exitFail, "split�[2J�[1;1HPASS.go")
	if strings.ContainsRune(r.stdout+r.stderr, '\x1b') {
		t.Errorf("an escape character got through:\n%q", r.stdout)
	}
}

// A --scanoss run without an index says what it passed over, and
// --scanoss-out makes its folder.
func TestSCANOSSOnlyRun(t *testing.T) {
	srv, _ := fakeService(t, "", 0)
	t.Setenv("SCANOSS_API_KEY", "")
	t.Setenv("SCANOSS_SCAN_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	out := filepath.Join(t.TempDir(), "deep", "er", "scanoss.json")
	expect(t, cli("scan", dir, "--scanoss", "--scanoss-url", srv.URL, "--scanoss-out", out), exitOK, "1 symbolic link, not followed")
	if _, err := os.Stat(out); err != nil {
		t.Errorf("--scanoss-out: %v", err)
	}
}

// A results file read only in part: scan says so, check can't pass.
func TestDroppedEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.go"), []byte("package ok\n\nfunc OK() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	match := `[{"id": "file", "lines": "all", "matched": "100%", "component": "x", "licenses": [{"name": "MIT", "source": "license_file"}]}]`
	results := filepath.Join(t.TempDir(), "scanoss.json")
	if err := os.WriteFile(results, []byte(`{"ok.go": `+match+`, "c:gpl.go": `+match+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, cli("scan", dir, "--from", results, "--license", "MIT"), exitOK, "1 entry with matches left out")
	expect(t, cli("check", dir, "--from", results, "--license", "MIT", "--fail-on", "conflict"), exitUsage,
		"a check can't pass on a results file it couldn't read whole")
}
