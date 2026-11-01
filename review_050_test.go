package remixware

// Independent review of 0.5.0 (licence misreads, Cargo.lock, build
// surface). Each test fails on the code as reviewed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cargoLockWith(extra string) string {
	return `version = 4

[[package]]
name = "app"
version = "0.1.0"
dependencies = [
 "forked",
]
` + extra
}

// A Cargo.lock change that moves a git crate to another commit at the same
// version, or patches a crates.io crate to a git fork at the same version
// ([patch.crates-io] keeps the version), changes the code that's built but
// the gate reports nothing.
func TestReview050CargoGitCommitChangeAtSameVersion(t *testing.T) {
	base := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "0.3.0"
source = "git+https://github.com/example/forked?branch=main#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`)}
	head := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "0.3.0"
source = "git+https://github.com/attacker/forked?branch=main#bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
`)}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("a git crate moved to another repository and commit passes: %+v", res.Changes)
	}
}

func TestReview050CargoPatchedToGitAtSameVersion(t *testing.T) {
	base := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "1.0.210"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "aa"
`)}
	head := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "1.0.210"
source = "git+https://github.com/someone/serde-fork#cccccccccccccccccccccccccccccccccccccccc"
`)}
	res := compare(t, head, base, nil, nil)
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("crates.io crate replaced by a git fork at the same version passes: %+v", res.Changes)
	}
}

// An update of a git crate (new version, new commit) passes with no allow
// rule and no install-script check: a git crate's build.rs is never looked
// at. pip's VCS requirements count as install-script for this reason
// (0.3.1); Cargo's don't.
func TestReview050CargoGitUpdatePassesUnchecked(t *testing.T) {
	base := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "0.3.0"
source = "git+https://github.com/example/forked?branch=main#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`)}
	head := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "0.4.0"
source = "git+https://github.com/example/forked?branch=main#bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
`)}
	f := newFakeRegistry(t)
	res := compare(t, head, base, nil, f.registry(""))
	if len(DepFailures(res, DefaultDepFailOn)) == 0 {
		t.Errorf("a git crate update passes with --online, its build script never checked: %+v", res.Changes)
	}
}

func crateRuns(t *testing.T, files map[string]string) bool {
	t.Helper()
	f := newFakeRegistry(t)
	f.raw["/crates/rv/rv-1.0.0.crate"] = crateFile(t, "rv", "1.0.0", files)
	facts, _, err := f.registry("").Facts(EcoCargo, "rv", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return facts.InstallScripts
}

// crateManifestBuild reads `key = value` lines literally: valid TOML that
// Cargo reads as a proc-macro or a build script is missed.
func TestReview050CrateManifestTOMLForms(t *testing.T) {
	pkg := "[package]\nname = \"rv\"\nversion = \"1.0.0\"\n"
	for name, manifest := range map[string]string{
		"trailing comment": pkg + "\n[lib]\nproc-macro = true # derive\n",
		// Top-level keys: under [package] they'd be package.lib, which Cargo
		// ignores as an unused key.
		"inline lib table": "lib = { proc-macro = true }\n" + pkg,
		"dotted key":       "lib.proc-macro = true\n" + pkg,
		"dotted build key": "package.name = \"rv\"\npackage.version = \"1.0.0\"\npackage.build = \"gen.rs\"\n",
	} {
		if !crateRuns(t, map[string]string{"Cargo.toml": manifest, "src/lib.rs": ""}) {
			t.Errorf("%s: compiled code at build time not seen", name)
		}
	}
}

// Cargo.toml is read only up to 1 MB: a [lib] proc-macro = true after
// that is never seen (crates.io takes crates up to 10 MB).
func TestReview050CrateManifestPastReadCap(t *testing.T) {
	manifest := "[package]\nname = \"rv\"\nversion = \"1.0.0\"\n" + strings.Repeat("# padding padding padding\n", 45000) +
		"\n[lib]\nproc-macro = true\n"
	if !crateRuns(t, map[string]string{"Cargo.toml": manifest}) {
		t.Error("a proc-macro declared past the first MB of Cargo.toml reads as clean")
	}
}

func goNative(t *testing.T, files map[string]string) bool {
	t.Helper()
	f := newFakeRegistry(t)
	mod := "example.com/rv"
	f.raw["/goproxy/"+mod+"/@v/v1.0.0.zip"] = moduleZip(t, mod, "v1.0.0", files)
	facts, _, err := f.registry("").Facts(EcoGo, mod, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return facts.NativeCode
}

// A Go file over 1 MB is cut before parsing; when the package clause or
// the import "C" falls past the cut, the parse fails and the file is
// skipped as one "Go wouldn't build".
func TestReview050GoCgoPastReadCap(t *testing.T) {
	src := "// Code generated by a table generator. DO NOT EDIT.\n" + strings.Repeat("// data data data data data data data\n", 30000) +
		"\npackage x\n\n// #include <stdio.h>\nimport \"C\"\n"
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "big.go": src}) {
		t.Error("cgo in a Go file larger than 1 MB is missed")
	}
}

// Go reads GOOS/GOARCH from a file name only up to its first dot
// (go/build goodOSArchFile: strings.Cut(name, ".")); Remixware drops only
// the last extension, so cgo_linux.x_aix.go, which Go builds on Linux,
// reads as AIX-only.
func TestReview050GoFileNameFirstDot(t *testing.T) {
	cgo := "package x\n\nimport \"C\"\n"
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "cgo_linux.x_aix.go": cgo}) {
		t.Error("a file Go builds on Linux (its name's GOOS is before the first dot) reads as AIX-only")
	}
}

// Since Go 1.17 a //go:build line wins and +build lines are ignored;
// Remixware ANDs them, so a stale +build line hides cgo Go builds.
func TestReview050GoBuildLineWinsOverPlusBuild(t *testing.T) {
	src := "//go:build linux\n// +build aix\n\npackage x\n\nimport \"C\"\n"
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "c.go": src}) {
		t.Error("a //go:build linux file with a stale // +build aix line reads as not built")
	}
}

// A //go:build line inside a /* */ block isn't a constraint to Go; it is
// to Remixware.
func TestReview050GoBuildInBlockComment(t *testing.T) {
	src := "/*\n//go:build aix\n*/\n\npackage x\n\nimport \"C\"\n"
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "c.go": src}) {
		t.Error("a //go:build line inside a block comment hides cgo")
	}
}

// go build runs SWIG and the C/C++ compiler for .swig and .swigcxx files
// in a package; those aren't looked at.
func TestReview050GoSwigFiles(t *testing.T) {
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "x.go": "package x\n", "x.swigcxx": "%module x\n"}) {
		t.Error("a package with a .swigcxx file (SWIG plus a C++ build) reads as no native code")
	}
}

func licenceDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for n, body := range files {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	got, _, _, _ := licenseFromFiles(paths)
	return got
}

// Common dual-licence wordings that say "or" without "either", "at your
// option" or "dual": 0.4 read LICENSE-MIT + LICENSE-APACHE as a choice;
// 0.5 reads them as AND because the short LICENSE names both files.
func TestReview050DualLicencePlainOr(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	mit, apache := read("licensetexts/MIT.txt"), read("licensetexts/Apache-2.0.txt")
	for _, w := range []string{
		"This project is licensed under the MIT license (LICENSE-MIT) or the Apache License 2.0 (LICENSE-APACHE).\n",
		"You may use this crate under the terms of LICENSE-MIT or LICENSE-APACHE, whichever you prefer.\n",
		"Choose the license that suits you: LICENSE-MIT or LICENSE-APACHE.\n",
	} {
		got := licenceDir(t, map[string]string{"LICENSE-MIT": mit, "LICENSE-APACHE": apache, "LICENSE": w})
		if got != "Apache-2.0 OR MIT" {
			t.Errorf("%q: %q, want Apache-2.0 OR MIT", strings.TrimSpace(w), got)
		}
	}
}

// The reverse: a short file that says the parts differ but happens to use
// a choice phrase ("either licence") about something else is read as a
// choice, so the code can be taken under the licence that asks less.
func TestReview050PartsDifferWithChoicePhrase(t *testing.T) {
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return string(b)
	}
	mit, gpl := read("licensetexts/MIT.txt"), read("testdata/licences/GPL-2.0-only.txt")
	w := "The library in src/ is under LICENSE-MIT. The command-line tool in cli/ is under LICENSE-GPL. " +
		"Keep the text of either licence with the part you copy.\n"
	got := licenceDir(t, map[string]string{"LICENSE-MIT": mit, "LICENSE-GPL": gpl, "LICENSE": w})
	if strings.Contains(got, " OR ") {
		t.Errorf("parts under different licences read as a choice: %q", got)
	}
}

// addsTerms looks at the whole text, copyright lines included: a plain
// MIT licence from an organisation with "Military" in its name, or a game
// studio with "Weapons", is no longer MIT.
func TestReview050AddsTermsInCopyrightHolder(t *testing.T) {
	b, err := os.ReadFile("licensetexts/MIT.txt")
	if err != nil {
		t.Fatal(err)
	}
	mit := strings.Replace(string(b), "Copyright (c) <year> <copyright holders>", "Copyright (c) 2024 Military Health Informatics Lab", 1)
	if !strings.Contains(mit, "Military") {
		mit = "Copyright (c) 2024 Military Health Informatics Lab\n\n" + string(b)
	}
	if got := DetectLicense(mit); got != "MIT" {
		t.Errorf("MIT with \"Military\" in the copyright holder: %q", got)
	}
	bsd, _ := os.ReadFile("licensetexts/BSD-3-Clause.txt")
	text := "Copyright (c) 2023, Nuclear Physics Software Group\n\n" + string(bsd) +
		"\nThis software models weapons ballistics for teaching.\n"
	if got := DetectLicense(text); got != "BSD-3-Clause" {
		t.Errorf("BSD-3 with an unrelated mention of weapons: %q", got)
	}
}

// The texts 0.5 now reads as LicenseRef-Unrecognised (BSD No-Military and
// No-Nuclear, TOSL, TMate, TU-Berlin-2.0) were BSD/0BSD-family before, and
// a copy without its notice was attribution-missing, failing check by
// default. LicenseRef-Unrecognised is review, needsNotice says no for it,
// and review isn't in check's default fail_on: the same copy, notice
// dropped, now passes.
func TestReview050UnrecognisedCopyWithoutNoticePasses(t *testing.T) {
	for _, f := range []string{"BSD-3-Clause-No-Military-License.txt", "TOSL.txt", "TU-Berlin-2.0.txt"} {
		b, err := os.ReadFile(filepath.Join("testdata/licences", f))
		if err != nil {
			t.Fatal(err)
		}
		id := DetectLicense(string(b))
		dir := t.TempDir()
		name := "copied.go"
		os.WriteFile(filepath.Join(dir, name), []byte("package x\n\nfunc Copied() int { return 1 }\n"), 0o644)
		res, err := NewResults(dir)
		if err != nil {
			t.Fatal(err)
		}
		src := Source{Key: "lib@1", Component: "lib", Version: "1", License: id, Via: "index",
			Copyrights: []string{"Copyright (c) 2019 Jane Doe"}}
		res.Add([]Finding{{File: name, Lines: Range{1, 3}, Source: src, Match: Match{Kind: "verbatim", Coverage: 1}}}, Input{Kind: "index", Path: "x.rmx"}, nil)
		Evaluate(res, nil, "MIT")
		st := res.Findings[0].Status
		failing := false
		for _, s := range DefaultFailOn {
			failing = failing || s == st
		}
		if !failing {
			t.Errorf("%s (read as %s): copy without any notice is %q, which check passes by default", f, id, st)
		}
	}
}
