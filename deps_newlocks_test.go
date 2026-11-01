package remixware

import (
	"strings"
	"testing"
)

// Tests for the pnpm-lock.yaml, yarn.lock (1 and berry) and uv.lock
// readers (0.4.0). The promise test runs all of them on generated changes;
// these cover what each format can say.

// pkgMap(t)(parse(...)) keys a reader's packages by name@version.
func pkgMap(t *testing.T) func([]lockPackage, error) map[string]lockPackage {
	return func(pkgs []lockPackage, err error) map[string]lockPackage {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]lockPackage{}
		for _, p := range pkgs {
			m[p.name+"@"+p.version] = p
		}
		return m
	}
}

func failsWith(t *testing.T, what string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: read without an error", what)
	} else if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("%s: error %q doesn't say %q", what, err, want)
	}
}

func TestPnpmLockVersions(t *testing.T) {
	v5 := `lockfileVersion: 5.4

specifiers:
  left-pad: ^1.3.0

dependencies:
  left-pad: 1.3.0

devDependencies:
  esbuild: 0.18.0_typescript@5.0.0

packages:

  /left-pad/1.3.0:
    resolution: {integrity: sha512-XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQm6dXbEA9qg5z2nZgjrW9ZK2qGbj4qgVbTmD35k1zdTW6Q==}
    dev: false

  /esbuild/0.18.0_typescript@5.0.0:
    resolution: {integrity: sha512-AAAA}
    requiresBuild: true
    dev: true
`
	m := pkgMap(t)(parsePnpmLock([]byte(v5)))
	if p := m["left-pad@1.3.0"]; !p.direct || !p.registry || p.scripts != scriptsNo {
		t.Errorf("v5 left-pad: %+v", p)
	}
	if p := m["esbuild@0.18.0"]; !p.direct || p.scripts != scriptsYes {
		t.Errorf("v5 esbuild (peer suffix, requiresBuild): %+v", p)
	}

	v6 := `lockfileVersion: '6.0'

importers:

  .:
    dependencies:
      '@scope/lib':
        specifier: ^2.0.0
        version: 2.0.1(react@18.2.0)
      shared:
        specifier: workspace:*
        version: link:packages/shared
      local-tgz:
        specifier: file:vendor/local.tgz
        version: file:vendor/local.tgz

  packages/shared:
    devDependencies:
      ms:
        specifier: 2.1.3
        version: 2.1.3

packages:

  /@scope/lib@2.0.1(react@18.2.0):
    resolution: {integrity: sha512-AAAA}
    dev: false

  /ms@2.1.3:
    resolution: {integrity: sha512-BBBB}
    dev: true

  /react@18.2.0:
    resolution: {integrity: sha512-CCCC}
    dev: false

  /from-git@1.0.0:
    resolution: {commit: 0123456789abcdef, repo: https://github.com/example/from-git, type: git}
    name: from-git
    version: 1.0.0
    dev: false

  /elsewhere@1.0.0:
    resolution: {tarball: https://pkgs.example.net/elsewhere-1.0.0.tgz}
    dev: false

  /on-npm@1.0.0:
    resolution: {tarball: https://registry.npmjs.org/on-npm/-/on-npm-1.0.0.tgz}
    dev: false

  file:vendor/local.tgz:
    resolution: {directory: vendor/local, type: directory}
    name: local-tgz
    version: 0.0.0
    dev: false
`
	m = pkgMap(t)(parsePnpmLock([]byte(v6)))
	if len(m) != 6 {
		t.Errorf("v6: %d packages, want 6 (the folder package left out): %v", len(m), m)
	}
	if p := m["@scope/lib@2.0.1"]; !p.direct || !p.registry {
		t.Errorf("v6 scoped with peer suffix: %+v", p)
	}
	if p := m["ms@2.1.3"]; !p.direct {
		t.Errorf("v6 dev dependency of a workspace importer isn't direct: %+v", p)
	}
	if p := m["react@18.2.0"]; p.direct {
		t.Errorf("v6 peer react counted as direct: %+v", p)
	}
	if p := m["from-git@1.0.0"]; p.registry {
		t.Errorf("v6 git resolution counted as registry: %+v", p)
	}
	if p := m["elsewhere@1.0.0"]; p.registry {
		t.Errorf("v6 tarball elsewhere counted as registry: %+v", p)
	}
	if p := m["on-npm@1.0.0"]; !p.registry {
		t.Errorf("v6 tarball on the npm registry: %+v", p)
	}
	if p := m["react@18.2.0"]; p.scripts != scriptsNo {
		t.Errorf("v6 without requiresBuild should be no scripts: %+v", p)
	}

	// Lockfile 9, with the package manager's own document first.
	v9 := `---
lockfileVersion: '9.0'

importers:

  .:
    configDependencies: {}
    packageManagerDependencies:
      pnpm:
        specifier: 10.0.0
        version: 10.0.0

packages:

  pnpm@10.0.0:
    resolution: {integrity: sha512-DDDD}
    hasBin: true

snapshots:

  pnpm@10.0.0: {}

---
lockfileVersion: '9.0'

settings:
  autoInstallPeers: true
  excludeLinksFromLockfile: false

importers:

  .:
    dependencies:
      big-alias:
        specifier: npm:lodash@^4.17.0
        version: lodash@4.17.21

packages:

  lodash@4.17.21:
    resolution: {integrity: sha512-EEEE}

  '@types/node@22.0.0':
    resolution: {integrity: sha512-FFFF}

snapshots:

  lodash@4.17.21: {}

  '@types/node@22.0.0':
    dependencies:
      undici-types: 6.0.0
`
	m = pkgMap(t)(parsePnpmLock([]byte(v9)))
	if p := m["pnpm@10.0.0"]; !p.direct || p.scripts != scriptsUnknown {
		t.Errorf("v9 package manager document: %+v", p)
	}
	if p := m["lodash@4.17.21"]; !p.direct {
		t.Errorf("v9 alias npm:lodash should make lodash direct: %+v", p)
	}
	if p := m["@types/node@22.0.0"]; p.direct || !p.registry || p.scripts != scriptsUnknown {
		t.Errorf("v9 transitive scoped: %+v", p)
	}

	_, err := parsePnpmLock([]byte("lockfileVersion: '7.0'\npackages: {}\n"))
	failsWith(t, "lockfile 7", err, "7.0")
	_, err = parsePnpmLock([]byte("dependencies:\n  a: 1.0.0\n"))
	failsWith(t, "no lockfileVersion", err, "lockfileVersion")
	_, err = parsePnpmLock([]byte("lockfileVersion: '9.0'\npackages:\n  a@1.0.0: &x\n    resolution: {integrity: sha512-A}\n"))
	failsWith(t, "an anchor", err, "")
	_, err = parsePnpmLock([]byte("lockfileVersion: '9.0'\npackages:\n  a@1.0.0:\n    resolution: {integrity: sha512-A}\n  a@1.0.0:\n    resolution: {integrity: sha512-B}\n"))
	failsWith(t, "a package given twice", err, "")
	_, err = parsePnpmLock([]byte("lockfileVersion: '9.0'\npackages:\n  a@1.0.0:\n    dev: false\n"))
	failsWith(t, "no resolution", err, "resolution")
	_, err = parsePnpmLock([]byte("lockfileVersion: '9.0'\npackages:\n  a@1.0.0:\n    resolution: {something: else}\n"))
	failsWith(t, "an unknown resolution", err, "resolution")
	_, err = parsePnpmLock([]byte("lockfileVersion: '9.0'\npackages:\n  Bad Name@1.0.0:\n    resolution: {integrity: sha512-A}\n"))
	failsWith(t, "a name npm doesn't allow", err, "name")
}

func TestYarnBerryLock(t *testing.T) {
	lock := `# This file is generated by running "yarn install" inside your project.
# Manual changes might be lost - proceed with caution!

__metadata:
  version: 8
  cacheKey: 10c0

"app@workspace:.":
  version: 0.0.0-use.local
  resolution: "app@workspace:."
  dependencies:
    lib: "workspace:^"
    ms: "npm:^2.1.0"
    old-name: "npm:new-name@^1.0.0"
  languageName: unknown
  linkType: soft

"lib@workspace:^, lib@workspace:packages/lib":
  version: 0.0.0-use.local
  resolution: "lib@workspace:packages/lib"
  dependencies:
    debug: "npm:^4.3.0"
  languageName: unknown
  linkType: soft

"ms@npm:^2.1.0, ms@npm:^2.1.3":
  version: 2.1.3
  resolution: "ms@npm:2.1.3"
  checksum: 10c0/aaaa
  languageName: node
  linkType: hard

"debug@npm:^4.3.0":
  version: 4.3.7
  resolution: "debug@npm:4.3.7"
  dependencies:
    ms: "npm:^2.1.3"
  checksum: 10c0/bbbb
  languageName: node
  linkType: hard

"old-name@npm:new-name@^1.0.0":
  version: 1.0.2
  resolution: "new-name@npm:1.0.2"
  languageName: node
  linkType: hard

"typescript@patch:typescript@npm%3A^5.6.0#optional!builtin<compat/typescript>":
  version: 5.6.3
  resolution: "typescript@patch:typescript@npm%3A5.6.3#optional!builtin<compat/typescript>::version=5.6.3&hash=8c6c40"
  languageName: node
  linkType: hard

"forked@https://github.com/example/forked.git#commit=abc":
  version: 1.0.0
  resolution: "forked@https://github.com/example/forked.git#commit=abc"
  languageName: node
  linkType: hard

"made@exec:./scripts/make.js":
  version: 0.0.0
  resolution: "made@exec:./scripts/make.js#./scripts/make.js::hash=1&locator=app%40workspace%3A."
  languageName: node
  linkType: hard

"tools@link:./tools::locator=app%40workspace%3A.":
  version: 0.0.0-use.local
  resolution: "tools@link:./tools::locator=app%40workspace%3A."
  languageName: node
  linkType: soft
`
	m := pkgMap(t)(parseYarnBerry([]byte(lock)))
	if len(m) != 6 {
		t.Errorf("%d packages, want 6 (workspaces and links left out): %v", len(m), m)
	}
	if p := m["ms@2.1.3"]; !p.direct || !p.registry || p.scripts != scriptsUnknown {
		t.Errorf("ms: %+v", p)
	}
	if p := m["debug@4.3.7"]; !p.direct {
		t.Errorf("a workspace's dependency is direct: %+v", p)
	}
	if p := m["new-name@1.0.2"]; !p.direct {
		t.Errorf("an aliased package is named by its real name and is direct: %+v", p)
	}
	if p := m["typescript@5.6.3"]; !p.registry {
		t.Errorf("yarn's compat patch over npm is a registry package: %+v", p)
	}
	if p := m["forked@1.0.0"]; p.registry {
		t.Errorf("a git package counted as registry: %+v", p)
	}
	if p := m["made@0.0.0"]; p.scripts != scriptsYes {
		t.Errorf("exec: runs a script: %+v", p)
	}

	_, err := parseYarnBerry([]byte("\"a@npm:^1\":\n  version: 1.0.0\n  resolution: \"a@npm:1.0.0\"\n"))
	failsWith(t, "no __metadata", err, "__metadata")
	_, err = parseYarnBerry([]byte("__metadata:\n  version: 8\n\n\"a@npm:^1\":\n  version: 1.0.0\n"))
	failsWith(t, "no resolution", err, "resolution")
	_, err = parseYarnBerry([]byte("__metadata:\n  version: 8\n\n\"a@npm:^1\":\n  version: 1.0.0\n  resolution: \"a@weird:1.0.0\"\n"))
	failsWith(t, "an unknown protocol", err, "weird")
	_, err = parseYarnBerry([]byte("__metadata:\n  version: 8\n\n\"a@npm:^1\":\n  version: 1.0.0\n  resolution: \"a@npm:1.0.0\"\n  checksum: |\n    abc\n"))
	failsWith(t, "a block scalar", err, "")
}

func TestYarnV1Lock(t *testing.T) {
	pj := []byte(`{"name": "app", "dependencies": {"ms": "^2.1.0", "old-name": "npm:new-name@^1.0.0"}, "devDependencies": {"@scope/tool": "1.x"}}`)
	lock := `# THIS IS AN AUTOGENERATED FILE. DO NOT EDIT THIS FILE DIRECTLY.
# yarn lockfile v1


"@scope/tool@1.x":
  version "1.4.0"
  resolved "https://registry.yarnpkg.com/@scope/tool/-/tool-1.4.0.tgz#aaaa"
  integrity sha512-AAAA
  dependencies:
    debug "^4.0.0"

debug@^4.0.0:
  version "4.3.7"
  resolved "https://registry.npmjs.org/debug/-/debug-4.3.7.tgz#bbbb"
  integrity sha512-BBBB
  dependencies:
    ms "^2.1.3"

ms@^2.1.0, ms@^2.1.3:
  version "2.1.3"
  resolved "https://registry.yarnpkg.com/ms/-/ms-2.1.3.tgz#cccc"
  integrity sha512-CCCC

"old-name@npm:new-name@^1.0.0":
  version "1.0.2"
  resolved "https://registry.yarnpkg.com/new-name/-/new-name-1.0.2.tgz#dddd"

mirror@^1.0.0:
  version "1.0.0"
  resolved "https://pkgs.example.net/mirror-1.0.0.tgz"

"from-git@git+https://github.com/example/from-git.git":
  version "2.0.0"
  resolved "git+https://github.com/example/from-git.git#0123456789abcdef"

"local@file:./vendor/local":
  version "0.0.0"
`
	m := pkgMap(t)(parseYarnV1([]byte(lock), pj))
	if len(m) != 6 {
		t.Errorf("%d packages, want 6 (the file: one left out): %v", len(m), m)
	}
	if p := m["ms@2.1.3"]; !p.direct || !p.registry {
		t.Errorf("ms: %+v", p)
	}
	if p := m["@scope/tool@1.4.0"]; !p.direct || !p.registry {
		t.Errorf("scoped dev dependency: %+v", p)
	}
	if p := m["debug@4.3.7"]; p.direct || !p.registry {
		t.Errorf("debug (transitive, registry.npmjs.org): %+v", p)
	}
	if p := m["new-name@1.0.2"]; !p.direct || !p.registry {
		t.Errorf("alias named by its real package: %+v", p)
	}
	if p := m["mirror@1.0.0"]; p.registry {
		t.Errorf("another host counted as registry: %+v", p)
	}
	if p := m["from-git@2.0.0"]; p.registry {
		t.Errorf("git counted as registry: %+v", p)
	}

	_, err := parseYarnV1([]byte("a@^1:\n  version \"1.0.0\"\n  wibble \"x\"\n"), nil)
	failsWith(t, "an unknown field", err, "wibble")
	_, err = parseYarnV1([]byte("a@^1:\n\tversion \"1.0.0\"\n"), nil)
	failsWith(t, "a tab", err, "tab")
	_, err = parseYarnV1([]byte("a@^1, b@^1:\n  version \"1.0.0\"\n"), nil)
	failsWith(t, "one entry naming two packages", err, "two packages")
	_, err = parseYarnV1([]byte("a@^1:\n  resolved \"https://registry.yarnpkg.com/a/-/a-1.0.0.tgz\"\n"), nil)
	failsWith(t, "no version", err, "version")
	_, err = parseYarnV1([]byte("a@^1:\n  version \"1.0.0\"\n  version \"1.0.1\"\n"), nil)
	failsWith(t, "a field given twice", err, "twice")
	_, err = parseYarnV1([]byte("a@^1\n  version \"1.0.0\"\n"), nil)
	failsWith(t, "a header without a colon", err, "")
	_, err = parseYarnV1([]byte("\"a@^1:\n  version \"1.0.0\"\n"), nil)
	failsWith(t, "an unclosed quote", err, "")
}

func TestYarnLockKindByContent(t *testing.T) {
	head := map[string]string{"package.json": `{"dependencies": {"ms": "^2.1.3"}}`,
		"yarn.lock": "__metadata:\n  version: 8\n\n\"ms@npm:^2.1.3\":\n  version: 2.1.3\n  resolution: \"ms@npm:2.1.3\"\n"}
	base := map[string]string{"package.json": `{"dependencies": {"ms": "^2.1.3"}}`,
		"yarn.lock": "# yarn lockfile v1\n\nms@^2.1.3:\n  version \"2.1.3\"\n  resolved \"https://registry.yarnpkg.com/ms/-/ms-2.1.3.tgz\"\n"}
	// Moving from yarn 1 to yarn 4 with the same packages is no change.
	if res := compare(t, head, base, nil, nil); len(res.Changes) != 0 {
		t.Errorf("yarn 1 to berry with the same packages: %+v", res.Changes)
	}
}

func TestUVLock(t *testing.T) {
	lock := `version = 1
revision = 2
requires-python = ">=3.10"
resolution-markers = [
    "python_full_version >= '3.12'",
    "python_full_version < '3.12'",
]

[manifest]
members = ["app", "app-lib"]

[[package]]
name = "app"
version = "0.1.0"
source = { editable = "." }
dependencies = [
    { name = "app-lib" },
    { name = "httpx" },
]

[package.optional-dependencies]
fast = [
    { name = "orjson" },
]

[package.dev-dependencies]
dev = [
    { name = "pytest" },
]

[package.metadata]
requires-dist = [{ name = "httpx", specifier = ">=0.27" }]

[[package]]
name = "app-lib"
version = "0.1.0"
source = { virtual = "packages/lib" }
dependencies = [
    { name = "Typing_Extensions" },
]

[[package]]
name = "httpx"
version = "0.28.1"
source = { registry = "https://pypi.org/simple" }
dependencies = [
    { name = "anyio" },
]
sdist = { url = "https://files.pythonhosted.org/packages/httpx-0.28.1.tar.gz", hash = "sha256:aa", size = 141406, upload-time = "2024-12-06T15:37:23.222Z" }
wheels = [
    { url = "https://files.pythonhosted.org/packages/httpx-0.28.1-py3-none-any.whl", hash = "sha256:bb", size = 73517, upload-time = "2024-12-06T15:37:21.509Z" },
]

[[package]]
name = "anyio"
version = "4.6.0"
source = { registry = "https://pypi.org/simple/" }
wheels = [
    { url = "https://files.pythonhosted.org/packages/anyio-4.6.0-py3-none-any.whl", hash = "sha256:cc", size = 1 },
]

[[package]]
name = "orjson"
version = "3.10.0"
source = { registry = "https://pypi.org/simple" }
sdist = { url = "https://files.pythonhosted.org/packages/orjson-3.10.0.tar.gz", hash = "sha256:dd", size = 1 }

[[package]]
name = "pytest"
version = "8.3.0"
source = { registry = "https://mirror.example.net/simple" }
wheels = [
    { url = "https://mirror.example.net/pytest-8.3.0-py3-none-any.whl", hash = "sha256:ee", size = 1 },
]

[[package]]
name = "typing-extensions"
version = "4.12.2"
source = { git = "https://github.com/python/typing_extensions?rev=4.12.2#abc" }

[[package]]
name = "pinned-wheel"
version = "1.0.0"
source = { url = "https://files.example.net/pinned_wheel-1.0.0-py3-none-any.whl" }

[[package]]
name = "local-sdist"
version = "1.0.0"
source = { path = "dist/local_sdist-1.0.0.tar.gz" }
`
	m := pkgMap(t)(parseUVLock([]byte(lock)))
	if len(m) != 7 {
		t.Errorf("%d packages, want 7 (the two workspace members left out): %v", len(m), m)
	}
	if p := m["httpx@0.28.1"]; !p.direct || !p.registry || p.scripts != scriptsNo || p.eco != EcoPyPI {
		t.Errorf("httpx: %+v", p)
	}
	if p := m["anyio@4.6.0"]; p.direct || !p.registry {
		t.Errorf("anyio (transitive, index with a trailing slash): %+v", p)
	}
	if p := m["orjson@3.10.0"]; !p.direct || p.scripts != scriptsYes {
		t.Errorf("orjson (optional, sdist only): %+v", p)
	}
	if p := m["pytest@8.3.0"]; !p.direct || p.registry {
		t.Errorf("pytest (dev group, another index): %+v", p)
	}
	if p := m["typing-extensions@4.12.2"]; !p.direct || p.registry || p.scripts != scriptsYes {
		t.Errorf("typing-extensions (a member's dependency, from git, name normalised): %+v", p)
	}
	if p := m["pinned-wheel@1.0.0"]; p.scripts != scriptsNo || p.registry {
		t.Errorf("a wheel by URL: %+v", p)
	}
	if p := m["local-sdist@1.0.0"]; p.scripts != scriptsYes || p.registry {
		t.Errorf("a source archive by path: %+v", p)
	}

	_, err := parseUVLock([]byte("[[package]]\nname = \"a\"\nversion = \"1\"\nsource = { registry = \"https://pypi.org/simple\" }\n"))
	failsWith(t, "no version line", err, "version")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1\"\nsource = { svn = \"x\" }\n"))
	failsWith(t, "an unknown source", err, "svn")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\"\nname = \"b\"\nversion = \"1\"\nsource = { registry = \"https://pypi.org/simple\" }\n"))
	failsWith(t, "a key given twice", err, "")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1\"\nsource = { registry = \"https://pypi.org/simple\" }\nwheels = [\n    { url = \"x.whl\" },\n    stray words\n]\n"))
	failsWith(t, "a stray line in an array", err, "")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1\"\n"))
	failsWith(t, "no source", err, "source")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1\"\nsource = { registry = \"https://pypi.org/simple\" \n"))
	failsWith(t, "an inline table never closed", err, "")
	_, err = parseUVLock([]byte("version = 1\n\n[[package]]\nname = \"a\nversion = \"1\"\n"))
	failsWith(t, "a string never closed", err, "")
}

// TestNewLockfilesInTheGate runs the new formats through CompareDeps: the
// statuses a change gets, and that a move off the registry in each is
// not-checked.
func TestNewLockfilesInTheGate(t *testing.T) {
	for _, kind := range []string{"pnpm9", "pnpm6", "berry", "yarn1", "uv"} {
		base := genLock(kind, []string{"a@1.0.0", "b@2.0.0"})
		head := genLock(kind, []string{"a@1.1.0", "c@3.0.0"})
		eco := genEco[kind]
		res := compare(t, head, base, allowPolicy(DepAllowRule{Package: eco + "/a", Reason: "x"}), nil)
		got := map[string]string{}
		for _, c := range res.Changes {
			got[c.Package] = c.Change + " " + c.Status
		}
		want := map[string]string{eco + "/a": "updated " + DepUpdated, eco + "/b": "removed " + DepRemoved, eco + "/c": "added " + DepNotAllowed}
		for k, v := range want {
			if !strings.HasPrefix(got[k], strings.Fields(v)[0]) || !strings.HasSuffix(got[k], strings.Fields(v)[1]) {
				t.Errorf("%s %s: got %q, want %q (all: %v)", kind, k, got[k], v, got)
			}
		}
		for _, c := range res.Checks {
			if c.Status == "not-run" && c.Name == "allow-list" {
				t.Errorf("%s: allow list not run", kind)
			}
		}
	}
	moved := map[string][2]map[string]string{
		"pnpm": {{"pnpm-lock.yaml": "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      a:\n        specifier: ^1\n        version: 1.0.1\npackages:\n  a@1.0.1:\n    resolution: {tarball: https://pkgs.example.net/a-1.0.1.tgz}\n"},
			{"pnpm-lock.yaml": "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      a:\n        specifier: ^1\n        version: 1.0.0\npackages:\n  a@1.0.0:\n    resolution: {integrity: sha512-A}\n"}},
		"yarn1": {{"yarn.lock": "a@^1:\n  version \"1.0.1\"\n  resolved \"https://pkgs.example.net/a-1.0.1.tgz\"\n"},
			{"yarn.lock": "a@^1:\n  version \"1.0.0\"\n  resolved \"https://registry.yarnpkg.com/a/-/a-1.0.0.tgz\"\n"}},
		"uv": {{"uv.lock": "version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1.0.1\"\nsource = { registry = \"https://mirror.example.net/simple\" }\n"},
			{"uv.lock": "version = 1\n\n[[package]]\nname = \"a\"\nversion = \"1.0.0\"\nsource = { registry = \"https://pypi.org/simple\" }\nwheels = [{ url = \"a.whl\" }]\n"}},
	}
	for name, hb := range moved {
		res := compare(t, hb[0], hb[1], nil, nil)
		if len(res.Changes) != 1 || res.Changes[0].Status != DepNotChecked {
			t.Errorf("%s update off the registry: %+v", name, res.Changes)
		}
	}
}

// TestSwitchPackageManager: moving from one lockfile kind to another that
// pins the same manifest is read through the new lockfile, not reported
// as a deleted lockfile; deleting it with nothing in its place still is.
func TestSwitchPackageManager(t *testing.T) {
	pj := `{"dependencies": {"a": "^1.0.0"}}`
	yarn := genLock("yarn1", []string{"a@1.0.0"})["yarn.lock"]
	pnpm := genLock("pnpm9", []string{"a@1.0.0"})["pnpm-lock.yaml"]
	res := compare(t, map[string]string{"package.json": pj, "pnpm-lock.yaml": pnpm}, map[string]string{"package.json": pj, "yarn.lock": yarn}, nil, nil)
	if len(res.Changes) != 0 {
		t.Errorf("yarn to pnpm, same packages: %+v", res.Changes)
	}
	res = compare(t, map[string]string{"package.json": pj}, map[string]string{"package.json": pj, "yarn.lock": yarn}, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) != 1 || DepFailures(res, DefaultDepFailOn)[0].Status != DepNotChecked {
		t.Errorf("yarn.lock deleted, package.json kept: %+v", res.Changes)
	}
	py := "[project]\nname = \"app\"\ndependencies = [\"a\"]\n"
	poetry := genLock("poetry", []string{"a@1.0.0"})["poetry.lock"]
	uv := genLock("uv", []string{"a@1.0.0"})["uv.lock"]
	res = compare(t, map[string]string{"pyproject.toml": py, "uv.lock": uv}, map[string]string{"pyproject.toml": py, "poetry.lock": poetry}, nil, nil)
	if len(res.Changes) != 0 {
		t.Errorf("poetry to uv, same packages: %+v", res.Changes)
	}
}
