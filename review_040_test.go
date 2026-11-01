package remixware

import (
	"strings"
	"testing"
)

// Independent review of 0.4.0 (pnpm, yarn and uv readers, the unreadable
// lockfile skip). Each test fails on the code as reviewed and shows one
// defect; run with go test -run Review040 ./...

func review040Has(t *testing.T, pkgs []lockPackage, err error, name, version string) (lockPackage, bool) {
	t.Helper()
	if err != nil {
		return lockPackage{}, false
	}
	for _, p := range pkgs {
		if p.name == name && p.version == version {
			return p, true
		}
	}
	return lockPackage{}, false
}

// yarn 1 reads every descriptor of an entry header as a key for the same
// entry and installs from its resolved URL. One extra file: descriptor makes
// Remixware drop the whole entry, so a new registry package goes unseen.
func TestReview040YarnV1FileDescriptorHidesPackage(t *testing.T) {
	base := genLock("yarn1", []string{"a@1.0.0"})
	head := map[string]string{"yarn.lock": base["yarn.lock"] +
		"\"evil@^1.0.0\", \"evil@file:./nothing\":\n  version \"1.0.0\"\n" +
		"  resolved \"https://registry.yarnpkg.com/evil/-/evil-1.0.0.tgz#aa\"\n  integrity sha512-EEEE\n"}
	base["package.json"] = `{"dependencies": {"a": "^1.0.0"}}`
	head["package.json"] = `{"dependencies": {"a": "^1.0.0", "evil": "^1.0.0"}}`
	pkgs, err := parseYarnV1([]byte(head["yarn.lock"]), []byte(head["package.json"]))
	if _, ok := review040Has(t, pkgs, err, "evil", "1.0.0"); !ok && err == nil {
		t.Errorf("evil@1.0.0 (resolved from the registry) is missing from the packages read: %+v", pkgs)
	}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("a new package not on the allow list passes the gate: %+v", res.Changes)
	}
}

// A YAML line "--- <content>" starts a document whose content is on that
// line. pnpm takes everything after the first "\n---\n" as the main
// document and parses it with js-yaml, so a main document written as
// "--- {flow mapping}" is what pnpm installs; Remixware drops the line.
func TestReview040YAMLDocumentStartLineDropped(t *testing.T) {
	lock := "---\nlockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    configDependencies: {}\n\n" +
		"---\n" +
		"--- {lockfileVersion: '9.0', importers: {'.': {dependencies: {evil: {specifier: 1.0.0, version: 1.0.0}}}}, " +
		"packages: {'evil@1.0.0': {resolution: {integrity: sha512-EEEE}}}, snapshots: {'evil@1.0.0': {}}}\n"
	pkgs, err := parsePnpmLock([]byte(lock))
	if _, ok := review040Has(t, pkgs, err, "evil", "1.0.0"); !ok && err == nil {
		t.Errorf("the document on the --- line was dropped without an error; packages read: %+v", pkgs)
	}
	base := map[string]string{"pnpm-lock.yaml": "---\nlockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    configDependencies: {}\n\n---\nlockfileVersion: '9.0'\nimporters:\n  .: {}\n"}
	res, cerr := CompareDeps(tree(t, map[string]string{"pnpm-lock.yaml": lock}), tree(t, base), DepsOptions{})
	if cerr == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("the hidden package passes the gate: %+v", res.Changes)
	}
}

// js-yaml reads "requiresBuild : true" as the key requiresBuild (a plain
// scalar ends before trailing spaces); Remixware reads the key
// "requiresBuild " and so no install script.
func TestReview040PnpmSpaceBeforeColon(t *testing.T) {
	lock := "lockfileVersion: '6.0'\n\ndependencies:\n  esbuild:\n    specifier: ^0.19.2\n    version: 0.19.2\n\npackages:\n\n" +
		"  /esbuild@0.19.2:\n    resolution: {integrity: sha512-AAAA}\n    requiresBuild : true\n    dev: false\n"
	pkgs, err := parsePnpmLock([]byte(lock))
	if p, ok := review040Has(t, pkgs, err, "esbuild", "0.19.2"); ok && p.scripts != scriptsYes {
		t.Errorf("requiresBuild : true read as scripts=%d, want scriptsYes (or an error)", p.scripts)
	}
	base := genLock("pnpm6", []string{"a@1.0.0"})
	head := map[string]string{"pnpm-lock.yaml": lock}
	res, cerr := CompareDeps(tree(t, head), tree(t, base), DepsOptions{Policy: allowPolicy(DepAllowRule{Package: "npm/esbuild", Reason: "build tool"})})
	if cerr == nil {
		for _, c := range res.Changes {
			if c.Name == "esbuild" && c.Status != DepInstallScript {
				t.Errorf("esbuild, recorded with requiresBuild, has status %s", c.Status)
			}
		}
	}
}

// js-yaml (pnpm's reader) takes True and TRUE as the boolean true.
func TestReview040PnpmRequiresBuildCapitalTrue(t *testing.T) {
	for _, v := range []string{"True", "TRUE"} {
		lock := "lockfileVersion: '6.0'\n\npackages:\n\n  /esbuild@0.19.2:\n    resolution: {integrity: sha512-AAAA}\n    requiresBuild: " + v + "\n"
		pkgs, err := parsePnpmLock([]byte(lock))
		if p, ok := review040Has(t, pkgs, err, "esbuild", "0.19.2"); ok && p.scripts != scriptsYes {
			t.Errorf("requiresBuild: %s read as scripts=%d, want scriptsYes (or an error)", v, p.scripts)
		}
	}
}

// pnpm names a packages entry by its key (nameVerFromPkgSnapshot parses
// the dep path and ignores the name field) and builds the registry tarball
// URL from that name. Remixware takes the name field instead, so an entry
// keyed evil@4.17.21 with "name: lodash" reads as the unchanged lodash.
func TestReview040PnpmNameFieldOverridesKey(t *testing.T) {
	base := map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      lodash:\n        specifier: ^4.17.21\n        version: 4.17.21\n" +
		"packages:\n  lodash@4.17.21:\n    resolution: {integrity: sha512-LLLL}\nsnapshots:\n  lodash@4.17.21: {}\n"}
	head := map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      lodash:\n        specifier: ^4.17.21\n        version: evil@4.17.21\n" +
		"packages:\n  evil@4.17.21:\n    resolution: {integrity: sha512-EEEE}\n    name: lodash\nsnapshots:\n  evil@4.17.21: {}\n"}
	pkgs, err := parsePnpmLock([]byte(head["pnpm-lock.yaml"]))
	if _, ok := review040Has(t, pkgs, err, "evil", "4.17.21"); !ok && err == nil {
		t.Errorf("the entry keyed evil@4.17.21 isn't read as evil: %+v", pkgs)
	}
	res, cerr := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if cerr == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("the registry package evil, installed in lodash's place, passes: %+v", res.Changes)
	}
}

// pnpm hands any resolution with a type (binary, variations, ...) to that
// type's fetcher as it is; it isn't a registry download. Remixware counts
// any unknown-typed resolution with an integrity as the public registry, so
// an update that moves a package to a URL isn't not-checked.
func TestReview040PnpmTypedResolutionCountedAsRegistry(t *testing.T) {
	lock := "lockfileVersion: '9.0'\npackages:\n  a@1.0.1:\n    resolution: {type: binary, archive: tarball, bin: a, url: 'https://evil.example/a.tgz', integrity: sha512-XXXX}\n"
	pkgs, err := parsePnpmLock([]byte(lock))
	if p, ok := review040Has(t, pkgs, err, "a", "1.0.1"); ok && p.registry {
		t.Errorf("a binary resolution from evil.example counted as the npm registry: %+v", p)
	}
	base := map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\npackages:\n  a@1.0.0:\n    resolution: {integrity: sha512-AAAA}\n"}
	res, cerr := CompareDeps(tree(t, map[string]string{"pnpm-lock.yaml": lock}), tree(t, base), DepsOptions{})
	if cerr == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("an update moving off the registry passes: %+v", res.Changes)
	}
}

// yarn berry's npm: resolutions can carry __archiveUrl; NpmSemverFetcher
// refuses those and another fetcher downloads the URL. Remixware counts
// every npm: resolution as the public registry.
func TestReview040BerryArchiveURLCountedAsRegistry(t *testing.T) {
	head := "__metadata:\n  version: 8\n  cacheKey: 10c0\n\n\"left-pad@npm:^1.3.0\":\n  version: 1.3.1\n" +
		"  resolution: \"left-pad@npm:1.3.1::__archiveUrl=https%3A%2F%2Fevil.example%2Fleft-pad-1.3.1.tgz\"\n  checksum: 10c0/bbbb\n  languageName: node\n  linkType: hard\n"
	pkgs, err := parseYarnBerry([]byte(head))
	if p, ok := review040Has(t, pkgs, err, "left-pad", "1.3.1"); ok && p.registry {
		t.Errorf("an npm: resolution with __archiveUrl=evil.example counted as the registry: %+v", p)
	}
	res, cerr := CompareDeps(tree(t, map[string]string{"yarn.lock": head}), tree(t, genLock("berry", []string{"left-pad@1.3.0"})), DepsOptions{})
	if cerr == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("an update moving to an archive URL passes: %+v", res.Changes)
	}
}

// yarn berry fetches the locator in resolution; the version field is the
// manifest's. Remixware takes the version field, so a resolution that
// moves to a new release while version stays put reads as no change, and
// the new release never reaches the online install-script or publisher
// checks.
func TestReview040BerryVersionFieldOverResolution(t *testing.T) {
	base := genLock("berry", []string{"lodash@4.17.21"})
	head := map[string]string{"yarn.lock": strings.Replace(base["yarn.lock"], `resolution: "lodash@npm:4.17.21"`, `resolution: "lodash@npm:4.17.22"`, 1)}
	pkgs, err := parseYarnBerry([]byte(head["yarn.lock"]))
	if _, ok := review040Has(t, pkgs, err, "lodash", "4.17.22"); !ok && err == nil {
		t.Errorf("resolution lodash@npm:4.17.22 read as another version: %+v", pkgs)
	}
	res, cerr := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if cerr == nil && len(res.Changes) == 0 {
		t.Errorf("lodash 4.17.21 -> 4.17.22 (by resolution) isn't reported as an update")
	}
}

// uv installs registry packages from the file URLs in uv.lock. A package
// whose source says pypi.org but whose wheel and sdist URLs are elsewhere
// counts as a registry package, so an update to it isn't not-checked.
func TestReview040UVFileURLsOffRegistry(t *testing.T) {
	base := genLock("uv", []string{"requests@2.32.3"})
	head := map[string]string{"uv.lock": strings.NewReplacer(
		"https://files.pythonhosted.org/requests-2.32.3", "https://evil.example/requests-2.32.4",
		`version = "2.32.3"`, `version = "2.32.4"`).Replace(base["uv.lock"])}
	pkgs, err := parseUVLock([]byte(head["uv.lock"]))
	if p, ok := review040Has(t, pkgs, err, "requests", "2.32.4"); ok && p.registry {
		t.Errorf("a wheel from evil.example counted as a PyPI package: %+v", p)
	}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("an update whose files come from evil.example passes: %+v", res.Changes)
	}
}

// yarn 1 and pnpm download the URL in the lockfile. A URL on the registry
// host for another package's tarball is counted as the named package from
// the registry.
func TestReview040RegistryURLForAnotherPackage(t *testing.T) {
	y := "lodash@^4.17.21:\n  version \"4.17.22\"\n  resolved \"https://registry.yarnpkg.com/evil/-/evil-1.0.0.tgz#aa\"\n  integrity sha512-EEEE\n"
	pkgs, err := parseYarnV1([]byte(y), nil)
	if p, ok := review040Has(t, pkgs, err, "lodash", "4.17.22"); ok && p.registry {
		t.Errorf("yarn 1: lodash resolved to evil's tarball counted as lodash from the registry: %+v", p)
	}
	pn := "lockfileVersion: '9.0'\npackages:\n  lodash@4.17.22:\n    resolution: {integrity: sha512-EEEE, tarball: 'https://registry.npmjs.org/evil/-/evil-1.0.0.tgz'}\n"
	pkgs, err = parsePnpmLock([]byte(pn))
	if p, ok := review040Has(t, pkgs, err, "lodash", "4.17.22"); ok && p.registry {
		t.Errorf("pnpm: lodash with evil's tarball counted as lodash from the registry: %+v", p)
	}
}

// 0.4.0 widened hasSiblingLockfile from "same kind" to "same manifest". A
// stale yarn.lock that the change doesn't touch now stops a deleted
// package-lock.json from being not-checked, though npm never reads
// yarn.lock: the next npm install picks whatever package.json allows.
func TestReview040StaleSiblingHidesDeletedLockfile(t *testing.T) {
	yarn := genLock("yarn1", []string{"a@1.0.0"})["yarn.lock"]
	base := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0"}}`, "package-lock.json": npmLock("a@1.0.0"), "yarn.lock": yarn}
	head := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0", "evil": "*"}}`, "yarn.lock": yarn}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("package-lock.json deleted, package.json kept and changed, an unchanged yarn.lock beside it: passes (%+v)", res.Changes)
	}
	// Worse when the sibling is one the gate skipped as unreadable.
	pnpmFixture := "lockfileVersion: '9.0'\npackages:\n  a@1.0.0: &x\n    resolution: {integrity: sha512-A}\n"
	base = map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0"}}`, "package-lock.json": npmLock("a@1.0.0"), "pnpm-lock.yaml": pnpmFixture}
	head = map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0"}}`, "pnpm-lock.yaml": pnpmFixture}
	res = compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("package-lock.json deleted; the only other lockfile was skipped as unreadable; passes (%+v)", res.Changes)
	}
}

// pnpm 8 (lockfile 6.0) keys git and tarball packages by their source
// (github.com/owner/repo/<commit>, '@host/path.tgz'), with name and version
// fields; lockfile 5.4 uses the same keys. The v6 reader stops on them, so
// any pnpm 8 project with a git dependency can't use the gate.
func TestReview040PnpmV6GitKey(t *testing.T) {
	lock := "lockfileVersion: '6.0'\n\ndependencies:\n  colors:\n    specifier: github:Marak/colors.js#6bc50e79eeaa1d87369bb3e7e608ebed18c5cf26\n" +
		"    version: github.com/Marak/colors.js/6bc50e79eeaa1d87369bb3e7e608ebed18c5cf26\n\npackages:\n\n" +
		"  github.com/Marak/colors.js/6bc50e79eeaa1d87369bb3e7e608ebed18c5cf26:\n" +
		"    resolution: {tarball: https://codeload.github.com/Marak/colors.js/tar.gz/6bc50e79eeaa1d87369bb3e7e608ebed18c5cf26}\n" +
		"    name: colors\n    version: 1.4.44-liberty-2\n    engines: {node: '>=0.1.90'}\n    dev: false\n"
	pkgs, err := parsePnpmLock([]byte(lock))
	if err != nil {
		t.Fatalf("a pnpm 8 lockfile with a git dependency can't be read: %v", err)
	}
	if p, ok := review040Has(t, pkgs, err, "colors", "1.4.44-liberty-2"); !ok || p.registry {
		t.Errorf("git package: %+v", pkgs)
	}
}

// The TOML reader's checks against extending an inline table or an inline
// array (r.defined["inline:..."]) are never armed: nothing sets those
// keys. uv's TOML parser refuses both; Remixware reads them, so the
// "strict" reader takes in files uv won't (low severity: uv then fails).
func TestReview040TOMLInlineValuesExtended(t *testing.T) {
	for name, doc := range map[string]string{
		"[t] after t = {...}":            "t = { a = 1 }\n[t]\nb = 2\n",
		"[[p]] after p = [...]":          "p = [{ a = 1 }]\n[[p]]\na = 2\n",
		"[p.source] after inline source": "[[p]]\nsource = { registry = \"https://pypi.org/simple\" }\n[p.source]\ngit = \"https://evil.example/x\"\n",
	} {
		if _, err := parseTOML([]byte(doc)); err == nil {
			t.Errorf("%s: read without an error", name)
		}
	}
}
