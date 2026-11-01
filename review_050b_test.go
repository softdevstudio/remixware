package remixware

// Re-check of the 0.5.0 review fixes. Each test fails on the code as
// re-checked.

import (
	"os"
	"strings"
	"testing"
)

// A crate patched to a git fork at the version main has, in the same
// change that adds another version of it: newVers isn't empty, so the
// same-version source check never runs, and only the new version is
// checked.
func TestReview050bCargoMovedVersionNextToNewOne(t *testing.T) {
	cio := `source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "aa"`
	base := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "1.0.0"
` + cio + `
`)}
	head := map[string]string{"Cargo.lock": cargoLockWith(`
[[package]]
name = "forked"
version = "1.0.0"
source = "git+https://github.com/someone/fork#cccccccccccccccccccccccccccccccccccccccc"

[[package]]
name = "forked"
version = "2.0.0"
` + cio + `
`)}
	res := compare(t, head, base, nil, nil)
	c := change(res, "cargo/forked")
	if c == nil {
		t.Fatal("no change")
	}
	found := false
	for _, i := range c.Issues {
		found = found || (i.Version == "1.0.0" && (i.Status == DepNotChecked || i.Status == DepInstallScript))
	}
	if !found {
		t.Errorf("1.0.0 moved from crates.io to a git fork, not reported: %+v", c)
	}
}

// Sources of one version from several lockfiles are joined in lockfile
// order, so the same crates moving between two Cargo.lock files (no
// source change at all) reads as a source change.
func TestReview050bCargoSourceOrderAcrossLockfiles(t *testing.T) {
	git := `source = "git+https://github.com/example/forked#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`
	cio := `source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "aa"`
	entry := func(src string) string {
		return "\n[[package]]\nname = \"forked\"\nversion = \"1.0.0\"\n" + src + "\n"
	}
	base := map[string]string{"a/Cargo.lock": cargoLockWith(entry(git)), "b/Cargo.lock": cargoLockWith(entry(cio))}
	head := map[string]string{"a/Cargo.lock": cargoLockWith(entry(cio)), "b/Cargo.lock": cargoLockWith(entry(git))}
	res := compare(t, head, base, allowPolicy(DepAllowRule{Package: "cargo/forked", Reason: "x", InstallScripts: true}), nil)
	if fs := DepFailures(res, DefaultDepFailOn); len(fs) != 0 {
		t.Errorf("the same two sources, swapped between lockfiles, read as a source change: %+v", fs)
	}
}

// FDK-AAC (and FDK-MPEG-H) ask that "complete source code" be made
// available; addsTerms now only matches "obtain complete source code", so
// both read as BSD-3-Clause (the 0.5.0 candidate had them for review).
func TestReview050bSourceAvailableWording(t *testing.T) {
	bsd, err := os.ReadFile("licensetexts/BSD-3-Clause.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := string(bsd) + "\nYou must make available free of charge copies of the complete source code of the FDK AAC Codec and your modifications thereto to recipients of copies in binary form.\n"
	if got := DetectLicense(text); got == "BSD-3-Clause" {
		t.Errorf("a BSD text that requires the complete source to be made available: %q", got)
	}
}

// Go ignores a // +build line that isn't followed by a blank line (it
// must end the header); Remixware applies it.
func TestReview050bGoPlusBuildWithoutBlankLine(t *testing.T) {
	src := "// +build aix\npackage x\n\nimport \"C\"\n"
	if !goNative(t, map[string]string{"go.mod": "module example.com/rv\n", "c.go": src}) {
		t.Error("a +build line straight above the package clause (ignored by Go) hides cgo")
	}
}

// Per-file licensing in wordings outside the short exclusion list (every
// file, each file, some files) is read as a choice for the whole project.
func TestReview050bPerFileWordingReadAsChoice(t *testing.T) {
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return string(b)
	}
	gpl, lgpl := read("testdata/licences/GPL-2.0-only.txt"), read("testdata/licences/LGPL-2.1-only.txt")
	for _, w := range []string{
		"Individual source files are licensed under COPYING.GPL or COPYING.LGPL, as stated in their headers.\n",
		"Files in this project are released under LICENSE-GPL or LICENSE-LGPL; the header of each says which.\n",
	} {
		names := []string{"COPYING.GPL", "COPYING.LGPL"}
		if strings.Contains(w, "LICENSE-GPL") {
			names = []string{"LICENSE-GPL", "LICENSE-LGPL"}
		}
		got := licenceDir(t, map[string]string{names[0]: gpl, names[1]: lgpl, "LICENSE": w})
		if strings.Contains(got, " OR ") {
			t.Errorf("%q: %q, but each file is under one of them, not the reader's pick", strings.TrimSpace(w), got)
		}
	}
}

// A real dual licence written over two sentences ("... under LICENSE-MIT.
// Alternatively, ... under LICENSE-APACHE.") is cut at the sentence break
// and read as AND.
func TestReview050bDualLicenceAlternativelySentence(t *testing.T) {
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return string(b)
	}
	mit, apache := read("licensetexts/MIT.txt"), read("licensetexts/Apache-2.0.txt")
	w := "You may use this software under the terms of LICENSE-MIT. Alternatively, you may use it under the terms of LICENSE-APACHE.\n"
	got := licenceDir(t, map[string]string{"LICENSE-MIT": mit, "LICENSE-APACHE": apache, "LICENSE": w})
	if !strings.Contains(got, "Apache-2.0 OR MIT") {
		t.Errorf("%q", got)
	}
}
