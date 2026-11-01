package remixware

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLicenceMisreads: texts that 0.4 read as a licence asking less than
// they do (testdata/licences, from the SPDX License List).
func TestLicenceMisreads(t *testing.T) {
	want := map[string]string{
		"OFL-1.1.txt":                          "OFL-1.1", // was MIT-0
		"OFL-1.0.txt":                          "OFL-1.0",
		"BSD-3-Clause-difflib-wording.txt":     "BSD-3-Clause", // was BSD-2-Clause
		"BSD-3-Clause-No-Nuclear-License.txt":  LicenseUnrecognised,
		"BSD-3-Clause-No-Military-License.txt": LicenseUnrecognised,
		"TOSL.txt":                             LicenseUnrecognised, // source must be available; was BSD-2-Clause
		"TMate.txt":                            LicenseUnrecognised,
		"TU-Berlin-2.0.txt":                    LicenseUnrecognised, // keeps the notice; was 0BSD
		"FSL-1.1-MIT.txt":                      "FSL-1.1-MIT",       // was MIT
		"FSL-1.1-ALv2.txt":                     "FSL-1.1-ALv2",      // was Apache-2.0
		"Sleepycat.txt":                        "Sleepycat",         // was BSD-3-Clause
		"CC-BY-NC-ND-2.0.txt":                  "CC-BY-NC-ND-4.0",   // was CC-BY-4.0
		"CC-BY-NC-SA-2.0.txt":                  "CC-BY-NC-SA-4.0",
		"GPL-2.0-only.txt":                     "GPL-2.0-only",
		"LGPL-2.1-only.txt":                    "LGPL-2.1-only",
	}
	for file, id := range want {
		b, err := os.ReadFile(filepath.Join("testdata", "licences", file))
		if err != nil {
			t.Fatal(err)
		}
		if got := DetectLicense(string(b)); got != id {
			t.Errorf("%s: read as %q, want %q", file, got, id)
		}
	}
	for id, cat := range map[string]Category{"OFL-1.1": CatWeakCopyleft, "FSL-1.1-MIT": CatSourceAvailable,
		"Sleepycat": CatCopyleft, "CC-BY-NC-ND-4.0": CatSourceAvailable, LicenseUnrecognised: CatUnknown} {
		if c := lookupLicense(id).Cat; c != cat {
			t.Errorf("%s is %v, want %v", id, c, cat)
		}
	}
}

// TestNamedLicenceFiles: LICENSE-MIT and LICENSE-APACHE are a choice
// unless a short file says the parts differ; GPL and LGPL files never are.
func TestNamedLicenceFiles(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	mit, apache := read("licensetexts/MIT.txt"), read("licensetexts/Apache-2.0.txt")
	gpl, lgpl := read("testdata/licences/GPL-2.0-only.txt"), read("testdata/licences/LGPL-2.1-only.txt")
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"dual", map[string]string{"LICENSE-MIT": mit, "LICENSE-APACHE": apache}, "Apache-2.0 OR MIT"},
		{"dual, a README-style choice", map[string]string{"LICENSE-MIT": mit, "LICENSE-APACHE": apache,
			"LICENSE": "Licensed under either of LICENSE-APACHE or LICENSE-MIT at your option.\n"}, "Apache-2.0 OR MIT"},
		{"parts differ", map[string]string{"LICENSE-MIT": mit, "LICENSE-APACHE": apache,
			"LICENSE": "The library is under the terms in LICENSE-MIT. The documentation is under LICENSE-APACHE.\n"}, "AND"},
		{"GPL and LGPL", map[string]string{"COPYING.GPL": gpl, "COPYING.LGPL": lgpl}, "GPL-2.0-only AND LGPL-2.1-only"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		var paths []string
		for n, body := range c.files {
			p := filepath.Join(dir, n)
			os.WriteFile(p, []byte(body), 0o644)
			paths = append(paths, p)
		}
		got, _, _, _ := licenseFromFiles(paths)
		switch {
		case c.want == "AND":
			if !containsAll(got, "Apache-2.0", "MIT") || containsAny(got, " OR ") {
				t.Errorf("%s: %q, want Apache-2.0 and MIT both applying", c.name, got)
			}
		case got != c.want:
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !containsAny(s, x) {
			return false
		}
	}
	return true
}

func containsAny(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
