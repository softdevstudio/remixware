package remixware

import (
	"fmt"
	"sync"
)

// Some code is written the same way by everyone, because the language asks
// for it. Go's sort.Interface is the main case: a slice type with Len, Swap
// and Less methods looks alike in every project, whoever wrote it. Matches
// made of it would be false alarms, so its k-grams are never fingerprinted.
//
// The idioms are generated here rather than taken from any project: every
// order of the three methods, value and pointer receivers, and the common
// shapes of the comparison in Less.

var (
	idiomOnce   sync.Once
	idiomHashes map[uint64]bool
)

func goIdioms() []string {
	lessBodies := []string{
		"return x[i] < x[j]",
		"return x[i] > x[j]",
		"return x[i].a < x[j].a",
		"return x[i].a > x[j].a",
		"return x[i].a.b < x[j].a.b",
		"return x[i].a() < x[j].a()",
		"return f(x[i], x[j])",
		"return f(x[i].a, x[j].a)",
		"return a.b(x[i].c, x[j].c) < 0",
		"return a.b(x[i].c, x[j].c) == -1",
		"return x[i].a < x[j].a || (x[i].a == x[j].a && x[i].b < x[j].b)",
	}
	methods := func(recv, less string) map[string]string {
		return map[string]string{
			"Len":  fmt.Sprintf("func (%s) Len() int { return len(x) }", recv),
			"Swap": fmt.Sprintf("func (%s) Swap(i, j int) { x[i], x[j] = x[j], x[i] }", recv),
			"Less": fmt.Sprintf("func (%s) Less(i, j int) bool { %s }", recv, less),
		}
	}
	orders := [][]string{{"Len", "Less", "Swap"}, {"Len", "Swap", "Less"}, {"Less", "Len", "Swap"},
		{"Less", "Swap", "Len"}, {"Swap", "Len", "Less"}, {"Swap", "Less", "Len"}}
	var out []string
	for _, recv := range []string{"x T", "x *T"} {
		for _, less := range lessBodies {
			m := methods(recv, less)
			for _, o := range orders {
				for _, decl := range []string{"", "type T []U\n\n", "type T []*U\n\n", "type T []a.U\n\n"} {
					out = append(out, decl+m[o[0]]+"\n\n"+m[o[1]]+"\n\n"+m[o[2]]+"\n")
				}
			}
		}
	}
	return out
}

// isIdiom reports whether a normalised k-gram hash belongs to a generated
// idiom.
func isIdiom(h uint64) bool {
	idiomOnce.Do(func() {
		idiomHashes = map[uint64]bool{}
		for _, src := range goIdioms() {
			toks := tokenize([]byte(src), synGo)
			norm, _ := rawKgramHashes(toks)
			for _, h := range norm {
				if h != invalidHash {
					idiomHashes[h] = true
				}
			}
		}
	})
	return idiomHashes[h]
}

// idiomLanguage reports whether idioms are generated for a language.
func idiomLanguage(sx *syntax) bool { return sx == synGo }
