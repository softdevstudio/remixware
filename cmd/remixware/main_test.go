package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
		"9 findings: 2 conflict, 1 no license, 1 review, 4 attribution missing, 1 attributed.")
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
		"FAIL: 7 findings break the policy (fail on: conflict, no-license, attribution-missing).", "remixware notice")
	expect(t, cli("check", fixture("projects", "clean"), "--index", ix), exitOK, "PASS")
	expect(t, cli("check", fixture("projects", "firmware"), "--index", ix, "--fail-on", "conflict"), exitFail, "FAIL: 2 findings")
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", ix, "--fail-on", "review"), exitFail, "FAIL: 1 finding breaks")
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
	expect(t, cli("check", fixture("projects", "shopfront"), "--index", all), exitFail, "FAIL: 7 findings break the policy")
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

	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitFail, "FAIL: 4 findings")
	expect(t, cli("notice", proj, "--index", ix, "--out", notes), exitOK,
		"added 5 entries", "not written: example.org/gplsort", "finish by hand: leftpadx 1.0.4: copyright line not known")
	after, _ := os.ReadFile(notes)
	if !bytes.HasPrefix(after, before) || len(after) <= len(before) {
		t.Fatalf("notices file not appended to")
	}
	// The entry notice couldn't finish doesn't count until someone does.
	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitFail, "FAIL: 1 finding breaks", "web/pad.js")
	const todo = "Copyright: not known. Copy the copyright line from the source before you ship this."
	if !bytes.Contains(after, []byte(todo)) {
		t.Fatalf("no line to finish in the notices:\n%s", after)
	}
	finished := bytes.Replace(after, []byte(todo), []byte("Copyright (c) 2016 The leftpadx authors"), 1)
	os.WriteFile(notes, finished, 0o644)
	expect(t, cli("check", proj, "--index", ix, "--fail-on", "attribution-missing"), exitOK, "PASS")
	expect(t, cli("notice", proj, "--index", ix, "--out", notes), exitOK, "nothing to add")
	again, _ := os.ReadFile(notes)
	if !bytes.Equal(again, finished) {
		t.Errorf("second notice run changed the file")
	}
	// Without --out the entries go to standard output.
	r := cli("notice", fixture("projects", "shopfront"), "--index", ix, "--quiet")
	expect(t, r, exitOK, "# Third-party notices", "Copyright (c) 2021 Mira Okafor")
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
