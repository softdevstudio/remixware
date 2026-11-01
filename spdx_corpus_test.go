package remixware

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// asksNothing are SPDX licences Remixware doesn't list whose texts ask for
// nothing, so reading them as a no-conditions licence is right: Clips and
// MIT-testregex grant MIT's rights with no notice to keep, and
// Unlicense-libwhirlpool is the Unlicense.
var asksNothing = map[string]bool{"Clips": true, "MIT-testregex": true, "Unlicense-libwhirlpool": true}

// TestSPDXCorpus reads every text of the SPDX License List (the folder in
// SPDX_TEXTS, from scripts/spdx-corpus.sh) and checks the promise: no text
// is read as a licence that asks less of you than the licence it is. That
// is, a listed licence is never read as one of a more lenient category, and
// an unlisted one is never read as a no-conditions licence unless its text
// asks for nothing. With SPDX_OUT set, it writes every reading there.
func TestSPDXCorpus(t *testing.T) {
	dir := os.Getenv("SPDX_TEXTS")
	if dir == "" {
		t.Skip("set SPDX_TEXTS to the text folder of spdx/license-list-data (scripts/spdx-corpus.sh)")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	sort.Strings(files)
	var out []string
	counts := map[string]int{}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".txt")
		if strings.HasPrefix(id, "deprecated_") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got := DetectLicense(string(b))
		truth := lookupLicense(id).Cat
		first := strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(got))
		cat := CatUnknown
		if len(first) > 0 {
			cat = lookupLicense(first[0]).Cat
		}
		verdict := ""
		switch {
		case strings.EqualFold(got, id):
			verdict = "same"
		case truth != CatUnknown && cat < truth:
			verdict = "MORE LENIENT"
			t.Errorf("%s (%v) read as %q (%v)", id, truth, got, cat)
		case truth == CatUnknown && cat == CatPublicDomain && !asksNothing[id]:
			verdict = "MORE LENIENT"
			t.Errorf("%s, a licence Remixware doesn't list, read as %q, which asks nothing", id, got)
		case truth == CatUnknown && cat == CatPublicDomain:
			verdict = "asks nothing"
		case got == "" || got == LicenseUnrecognised:
			verdict = "review"
		case truth == CatUnknown && cat == CatUnknown:
			verdict = "review"
		case truth == CatUnknown:
			// A variant Remixware doesn't list, read as a listed licence that
			// asks at least for a notice (the BSD and MIT variants, mostly).
			verdict = "unlisted, read as " + cat.String()
		case cat > truth:
			verdict = "stricter"
		default:
			verdict = "same category"
		}
		counts[verdict]++
		if got == "" {
			got = "(none)"
		}
		out = append(out, fmt.Sprintf("%-45s %-45s %s", id, got, verdict))
	}
	unlisted := 0
	for k, n := range counts {
		if strings.HasPrefix(k, "unlisted") {
			unlisted += n
		}
	}
	summary := fmt.Sprintf("%d texts: %d read as the same licence, %d as another listed licence of the same category, %d as a stricter one, "+
		"%d unlisted ones as a listed licence asking at least for a notice, %d left for review, "+
		"%d unlisted texts that ask nothing read as such, %d more lenient",
		len(out), counts["same"], counts["same category"], counts["stricter"], unlisted, counts["review"], counts["asks nothing"], counts["MORE LENIENT"])
	t.Log(summary)
	if p := os.Getenv("SPDX_OUT"); p != "" {
		text := summary + "\n\n" + fmt.Sprintf("%-45s %-45s %s\n", "SPDX id", "read as", "verdict") + strings.Join(out, "\n") + "\n"
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
