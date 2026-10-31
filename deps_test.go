package remixware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tree writes files into a new folder and returns it as a DepsTree.
func tree(t *testing.T, files map[string]string) DepsTree {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := DepsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func pkgsByName(ps []lockPackage) map[string]lockPackage {
	m := map[string]lockPackage{}
	for _, p := range ps {
		m[p.name+"@"+p.version] = p
	}
	return m
}

const goModA = `module example.com/app

go 1.22

require (
	github.com/acme/direct v1.2.0
	github.com/acme/indirect v0.3.1 // indirect
)

require golang.org/x/text v0.14.0

replace github.com/acme/forked => github.com/someone/forked v1.0.1
replace github.com/acme/local => ../local
`

const goSumA = `github.com/acme/direct v1.2.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
github.com/acme/direct v1.2.0/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
github.com/acme/indirect v0.3.1 h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=
github.com/acme/deep v0.0.9 h1:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=
github.com/acme/onlymod v1.0.0/go.mod h1:EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE=
github.com/acme/forked v1.0.0 h1:FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=
github.com/acme/local v1.0.0 h1:GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG=
`

func TestParseGoMod(t *testing.T) {
	ps, err := parseGoMod([]byte(goModA), []byte(goSumA))
	if err != nil {
		t.Fatal(err)
	}
	m := pkgsByName(ps)
	want := map[string]bool{ // name@version -> direct
		"github.com/acme/direct@v1.2.0":    true,
		"github.com/acme/indirect@v0.3.1":  false,
		"golang.org/x/text@v0.14.0":        true,
		"github.com/acme/deep@v0.0.9":      false,
		"github.com/someone/forked@v1.0.1": false,
	}
	if len(m) != len(want) {
		t.Errorf("got %d modules, want %d: %v", len(m), len(want), m)
	}
	for k, d := range want {
		p, ok := m[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if p.direct != d || p.eco != EcoGo || !p.pinned || !p.registry || p.scripts != scriptsNo {
			t.Errorf("%s: %+v", k, p)
		}
	}
	for _, bad := range []struct{ mod, sum, why string }{
		{"module x\nrequire github.com/a/b\n", "", "require without version"},
		{"module x\nrequire (\n github.com/a/b v1.0.0\n", "", "unclosed block"},
		{"require github.com/a/b v1.0.0\n", "", "no module line"},
		{"module x\n", "github.com/a/b v1.0.0\n", "go.sum line with two fields"},
		{"module x\n", "github.com/a/b v1.0.0 sha256:zzz\n", "go.sum hash"},
		{"module x\nreplace a => b\n", "", "replace target without version"},
	} {
		if _, err := parseGoMod([]byte(bad.mod), []byte(bad.sum)); err == nil {
			t.Errorf("%s: no error", bad.why)
		}
	}
}

const npmLockV3 = `{
  "name": "app", "lockfileVersion": 3, "requires": true,
  "packages": {
    "": {"name": "app", "dependencies": {"left-pad": "^1.3.0", "@acme/ui": "2.0.0"}, "devDependencies": {"esbuild": "^0.19.0"}},
    "node_modules/left-pad": {"version": "1.3.0", "resolved": "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"},
    "node_modules/@acme/ui": {"version": "2.0.0", "resolved": "https://registry.npmjs.org/@acme/ui/-/ui-2.0.0.tgz"},
    "node_modules/@acme/ui/node_modules/left-pad": {"version": "1.1.0"},
    "node_modules/esbuild": {"version": "0.19.2", "dev": true, "hasInstallScript": true},
    "node_modules/private": {"version": "1.0.0", "resolved": "https://npm.example.com/private-1.0.0.tgz"},
    "node_modules/fromgit": {"version": "git+ssh://git@example.com/x.git#abc", "resolved": "git+ssh://git@example.com/x.git#abc"},
    "packages/web": {"name": "web", "version": "0.0.1", "dependencies": {"private": "1.0.0"}},
    "node_modules/web": {"resolved": "packages/web", "link": true}
  }
}`

func TestParseNPMLock(t *testing.T) {
	ps, err := parseNPMLock([]byte(npmLockV3), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := pkgsByName(ps)
	if len(m) != 6 {
		t.Errorf("got %d packages, want 6: %v", len(m), m)
	}
	check := func(k string, direct, registry bool, scripts int8) {
		t.Helper()
		p, ok := m[k]
		if !ok {
			t.Errorf("missing %s", k)
			return
		}
		if p.direct != direct || p.registry != registry || p.scripts != scripts {
			t.Errorf("%s: direct=%v registry=%v scripts=%d", k, p.direct, p.registry, p.scripts)
		}
	}
	check("left-pad@1.3.0", true, true, scriptsNo)
	check("left-pad@1.1.0", false, true, scriptsNo) // nested under @acme/ui
	check("@acme/ui@2.0.0", true, true, scriptsNo)
	check("esbuild@0.19.2", true, true, scriptsYes)
	check("private@1.0.0", true, false, scriptsNo) // direct through the workspace, from another registry
	check("fromgit@git+ssh://git@example.com/x.git#abc", false, false, scriptsNo)

	v1 := `{"lockfileVersion": 1, "dependencies": {
	  "a": {"version": "1.0.0", "dependencies": {"b": {"version": "2.0.0"}}},
	  "c": {"version": "3.0.0"}}}`
	ps, err = parseNPMLock([]byte(v1), []byte(`{"dependencies": {"a": "^1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	m = pkgsByName(ps)
	if !m["a@1.0.0"].direct || m["b@2.0.0"].direct || m["c@3.0.0"].direct || m["a@1.0.0"].scripts != scriptsUnknown {
		t.Errorf("v1: %+v", m)
	}
	for _, bad := range []string{`{`, `{"packages": {}}`, `{"lockfileVersion": 4, "packages": {}}`,
		`{"lockfileVersion": 3}`, `{"lockfileVersion": 3, "packages": {"node_modules/x": {}}}`,
		`{"lockfileVersion": 3, "packages": {"node_modules/BAD NAME": {"version": "1.0.0"}}}`} {
		if _, err := parseNPMLock([]byte(bad), nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestParseRequirements(t *testing.T) {
	tr := tree(t, map[string]string{
		"requirements.txt": `# top
-r requirements/base.txt
--index-url https://pypi.org/simple
Django==4.2.7 \
    --hash=sha256:aaaa \
    --hash=sha256:bbbb
requests[socks]>=2.31,<3 ; python_version >= "3.8"
zope.interface
git+https://example.com/repo.git#egg=Some_Pkg
-e ./local
./also-local
attrs==23.1.0
    # via
    #   -r requirements.in
    #   cattrs
cattrs==23.2.0
    # via django
`,
		"requirements/base.txt": "Flask===3.0.0\n",
	})
	data, _, _ := tr.t.read("requirements.txt")
	var sources []string
	ps, err := parseRequirements(tr.t, "requirements.txt", data, 0, &sources)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sources, "|") != "--index-url https://pypi.org/simple" {
		t.Errorf("sources: %q", sources)
	}
	m := pkgsByName(ps)
	for k, want := range map[string][3]bool{ // direct, registry, pinned
		"flask@3.0.0":        {true, true, true},
		"django@4.2.7":       {true, true, true},
		"requests@>=2.31,<3": {true, true, false},
		"zope-interface@":    {true, true, false},
		"attrs@23.1.0":       {true, true, true},
		"cattrs@23.2.0":      {false, true, true},
		"some-pkg@git+https://example.com/repo.git#egg=Some_Pkg": {true, false, false},
	} {
		p, ok := m[k]
		if !ok {
			t.Errorf("missing %s in %v", k, m)
			continue
		}
		if p.direct != want[0] || p.registry != want[1] || p.pinned != want[2] {
			t.Errorf("%s: %+v", k, p)
		}
	}
	if len(m) != 7 {
		t.Errorf("got %d, want 7: %v", len(m), m)
	}
	for name, body := range map[string]string{
		"garbage":     "!!! not a requirement\n",
		"bad version": "requests=2.0\n",
		"unknown opt": "--frobnicate\n",
		"escaping -r": "-r ../../etc/passwd\n",
		"missing -r":  "-r nothere.txt\n",
		"absolute -r": "-r /etc/passwd\n",
	} {
		tr := tree(t, map[string]string{"requirements.txt": body})
		data, _, _ := tr.t.read("requirements.txt")
		if _, err := parseRequirements(tr.t, "requirements.txt", data, 0, new([]string)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

const poetryLock = `# This file is automatically @generated by Poetry
[[package]]
name = "Requests"
version = "2.31.0"
description = "HTTP"
optional = false
python-versions = ">=3.7"
files = [
    {file = "requests-2.31.0-py3-none-any.whl", hash = "sha256:aa"},
    {file = "requests-2.31.0.tar.gz", hash = "sha256:bb"},
]

[package.dependencies]
idna = ">=2.5"

[[package]]
name = "idna"
version = "3.6"
files = [
    {file = "idna-3.6.tar.gz", hash = "sha256:cc"},
]

[[package]]
name = "mylib"
version = "0.1.0"
files = []

[package.source]
type = "directory"
url = "../mylib"

[[package]]
name = "gitdep"
version = "1.0.0"
files = []

[package.source]
type = "git"
url = "https://example.com/gitdep.git"

[[package]]
name = "old_style"
version = "1.0"

[metadata]
lock-version = "1.1"

[metadata.files]
old-style = [
    {file = "old_style-1.0-py3-none-any.whl", hash = "sha256:dd"},
]
`

func TestParsePoetryLock(t *testing.T) {
	ps, err := parsePoetryLock([]byte(poetryLock), []byte("[tool.poetry.dependencies]\npython = \"^3.10\"\nrequests = \"^2.31\"\n\n[tool.poetry.group.dev.dependencies]\nGitDep = {git = \"x\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	m := pkgsByName(ps)
	if len(m) != 4 {
		t.Errorf("got %d, want 4 (the directory package is the project's own): %v", len(m), m)
	}
	for k, want := range map[string]struct {
		direct, registry bool
		scripts          int8
	}{
		"requests@2.31.0": {true, true, scriptsNo},
		"idna@3.6":        {false, true, scriptsYes},
		"gitdep@1.0.0":    {true, false, scriptsYes},
		"old-style@1.0":   {false, true, scriptsNo},
	} {
		p := m[k]
		if p.direct != want.direct || p.registry != want.registry || p.scripts != want.scripts {
			t.Errorf("%s: %+v", k, p)
		}
	}
	direct := pyprojectDirect("[project]\nname = \"x\"\ndependencies = [\n  \"httpx>=0.27\",\n  'Rich[jupyter]',\n]\n")
	if !direct["httpx"] || !direct["rich"] || direct["x"] {
		t.Errorf("PEP 621 dependencies: %v", direct)
	}
	for _, bad := range []string{"[[package]]\nname = \"a\"\n", "hello = 1\n", "[[package]\nname = \"a\"\nversion = \"1\"\n"} {
		if _, err := parsePoetryLock([]byte(bad), nil); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func npmLock(pkgs ...string) string {
	var entries []string
	var root []string
	for _, p := range pkgs {
		name, ver, _ := strings.Cut(p, "@")
		extra := ""
		if strings.HasSuffix(ver, "!") {
			ver = strings.TrimSuffix(ver, "!")
			extra = `, "hasInstallScript": true`
		}
		entries = append(entries, fmt.Sprintf(`"node_modules/%s": {"version": "%s"%s}`, name, ver, extra))
		root = append(root, fmt.Sprintf(`"%s": "*"`, name))
	}
	return fmt.Sprintf(`{"lockfileVersion": 3, "packages": {"": {"dependencies": {%s}}, %s}}`,
		strings.Join(root, ", "), strings.Join(entries, ", "))
}

func compare(t *testing.T, head, base map[string]string, pol *Policy, reg *Registry) *DepsResult {
	t.Helper()
	res, err := CompareDeps(tree(t, head), tree(t, base), DepsOptions{Policy: pol, Registry: reg,
		Now: func() time.Time { return time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func change(res *DepsResult, pkg string) *DepChange {
	for i := range res.Changes {
		if res.Changes[i].Package == pkg {
			return &res.Changes[i]
		}
	}
	return nil
}

func check(res *DepsResult, name string) DepCheck {
	for _, c := range res.Checks {
		if c.Name == name {
			return c
		}
	}
	return DepCheck{}
}

func allowPolicy(rules ...DepAllowRule) *Policy {
	return &Policy{Deps: &DepsPolicy{Allow: rules}}
}

func TestDepsUnchangedPasses(t *testing.T) {
	files := map[string]string{"package-lock.json": npmLock("a@1.0.0", "esbuild@0.19.2!"), "go.mod": goModA, "go.sum": goSumA}
	res := compare(t, files, files, nil, nil)
	if len(res.Changes) != 0 || len(DepFailures(res, DefaultDepFailOn)) != 0 {
		t.Errorf("changes in an unchanged set: %+v", res.Changes)
	}
	if len(res.Lockfiles) != 2 {
		t.Errorf("lockfiles: %+v", res.Lockfiles)
	}
	for _, c := range res.Checks {
		if c.Status != "nothing-to-check" || c.Online != 0 {
			t.Errorf("check %+v", c)
		}
	}
}

func TestDepsNewPackageNeedsAllowRule(t *testing.T) {
	base := map[string]string{"package-lock.json": npmLock("a@1.0.0")}
	head := map[string]string{"package-lock.json": npmLock("a@1.0.0", "evil@6.6.6")}
	res := compare(t, head, base, nil, nil)
	c := change(res, "npm/evil")
	if c == nil || c.Status != DepNotAllowed || c.Change != "added" || len(DepFailures(res, DefaultDepFailOn)) != 1 {
		t.Fatalf("new package: %+v", res.Changes)
	}
	// A rule for another package, or one differing in case, doesn't allow it.
	for _, r := range []DepAllowRule{{Package: "npm/evil-extra", Reason: "x"}, {Package: "npm/Evil", Reason: "x"},
		{Package: "pypi/evil", Reason: "x"}, {Package: "npm/ev", Reason: "x"}} {
		res := compare(t, head, base, allowPolicy(r), nil)
		if change(res, "npm/evil").Status != DepNotAllowed {
			t.Errorf("rule %q allowed npm/evil", r.Package)
		}
	}
	for _, r := range []DepAllowRule{{Package: "npm/evil", Reason: "vetted"}, {Package: "npm/ev*", Reason: "vetted"}} {
		res := compare(t, head, base, allowPolicy(r), nil)
		c := change(res, "npm/evil")
		if c.Status != DepAllowed || c.AllowedBy != "vetted" || len(DepFailures(res, DefaultDepFailOn)) != 0 {
			t.Errorf("rule %q: %+v", r.Package, c)
		}
	}
	if a := check(res, "allow-list"); a.Status != "ran" || a.Packages != 1 || a.Offline != 1 {
		t.Errorf("allow-list check: %+v", a)
	}
}

func TestDepsTransitiveGoModuleNeedsAllowRule(t *testing.T) {
	base := map[string]string{"go.mod": goModA, "go.sum": goSumA}
	head := map[string]string{"go.mod": goModA, "go.sum": goSumA +
		"github.com/Acme/deep v0.0.9 h1:HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH=\n"}
	res := compare(t, head, base, allowPolicy(DepAllowRule{Package: "golang/github.com/acme/*", Reason: "ours"}), nil)
	c := change(res, "golang/github.com/Acme/deep")
	if c == nil || c.Status != DepNotAllowed || c.Direct {
		t.Fatalf("case-changed module path slipped through: %+v", res.Changes)
	}
	if s := check(res, "install-scripts"); s.NotApplicable != 1 || s.Status != "not-applicable" {
		t.Errorf("install-scripts on a Go module: %+v", s)
	}
	if p := check(res, "publisher"); p.NotApplicable != 1 || p.Online != 0 {
		t.Errorf("publisher on a Go module: %+v", p)
	}
}

func TestDepsPyPINamesNormalised(t *testing.T) {
	base := map[string]string{"requirements.txt": "flask==3.0.0\n"}
	head := map[string]string{"requirements.txt": "Flask==3.0.0\nZope.Interface==6.0\n"}
	res := compare(t, head, base, allowPolicy(DepAllowRule{Package: "pypi/zope_interface", Reason: "ok"}), nil)
	if len(res.Changes) != 1 || res.Changes[0].Status != DepAllowed {
		t.Errorf("%+v", res.Changes)
	}
}

func TestDepsUpdatedAndRemoved(t *testing.T) {
	base := map[string]string{"package-lock.json": npmLock("a@1.0.0", "b@1.0.0")}
	head := map[string]string{"package-lock.json": npmLock("a@1.1.0")}
	res := compare(t, head, base, nil, nil)
	a, b := change(res, "npm/a"), change(res, "npm/b")
	if a == nil || a.Status != DepUpdated || a.From[0] != "1.0.0" || a.To[0] != "1.1.0" {
		t.Errorf("updated: %+v", a)
	}
	if b == nil || b.Status != DepRemoved {
		t.Errorf("removed: %+v", b)
	}
	if len(DepFailures(res, DefaultDepFailOn)) != 0 {
		t.Errorf("an update without install scripts failed")
	}
	if len(DepFailures(res, []string{DepUpdated})) != 1 {
		t.Errorf("fail_on updated doesn't fail")
	}
}

func TestDepsInstallScriptOffline(t *testing.T) {
	base := map[string]string{"package-lock.json": npmLock("esbuild@0.18.0")}
	head := map[string]string{"package-lock.json": npmLock("esbuild@0.19.2!")}
	res := compare(t, head, base, nil, nil)
	c := change(res, "npm/esbuild")
	if c == nil || c.Status != DepInstallScript || c.Change != "updated" {
		t.Fatalf("%+v", res.Changes)
	}
	s := check(res, "install-scripts")
	if s.Status != "ran" || s.Offline != 1 || s.Online != 0 {
		t.Errorf("install-scripts: %+v", s)
	}
	p := check(res, "publisher")
	if p.Status != "not-run" || p.Online != 0 || p.NotRun[whyNeedsOnline] != 1 {
		t.Errorf("publisher offline: %+v", p)
	}
	res = compare(t, head, base, allowPolicy(DepAllowRule{Package: "npm/esbuild", Reason: "binary", InstallScripts: true}), nil)
	if c := change(res, "npm/esbuild"); c.Status != DepUpdated {
		t.Errorf("install_scripts rule: %+v", c)
	}
	// An allow rule without install_scripts allows the package, not its script.
	res = compare(t, head, map[string]string{}, allowPolicy(DepAllowRule{Package: "npm/esbuild", Reason: "x"}), nil)
	if c := change(res, "npm/esbuild"); c.Status != DepInstallScript {
		t.Errorf("plain rule accepted an install script: %+v", c)
	}
	// Poetry: no wheel, offline.
	res = compare(t, map[string]string{"poetry.lock": poetryLock}, map[string]string{}, allowPolicy(DepAllowRule{Package: "pypi/*", Reason: "x"}), nil)
	if c := change(res, "pypi/idna"); c == nil || c.Status != DepInstallScript {
		t.Errorf("poetry sdist only: %+v", c)
	}
}

func TestDepsOfflineNeverClaimsOnline(t *testing.T) {
	base := map[string]string{"requirements.txt": "flask==3.0.0\n"}
	head := map[string]string{"requirements.txt": "flask==3.0.1\nnewpkg==1.0\n",
		"package-lock.json": `{"lockfileVersion": 1, "dependencies": {"x": {"version": "1.0.0"}}}`}
	res := compare(t, head, base, nil, nil)
	if res.Online {
		t.Error("result says online")
	}
	for _, c := range res.Checks {
		if c.Online != 0 || c.Cached != 0 {
			t.Errorf("offline check claims online work: %+v", c)
		}
	}
	s := check(res, "install-scripts")
	if s.Status != "not-run" || s.NotRun[whyNeedsOnline] != 3 {
		t.Errorf("install-scripts: %+v", s)
	}
}

func TestDepsUnreadLockfile(t *testing.T) {
	base := map[string]string{"yarn.lock": "a@1:\n  version 1\n"}
	res := compare(t, map[string]string{"yarn.lock": "a@1:\n  version 1\nevil@6:\n  version 6\n"}, base, nil, nil)
	if len(res.Changes) != 1 || res.Changes[0].Status != DepNotChecked || len(DepFailures(res, DefaultDepFailOn)) != 1 {
		t.Errorf("changed yarn.lock: %+v", res.Changes)
	}
	res = compare(t, base, base, nil, nil)
	if len(res.Changes) != 0 {
		t.Errorf("unchanged yarn.lock: %+v", res.Changes)
	}
}

func TestDepsMalformedLockfileFails(t *testing.T) {
	good := map[string]string{"package-lock.json": npmLock("a@1.0.0")}
	for name, files := range map[string]map[string]string{
		"npm":          {"package-lock.json": `{"lockfileVersion": 3, "packages": `},
		"go.mod":       {"go.mod": "module x\nrequire (\n"},
		"go.sum":       {"go.mod": "module x\n", "go.sum": "broken\n"},
		"requirements": {"requirements.txt": "%%%\n"},
		"poetry":       {"poetry.lock": "[[package]]\nname = \"x\"\n"},
	} {
		for _, side := range []string{"head", "base"} {
			h, b := files, good
			if side == "base" {
				h, b = good, files
			}
			_, err := CompareDeps(tree(t, h), tree(t, b), DepsOptions{})
			if err == nil {
				t.Errorf("%s broken on the %s side: no error", name, side)
			}
		}
	}
}

func TestDepsLockfileSearch(t *testing.T) {
	head := map[string]string{
		"web/package-lock.json":            npmLock("a@1.0.0"),
		"node_modules/x/package-lock.json": npmLock("hidden@1.0.0"),
		"testdata/requirements.txt":        "ignored==1.0\n",
		"requirements/dev.txt":             "pytest==8.0.0\n",
		"vendor/github.com/x/y/go.mod":     "module y\nrequire z v1.0.0\n",
	}
	pol := &Policy{Ignore: []string{"testdata/**"}, Deps: &DepsPolicy{Allow: []DepAllowRule{{Package: "npm/a", Reason: "x"}}}}
	res := compare(t, head, map[string]string{}, pol, nil)
	var got []string
	for _, l := range res.Lockfiles {
		got = append(got, l.Path)
	}
	if strings.Join(got, ",") != "requirements/dev.txt,web/package-lock.json" {
		t.Errorf("lockfiles found: %v", got)
	}
	// deps.lockfiles replaces the search, and a name in neither tree is an error.
	pol.Deps.Lockfiles = []string{"web/package-lock.json"}
	res = compare(t, head, map[string]string{}, pol, nil)
	if len(res.Lockfiles) != 1 {
		t.Errorf("deps.lockfiles: %+v", res.Lockfiles)
	}
	pol.Deps.Lockfiles = []string{"api/package-lock.json"}
	if _, err := CompareDeps(tree(t, head), tree(t, nil), DepsOptions{Policy: pol}); err == nil {
		t.Error("a deps.lockfiles entry in neither tree passed")
	}
}

func TestDepsPolicyValidation(t *testing.T) {
	for name, body := range map[string]string{
		"no ecosystem":   `{"deps": {"allow": [{"package": "left-pad", "reason": "x"}]}}`,
		"bad ecosystem":  `{"deps": {"allow": [{"package": "cargo/serde", "reason": "x"}]}}`,
		"no reason":      `{"deps": {"allow": [{"package": "npm/a"}]}}`,
		"empty fail_on":  `{"deps": {"fail_on": []}}`,
		"bad status":     `{"deps": {"fail_on": ["conflict"]}}`,
		"unknown key":    `{"deps": {"allwo": []}}`,
		"unknown rule":   `{"deps": {"allow": [{"package": "npm/a", "reason": "x", "scripts": true}]}}`,
		"negative days":  `{"deps": {"publisher_days": -1}}`,
		"bad lockfile":   `{"deps": {"lockfiles": ["../other/package-lock.json"]}}`,
		"not a lockfile": `{"deps": {"lockfiles": ["README.md"]}}`,
	} {
		p := filepath.Join(t.TempDir(), "p.json")
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := LoadPolicy(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(p, []byte(`{"license": "MIT", "deps": {"allow": [{"package": "golang/golang.org/x/*", "reason": "Go team", "install_scripts": true}], "fail_on": ["not-allowed"], "publisher_days": 30, "lockfiles": ["go.mod"]}}`), 0o644)
	pol, err := LoadPolicy(p)
	if err != nil || pol.Deps.PublisherDays != 30 || !pol.Deps.Allow[0].InstallScripts {
		t.Errorf("good policy: %v %+v", err, pol)
	}
}

func TestDepsGitRevision(t *testing.T) {
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
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "package-lock.json"), []byte(npmLock("a@1.0.0")), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "base")
	os.WriteFile(filepath.Join(dir, "app", "package-lock.json"), []byte(npmLock("a@1.0.0", "evil@1.0.0")), 0o644)
	head, _ := DepsDir(filepath.Join(dir, "app"))
	base, err := DepsGit(filepath.Join(dir, "app"), "main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := CompareDeps(head, base, DepsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Package != "npm/evil" || res.Lockfiles[0].Path != "package-lock.json" {
		t.Errorf("against git main: %+v %+v", res.Changes, res.Lockfiles)
	}
	if _, err := DepsGit(dir, "nosuchbranch"); err == nil {
		t.Error("a missing revision was accepted")
	}
	if _, err := DepsGit(dir, "--output=/tmp/x"); err == nil {
		t.Error("an option as revision was accepted")
	}
}

// fakeRegistry serves npm packuments and PyPI JSON from maps.
type fakeRegistry struct {
	npm  map[string]string
	pypi map[string]string
	hits atomic.Int32
	fail bool
	srv  *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	f := &fakeRegistry{npm: map[string]string{}, pypi: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		p := r.URL.EscapedPath()
		var body string
		var ok bool
		if strings.HasPrefix(p, "/npm/") {
			body, ok = f.npm[strings.TrimPrefix(p, "/npm/")]
		} else if strings.HasPrefix(p, "/pypi/") {
			body, ok = f.pypi[strings.TrimPrefix(p, "/pypi/")]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegistry) registry(cache string) *Registry {
	return &Registry{NPMURL: f.srv.URL + "/npm", PyPIURL: f.srv.URL + "/pypi", CachePath: cache}
}

type npmVer struct {
	version, publisher, time string
	maintainers              []string
	scripts                  bool
}

func packument(vs ...npmVer) string {
	doc := map[string]any{"versions": map[string]any{}, "time": map[string]string{"created": "2015-01-01T00:00:00Z"}}
	for _, v := range vs {
		var ms []map[string]string
		for _, m := range v.maintainers {
			ms = append(ms, map[string]string{"name": m})
		}
		ver := map[string]any{"_npmUser": map[string]string{"name": v.publisher}, "maintainers": ms}
		if v.scripts {
			ver["scripts"] = map[string]string{"postinstall": "node install.js"}
		}
		doc["versions"].(map[string]any)[v.version] = ver
		doc["time"].(map[string]string)[v.version] = v.time
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func TestDepsOnlinePublisherChange(t *testing.T) {
	f := newFakeRegistry(t)
	f.npm["colors"] = packument(
		npmVer{"1.4.0", "marak", "2019-01-01T00:00:00Z", []string{"marak", "dabh"}, false},
		npmVer{"1.4.1", "stranger", "2026-10-20T00:00:00Z", []string{"stranger"}, true},
		npmVer{"1.4.2", "dabh", "2026-10-25T00:00:00Z", []string{"dabh"}, false},
	)
	f.npm["@acme%2Fui"] = packument(
		npmVer{"1.0.0", "alice", "2025-01-01T00:00:00Z", []string{"alice"}, false},
		npmVer{"2.0.0", "mallory", "2026-10-30T00:00:00Z", []string{"mallory"}, false},
	)
	cache := filepath.Join(t.TempDir(), "cache.json")
	base := map[string]string{"package-lock.json": `{"lockfileVersion": 1, "dependencies": {"colors": {"version": "1.4.0"}}}`}
	head := map[string]string{"package-lock.json": `{"lockfileVersion": 1, "dependencies": {"colors": {"version": "1.4.1"}, "@acme/ui": {"version": "2.0.0"}}}`}
	pol := allowPolicy(DepAllowRule{Package: "npm/@acme/*", Reason: "ours"})
	res := compare(t, head, base, pol, f.registry(cache))
	c := change(res, "npm/colors")
	if c == nil || c.Status != DepPublisherChanged {
		t.Fatalf("colors 1.4.1: %+v", res.Changes)
	}
	statuses := strings.Join(c.Statuses(), ",")
	if !strings.Contains(statuses, DepInstallScript) {
		t.Errorf("colors 1.4.1 runs a postinstall: %s", statuses)
	}
	ui := change(res, "npm/@acme/ui")
	if ui == nil || ui.Status != DepPublisherChanged || !strings.Contains(strings.Join(ui.Statuses(), ","), DepPublisherChanged) {
		t.Errorf("@acme/ui 2.0.0 by a new publisher, compared with the release before: %+v", ui)
	}
	p := check(res, "publisher")
	if p.Status != "ran" || p.Online != 2 || p.Cached != 0 {
		t.Errorf("publisher check: %+v", p)
	}
	s := check(res, "install-scripts")
	if s.Online != 2 || s.Status != "ran" {
		t.Errorf("install-scripts check: %+v", s)
	}
	// From the cache, with the registry down: same answer, and it says so.
	f.fail = true
	res = compare(t, head, base, pol, f.registry(cache))
	if c := change(res, "npm/colors"); c.Status != DepPublisherChanged {
		t.Errorf("from cache: %+v", c)
	}
	if p := check(res, "publisher"); p.Cached != 2 || p.Online != 2 {
		t.Errorf("from cache: %+v", p)
	}
	f.fail = false
	// A maintainer of the version before publishing isn't a change; nor is an
	// old change; nor one the policy accepts.
	head2 := map[string]string{"package-lock.json": `{"lockfileVersion": 1, "dependencies": {"colors": {"version": "1.4.2"}}}`}
	if c := change(compare(t, head2, base, nil, f.registry("")), "npm/colors"); c.Status != DepUpdated {
		t.Errorf("1.4.2 by a maintainer of 1.4.0: %+v", c)
	}
	old := &Policy{Deps: &DepsPolicy{PublisherDays: 5, Allow: pol.Deps.Allow}}
	if c := change(compare(t, head, base, old, f.registry("")), "npm/colors"); strings.Contains(strings.Join(c.Statuses(), ","), DepPublisherChanged) {
		t.Errorf("a change older than publisher_days: %+v", c)
	}
	acc := allowPolicy(DepAllowRule{Package: "npm/colors", Reason: "checked", PublisherChange: true, InstallScripts: true})
	if c := change(compare(t, head, base, acc, f.registry("")), "npm/colors"); c.Status != DepUpdated {
		t.Errorf("publisher_change rule: %+v", c)
	}
}

func TestDepsOnlineErrorsFail(t *testing.T) {
	f := newFakeRegistry(t)
	head := map[string]string{"package-lock.json": npmLock("ghost@1.0.0")}
	pol := allowPolicy(DepAllowRule{Package: "npm/*", Reason: "x"})
	if _, err := CompareDeps(tree(t, head), tree(t, nil), DepsOptions{Policy: pol, Registry: f.registry("")}); err == nil ||
		!strings.Contains(err.Error(), "isn't on the npm registry") {
		t.Errorf("a package missing from the registry: %v", err)
	}
	f.npm["ghost"] = packument(npmVer{"0.9.0", "a", "2020-01-01T00:00:00Z", nil, false})
	if _, err := CompareDeps(tree(t, head), tree(t, nil), DepsOptions{Policy: pol, Registry: f.registry("")}); err == nil {
		t.Error("a version missing from the registry passed")
	}
	f.fail = true
	if _, err := CompareDeps(tree(t, head), tree(t, nil), DepsOptions{Policy: pol, Registry: f.registry("")}); err == nil {
		t.Error("a registry that's down passed")
	}
	f.fail = false
	f.npm["ghost"] = "not json"
	if _, err := CompareDeps(tree(t, head), tree(t, nil), DepsOptions{Policy: pol, Registry: f.registry("")}); err == nil {
		t.Error("a garbage answer passed")
	}
}

func TestDepsOnlinePyPI(t *testing.T) {
	f := newFakeRegistry(t)
	f.pypi["oldlib/1.0/json"] = `{"info": {"author": "Ann", "author_email": "ann@example.com"}, "urls": [{"packagetype": "sdist", "upload_time_iso_8601": "2026-10-28T00:00:00Z"}]}`
	f.pypi["oldlib/json"] = `{"releases": {"0.9": [{"upload_time_iso_8601": "2024-01-01T00:00:00Z"}], "1.0": [{"upload_time_iso_8601": "2026-10-28T00:00:00Z"}]}}`
	f.pypi["oldlib/0.9/json"] = `{"info": {"author": "Bob", "author_email": "bob@example.com"}, "urls": [{"packagetype": "bdist_wheel", "upload_time_iso_8601": "2024-01-01T00:00:00Z"}]}`
	head := map[string]string{"requirements.txt": "oldlib==1.0\nunpinned>=1\n"}
	pol := allowPolicy(DepAllowRule{Package: "pypi/*", Reason: "x"})
	res := compare(t, head, nil, pol, f.registry(""))
	c := change(res, "pypi/oldlib")
	st := strings.Join(c.Statuses(), ",")
	if c.Status != DepPublisherChanged || !strings.Contains(st, DepInstallScript) {
		t.Errorf("oldlib 1.0: %s %+v", st, c)
	}
	if s := check(res, "install-scripts"); s.Status != "partly" || s.NotRun[whyNotPinned] != 1 || s.Online != 1 {
		t.Errorf("install-scripts: %+v", s)
	}
}

// genLock writes one lockfile of the given kind naming pkgs (name@version,
// a trailing ! for an install script), in the given order.
func genLock(kind string, pkgs []string) map[string]string {
	switch kind {
	case "npm":
		return map[string]string{"package-lock.json": npmLock(pkgs...)}
	case "npm1":
		var e []string
		for _, p := range pkgs {
			n, v, _ := strings.Cut(strings.TrimSuffix(p, "!"), "@")
			e = append(e, fmt.Sprintf(`"%s": {"version": "%s"}`, n, v))
		}
		return map[string]string{"package-lock.json": `{"lockfileVersion": 1, "dependencies": {` + strings.Join(e, ", ") + `}}`}
	case "go":
		var b strings.Builder
		for _, p := range pkgs {
			n, v, _ := strings.Cut(strings.TrimSuffix(p, "!"), "@")
			fmt.Fprintf(&b, "%s v%s h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n", n, v)
		}
		return map[string]string{"go.mod": "module example.com/app\n", "go.sum": b.String()}
	case "pip":
		var b strings.Builder
		for _, p := range pkgs {
			n, v, _ := strings.Cut(strings.TrimSuffix(p, "!"), "@")
			fmt.Fprintf(&b, "%s==%s\n", n, v)
		}
		return map[string]string{"requirements.txt": b.String()}
	case "poetry":
		var b strings.Builder
		for _, p := range pkgs {
			n, v, _ := strings.Cut(strings.TrimSuffix(p, "!"), "@")
			fmt.Fprintf(&b, "[[package]]\nname = %q\nversion = %q\nfiles = [\n    {file = \"%s-%s-py3-none-any.whl\", hash = \"sha256:aa\"},\n]\n\n", n, v, n, v)
		}
		return map[string]string{"poetry.lock": b.String()}
	}
	panic(kind)
}

var genEco = map[string]string{"npm": EcoNPM, "npm1": EcoNPM, "go": EcoGo, "pip": EcoPyPI, "poetry": EcoPyPI}

// TestDepsGatePromise checks the gate's promise on generated changes: a
// new package that isn't allowed always fails, an allowed one or an
// unchanged set (in any order) always passes, a new install script always
// fails, and a broken lockfile is always an error. The counts are
// recorded in test/results/deps/promise.txt.
func TestDepsGatePromise(t *testing.T) {
	rng := uint64(20261031)
	next := func(n int) int {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int(rng % uint64(n))
	}
	kinds := []string{"npm", "npm1", "go", "pip", "poetry"}
	var risky, riskyFailed, safe, safePassed, scripts, scriptsFailed, broken, brokenErr int
	for i := 0; i < 400; i++ {
		kind := kinds[i%len(kinds)]
		var basePkgs []string
		for j := 0; j < 3+next(20); j++ {
			basePkgs = append(basePkgs, fmt.Sprintf("pkg%d-%d@%d.%d.%d", i, j, next(5), next(10), next(10)))
		}
		shuffled := append([]string(nil), basePkgs...)
		for j := len(shuffled) - 1; j > 0; j-- {
			k := next(j + 1)
			shuffled[j], shuffled[k] = shuffled[k], shuffled[j]
		}
		newName := fmt.Sprintf("new%d", i)
		withNew := append(append([]string(nil), shuffled...), newName+"@1.0.0")
		run := func(head []string, pol *Policy) []DepChange {
			res, err := CompareDeps(tree(t, genLock(kind, head)), tree(t, genLock(kind, basePkgs)), DepsOptions{Policy: pol})
			if err != nil {
				t.Fatalf("%s case %d: %v", kind, i, err)
			}
			return DepFailures(res, DefaultDepFailOn)
		}
		// Unchanged, reordered: passes.
		safe++
		if len(run(shuffled, nil)) == 0 {
			safePassed++
		} else {
			t.Errorf("%s case %d: an unchanged set failed", kind, i)
		}
		// New and not allowed: fails, naming the package.
		risky++
		if f := run(withNew, allowPolicy(DepAllowRule{Package: genEco[kind] + "/pkg*", Reason: "x"})); len(f) == 1 && f[0].Name == newName && f[0].Status == DepNotAllowed {
			riskyFailed++
		} else {
			t.Errorf("%s case %d: new package %s gave %+v", kind, i, newName, f)
		}
		// New and allowed: passes.
		safe++
		if len(run(withNew, allowPolicy(DepAllowRule{Package: genEco[kind] + "/" + newName, Reason: "x"}))) == 0 {
			safePassed++
		} else {
			t.Errorf("%s case %d: an allowed package failed", kind, i)
		}
		// An update that starts running an install script (npm lockfile v3).
		if kind == "npm" {
			scripts++
			bumped := append([]string(nil), shuffled...)
			k := next(len(bumped))
			bumped[k] = strings.Replace(bumped[k], "@", "@9", 1) + "!"
			if f := run(bumped, nil); len(f) == 1 && f[0].Status == DepInstallScript {
				scriptsFailed++
			} else {
				t.Errorf("case %d: install script gave %+v", i, f)
			}
		}
		// A broken lockfile: JSON cut short, or a line no lockfile has.
		broken++
		files := genLock(kind, withNew)
		for name, body := range files {
			if name == "go.mod" {
				continue
			}
			if strings.HasSuffix(name, ".json") {
				files[name] = body[:next(len(body)-1)]
			} else {
				lines := strings.SplitAfter(body, "\n")
				k := next(len(lines))
				bad := "@@ not a lockfile line @@\n"
				files[name] = strings.Join(lines[:k], "") + bad + strings.Join(lines[k:], "")
			}
		}
		if _, err := CompareDeps(tree(t, files), tree(t, genLock(kind, basePkgs)), DepsOptions{}); err != nil {
			brokenErr++
		} else {
			t.Errorf("%s case %d: a broken lockfile passed", kind, i)
		}
	}
	t.Logf("promise: %d new packages not on the allow list, %d failed; %d unchanged or allowed sets, %d passed; %d new install scripts, %d failed; %d broken lockfiles, %d stopped with an error",
		risky, riskyFailed, safe, safePassed, scripts, scriptsFailed, broken, brokenErr)
}

// noGitConfig is an empty git config file, so the tests don't depend on
// the machine's.
func noGitConfig(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(p, nil, 0o644)
	return p
}

func TestDepsPipIndexChange(t *testing.T) {
	base := map[string]string{"requirements.txt": "--index-url https://pypi.org/simple\nflask==3.0.0\n"}
	head := map[string]string{"requirements.txt": "--index-url https://pypi.org/simple\n--extra-index-url https://pkgs.example.net/simple\nflask==3.0.0\n"}
	res := compare(t, head, base, nil, nil)
	if len(res.Changes) != 1 || res.Changes[0].Status != DepNotChecked || !strings.Contains(res.Changes[0].Reason, "pkgs.example.net") {
		t.Errorf("new extra index: %+v", res.Changes)
	}
	if res := compare(t, base, head, nil, nil); len(res.Changes) != 0 {
		t.Errorf("an index taken away: %+v", res.Changes)
	}
}

func TestDepsMovedOffRegistry(t *testing.T) {
	base := map[string]string{"requirements.txt": "requests==2.32.3\n"}
	head := map[string]string{"requirements.txt": "requests @ https://files.example.net/requests-2.32.3.tar.gz\n"}
	res := compare(t, head, base, nil, nil)
	c := change(res, "pypi/requests")
	// A source archive also builds from source: install-script on top.
	if c == nil || !containsString(c.Statuses(), DepNotChecked) || len(DepFailures(res, DefaultDepFailOn)) != 1 {
		t.Errorf("moved to a URL: %+v", res.Changes)
	}
	baseNPM := map[string]string{"package-lock.json": npmLock("a@1.0.0")}
	headNPM := map[string]string{"package-lock.json": `{"lockfileVersion": 3, "packages": {"": {"dependencies": {"a": "*"}},
	  "node_modules/a": {"version": "1.0.1", "resolved": "https://npm.example.net/a/-/a-1.0.1.tgz"}}}`}
	if c := change(compare(t, headNPM, baseNPM, nil, nil), "npm/a"); c == nil || c.Status != DepNotChecked {
		t.Errorf("npm moved to another registry: %+v", c)
	}
}
