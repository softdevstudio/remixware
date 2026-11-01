package remixware

import (
	"testing"
)

// Re-check of the 0.4.0 review fixes (14c2f13). Each test fails on that
// commit; run with go test -run Review040b ./...

// js-yaml ends a plain scalar before trailing white space, tabs included,
// so "requiresBuild\t: true" is the key requiresBuild. The fix trims only
// spaces, so the key stays "requiresBuild\t" and lockfiles 5 and 6 read
// no install script.
func TestReview040bTabBeforeColon(t *testing.T) {
	for name, line := range map[string]string{
		"key\\t: value":  "    requiresBuild\t: true\n",
		"key \\t: value": "    requiresBuild \t: true\n",
	} {
		lock := "lockfileVersion: '6.0'\n\npackages:\n\n  /esbuild@0.19.2:\n    resolution: {integrity: sha512-AAAA}\n" + line
		pkgs, err := parsePnpmLock([]byte(lock))
		if p, ok := review040Has(t, pkgs, err, "esbuild", "0.19.2"); ok && p.scripts != scriptsYes {
			t.Errorf("%s: requiresBuild read as scripts=%d, want scriptsYes (or an error)", name, p.scripts)
		}
	}
}

// pnpm names every packages entry by its key (pnpm 11's
// nameVerFromPkgSnapshot never reads the name field). For a lockfile 9
// key that isn't name@semver (name@https://..., name@git+...), the fix
// takes the name field when there is one, so evil@<url> with name: lodash
// reads as lodash. At the version main has, that's no change at all.
func TestReview040bPnpm9URLKeyNameField(t *testing.T) {
	base := map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\npackages:\n  lodash@4.17.21:\n    resolution: {integrity: sha512-LLLL}\n"}
	head := map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\npackages:\n" +
		"  evil@https://evil.example/evil-1.0.0.tgz:\n    resolution: {tarball: 'https://evil.example/evil-1.0.0.tgz'}\n    name: lodash\n    version: 4.17.21\n"}
	pkgs, err := parsePnpmLock([]byte(head["pnpm-lock.yaml"]))
	if _, ok := review040Has(t, pkgs, err, "lodash", "4.17.21"); ok {
		t.Errorf("the entry keyed evil@<url> is read as lodash (pnpm names it evil): %+v", pkgs)
	}
	res, cerr := CompareDeps(tree(t, head), tree(t, base), DepsOptions{})
	if cerr == nil && len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("evil from evil.example, named lodash by a field, passes: %+v", res.Changes)
	}
}

// The new sibling rule counts a sibling lockfile the change "adds or
// edits". Editing a stale yarn.lock by one comment line is enough: the
// change then deletes package-lock.json, adds a package to package.json,
// and passes, though npm (which made package-lock.json) never reads
// yarn.lock.
func TestReview040bSiblingEditedByComment(t *testing.T) {
	yarn := genLock("yarn1", []string{"a@1.0.0"})["yarn.lock"]
	base := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0"}}`, "package-lock.json": npmLock("a@1.0.0"), "yarn.lock": yarn}
	head := map[string]string{"package.json": `{"dependencies": {"a": "^1.0.0", "evil": "*"}}`, "yarn.lock": yarn + "# touched\n"}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("package-lock.json deleted and package.json changed, a stale yarn.lock edited by a comment: passes (%+v)", res.Changes)
	}
}
