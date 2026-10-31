package remixware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests from the independent review of the dependency gate (2026-10-31).
// Each one is a change that passed (exit 0) or a check that lied before
// the fix.

func mustFail(t *testing.T, res *DepsResult, what string) {
	t.Helper()
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("%s passed: %+v", what, res.Changes)
	}
}

// Go's encoding/json matches struct fields without regard to case (and
// folds ſ to s, K to k); npm reads exact keys. A key npm ignores must not
// change what the gate reads.
func TestDepsReviewNPMKeysAreExact(t *testing.T) {
	base := map[string]string{"package-lock.json": npmLock("a@1.0.0")}
	heads := map[string]string{
		"LINK hides a package": `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*", "evil": "*"}},
			"node_modules/a": {"version": "1.0.0"},
			"node_modules/evil": {"version": "6.6.6", "LINK": true}}}`,
		"HasInstallScript overrides hasInstallScript": `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*"}},
			"node_modules/a": {"version": "1.0.1", "hasInstallScript": true, "haſInstallScript": false}}}`,
		"LockfileVersion 1 hides packages": `{"lockfileVersion": 3, "LockfileVersion": 1, "dependencies": {"a": {"version": "1.0.0"}},
			"packages": {"": {"dependencies": {"a": "*", "evil": "*"}},
			"node_modules/a": {"version": "1.0.0"}, "node_modules/evil": {"version": "6.6.6"}}}`,
		"Version shadows version": `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*"}},
			"node_modules/a": {"version": "1.0.0"},
			"node_modules/b": {"version": "6.6.6", "Name": "a", "Version": "1.0.0"}}}`,
	}
	for what, h := range heads {
		res, err := CompareDeps(tree(t, map[string]string{"package-lock.json": h}), tree(t, base), DepsOptions{})
		if err != nil {
			continue // refusing the file is fine too
		}
		mustFail(t, res, what)
	}
}

// TOML allows spaces inside table headers; Poetry reads [[ package ]].
func TestDepsReviewPoetryHeaderSpaces(t *testing.T) {
	base := map[string]string{"poetry.lock": "[[package]]\nname = \"a\"\nversion = \"1.0\"\nfiles = []\n\n[metadata]\n"}
	head := map[string]string{"poetry.lock": base["poetry.lock"] + "\n[[ package ]]\nname = \"evil\"\nversion = \"6.6.6\"\nfiles = []\n"}
	res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if err == nil {
		mustFail(t, res, "[[ package ]]")
	}
}

// A multi-line string can hold lines that look like keys.
func TestDepsReviewPoetryMultilineString(t *testing.T) {
	base := map[string]string{"poetry.lock": "[[package]]\nname = \"requests\"\nversion = \"2.31.0\"\nfiles = []\n\n[metadata]\n"}
	head := map[string]string{"poetry.lock": "[[package]]\nname = \"evil\"\nversion = \"6.6.6\"\ndescription = \"\"\"\nname = \"requests\"\nversion = \"2.31.0\"\"\"\nfiles = []\n\n[metadata]\n"}
	res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if err == nil {
		mustFail(t, res, "names inside a multi-line string")
	}
}

// source written as an inline table or dotted keys is still a source.
func TestDepsReviewPoetryInlineSource(t *testing.T) {
	base := map[string]string{"poetry.lock": "[[package]]\nname = \"a\"\nversion = \"1.0\"\nfiles = [{file = \"a-1.0-py3-none-any.whl\"}]\n\n[metadata]\n"}
	for _, src := range []string{
		"source = {type = \"git\", url = \"https://evil.example/a.git\", reference = \"x\"}\n",
		"source.type = \"git\"\nsource.url = \"https://evil.example/a.git\"\n",
	} {
		head := map[string]string{"poetry.lock": "[[package]]\nname = \"a\"\nversion = \"1.1\"\nfiles = []\n" + src + "\n[metadata]\n"}
		res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
		if err == nil {
			mustFail(t, res, "update moving to "+strings.TrimSpace(src))
		}
	}
}

// pip joins a line ending in \ with the next one even after a comment
// (its COMMENT_RE.match only looks at the line's start), so the next
// requirement is part of the comment: checked against pip 26.2.1.
func TestDepsReviewRequirementsCommentBackslashLikePip(t *testing.T) {
	ps, err := parseRequirements(nil, "requirements.txt", []byte("requests==2.31.0  # pinned for now \\\nevil==6.6.6\n"), 0, new([]string))
	if err != nil || len(ps) != 1 || ps[0].name != "requests" {
		t.Errorf("%+v %v", ps, err)
	}
}

// pip splits lines with Python's str.splitlines: \r, \v, \f, \x1c-\x1e,
// NEL, U+2028 and U+2029 end lines too.
func TestDepsReviewRequirementsPythonLineBreaks(t *testing.T) {
	base := map[string]string{"requirements.txt": "requests==2.31.0\n"}
	for _, sep := range []string{"\r", "\v", "\f", "\x1c", "\u0085", " ", " "} {
		head := map[string]string{"requirements.txt": "requests==2.31.0 # x" + sep + "evil==6.6.6\n"}
		res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
		if err == nil {
			mustFail(t, res, "line break "+strings.ToValidUTF8(sep, "?"))
		}
	}
}

// A constraints file can point pip at another index too.
func TestDepsReviewConstraintsIndex(t *testing.T) {
	base := map[string]string{"requirements.txt": "requests==2.31.0\n"}
	head := map[string]string{"requirements.txt": "-c constraints.txt\nrequests==2.31.0\n",
		"constraints.txt": "--extra-index-url https://evil.example/simple\n"}
	res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if err == nil {
		mustFail(t, res, "index added in a constraints file")
	}
}

// Common requirements file names beyond requirements*.txt.
func TestDepsReviewRequirementsNames(t *testing.T) {
	for _, name := range []string{"test-requirements.txt", "dev-requirements.txt", "requirements-dev.in.txt", "requirements/base.txt"} {
		base := map[string]string{name: "requests==2.31.0\n"}
		head := map[string]string{name: "requests==2.31.0\nevil==6.6.6\n"}
		mustFail(t, compare(t, head, base, nil, nil), name)
	}
}

// go.work overrides go.mod for go build.
func TestDepsReviewGoWork(t *testing.T) {
	base := map[string]string{"go.mod": goModA, "go.sum": goSumA}
	head := map[string]string{"go.mod": goModA, "go.sum": goSumA,
		"go.work": "go 1.22\n\nuse .\n\nreplace golang.org/x/text => github.com/evil/text v0.0.1\n"}
	mustFail(t, compare(t, head, base, nil, nil), "a new go.work")
}

// npm follows a symbolic link named package-lock.json.
func TestDepsReviewSymlinkedLockfile(t *testing.T) {
	base := tree(t, map[string]string{"package-lock.json": npmLock("a@1.0.0")})
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "locks"), 0o755)
	os.WriteFile(filepath.Join(dir, "locks", "real.json"), []byte(npmLock("a@1.0.0", "evil@6.6.6")), 0o644)
	if err := os.Symlink("locks/real.json", filepath.Join(dir, "package-lock.json")); err != nil {
		t.Skip(err)
	}
	head, err := DepsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CompareDeps(head, base, DepsOptions{})
	if err == nil {
		mustFail(t, res, "a lockfile replaced by a link")
	}
	// A new lockfile that's a link, where the base had none.
	empty := tree(t, map[string]string{"README": "x"})
	if res, err := CompareDeps(head, empty, DepsOptions{}); err == nil {
		mustFail(t, res, "a new lockfile that's a link")
	}
	// A link out of the project is refused.
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "x.json"), []byte(npmLock("a@1.0.0")), 0o644)
	dir2 := t.TempDir()
	os.Symlink(filepath.Join(out, "x.json"), filepath.Join(dir2, "package-lock.json"))
	head2, _ := DepsDir(dir2)
	if res, err := CompareDeps(head2, base, DepsOptions{}); err == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Error("a lockfile linked from outside the project passed")
	}
}

// Deleting the lockfile while its manifest stays lets the installer pick
// anything the manifest asks for.
func TestDepsReviewLockfileRemovedManifestKept(t *testing.T) {
	base := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0"}}`, "package-lock.json": npmLock("a@1.0.0")}
	head := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0", "evil": "*"}}`}
	mustFail(t, compare(t, head, base, nil, nil), "package-lock.json deleted, package.json kept")
	pbase := map[string]string{"pyproject.toml": "[tool.poetry.dependencies]\na = \"*\"\n",
		"poetry.lock": "[[package]]\nname = \"a\"\nversion = \"1.0\"\nfiles = []\n\n[metadata]\n"}
	phead := map[string]string{"pyproject.toml": "[tool.poetry.dependencies]\na = \"*\"\nevil = \"*\"\n"}
	mustFail(t, compare(t, phead, pbase, nil, nil), "poetry.lock deleted, pyproject.toml kept")
	// Deleting both is a removal.
	if f := DepFailures(compare(t, map[string]string{"x.txt": "x"}, base, nil, nil), DefaultDepFailOn); len(f) != 0 {
		t.Errorf("removing package.json and its lockfile failed: %+v", f)
	}
}

// Without --base-policy, a change that loosens the policy's settings must
// say so, as it does for allow rules.
func TestDepsReviewPolicySettingChangesShown(t *testing.T) {
	base := map[string]string{"package-lock.json": npmLock("a@1.0.0")}
	head := map[string]string{"package-lock.json": npmLock("a@1.0.0", "evil@6.6.6")}
	basePol := &Policy{}
	for what, pol := range map[string]*Policy{
		"fail_on":   {Deps: &DepsPolicy{FailOn: []string{DepInstallScript}}},
		"ignore":    {Ignore: []string{"package-lock.json"}},
		"lockfiles": {Deps: &DepsPolicy{Lockfiles: []string{"go.mod"}}},
	} {
		res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{Policy: pol, BasePolicy: basePol})
		if err != nil {
			if what == "lockfiles" {
				continue
			}
			t.Fatal(err)
		}
		if len(res.PolicyChanges) == 0 {
			t.Errorf("%s changed in the policy without notice", what)
		}
	}
	res, _ := CompareDeps(tree(t, head), tree(t, base), DepsOptions{Policy: basePol, BasePolicy: basePol})
	if len(res.PolicyChanges) != 0 {
		t.Errorf("unchanged policy reported: %v", res.PolicyChanges)
	}
}

// The registry cache must not answer for one registry with another's facts.
func TestDepsReviewRegistryCacheKeyedByRegistry(t *testing.T) {
	serve := func(scripts bool) *httptest.Server {
		doc := packument(npmVer{"1.0.0", "alice", "2020-01-01T00:00:00Z", []string{"alice"}, scripts})
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(doc)) }))
		t.Cleanup(s.Close)
		return s
	}
	clean, dirty := serve(false), serve(true)
	cache := filepath.Join(t.TempDir(), "c.json")
	r1 := &Registry{NPMURL: clean.URL, CachePath: cache}
	if _, _, err := r1.Facts(EcoNPM, "x", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := r1.Save(); err != nil {
		t.Fatal(err)
	}
	r2 := &Registry{NPMURL: dirty.URL, CachePath: cache}
	f, cached, err := r2.Facts(EcoNPM, "x", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if cached || !f.InstallScripts {
		t.Errorf("answer for one registry came from another's cache: %+v cached=%v", f, cached)
	}
}

// A VCS or source-archive requirement builds from source when pip installs
// it, as poetry.lock's git packages do; moving one to another host must
// not pass as a plain update.
func TestDepsReviewRequirementsVCSBuildsFromSource(t *testing.T) {
	base := map[string]string{"requirements.txt": "foo @ git+https://github.com/org/foo@v1.0\n"}
	for _, h := range []string{
		"foo @ git+https://evil.example/foo@v1.0\n",
		"git+https://evil.example/foo@v1.0#egg=foo\n",
		"foo @ https://evil.example/foo-1.0.tar.gz\n",
	} {
		mustFail(t, compare(t, map[string]string{"requirements.txt": h}, base, nil, nil), h)
	}
	// A wheel URL doesn't run build code (still not from the registry, but
	// that's the base's state too).
	wb := map[string]string{"requirements.txt": "foo @ https://files.example/foo-1.0-py3-none-any.whl\n"}
	wh := map[string]string{"requirements.txt": "foo @ https://files.example/foo-1.1-py3-none-any.whl\n"}
	if c := change(compare(t, wh, wb, nil, nil), "pypi/foo"); c == nil || c.Status != DepUpdated {
		t.Errorf("wheel URL update: %+v", c)
	}
	_ = time.Now
}

// A symbolic-link lockfile on main is read through the link too, so an
// unchanged linked lockfile is no change; a link out of the project fails.
func TestDepsReviewGitSymlinkedLockfile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL="+noGitConfig(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, "app", "locks"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "locks", "npm.json"), []byte(npmLock("a@1.0.0")), 0o644)
	if err := os.Symlink("locks/npm.json", filepath.Join(dir, "app", "package-lock.json")); err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(dir, "outside.json"), []byte(npmLock("a@1.0.0")), 0o644)
	os.Symlink("../outside.json", filepath.Join(dir, "app", "npm-shrinkwrap.json"))
	run("add", ".")
	run("commit", "-q", "-m", "base")
	base, err := DepsGit(filepath.Join(dir, "app"), "main")
	if err != nil {
		t.Fatal(err)
	}
	b, ok, err := base.ReadFile("package-lock.json")
	if err != nil || !ok || !strings.Contains(string(b), "node_modules/a") {
		t.Errorf("linked lockfile at main: %q %v %v", b, ok, err)
	}
	if _, _, err := base.ReadFile("npm-shrinkwrap.json"); err == nil {
		t.Error("a link out of the project was read")
	}
}
