package remixware

import "math"

// Fingerprinting settings. A k-gram is K tokens in a row; winnowing keeps the
// smallest k-gram hash in every window of W k-grams. Together they guarantee
// that any shared run of at least W+K-1 tokens produces at least one shared
// fingerprint, while keeping about 2/(W+1) of all k-grams.
const (
	kgramSize   = 17
	windowSize  = 12
	minDistinct = 6 // k-grams with fewer distinct tokens (lists, tables) are ignored
	maxRepeats  = 4 // k-grams found this often in one file are ignored as the file's own boilerplate
	normVersion = 2 // bump when tokenizing or hashing changes: old indexes stop matching
)

// fingerprint is one selected k-gram.
type fingerprint struct {
	hash  uint64 // hash of the normalised k-gram, names numbered by first use
	exact uint32 // hash of the k-gram as written
	pos   uint32 // index of the k-gram's first token
	line  uint32 // line of the first token
	end   uint32 // line of the last token
}

func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

const invalidHash = math.MaxUint64

// kgramHashes returns the normalised and exact hash of every k-gram.
//
// Names are replaced by their order of first appearance inside the k-gram,
// so "a, b := f(a)" and "x, y := g(x)" hash the same, but "a, b := f(b)"
// does not. Consistent renaming keeps the hash; code that only shares
// a shape with no shared flow of names doesn't.
func rawKgramHashes(toks []token) (norm []uint64, exact []uint32) {
	n := len(toks) - kgramSize + 1
	if n <= 0 {
		return nil, nil
	}
	norm = make([]uint64, n)
	exact = make([]uint32, n)
	var names [kgramSize]uint64
	var vals [kgramSize]uint64
	for s := 0; s < n; s++ {
		nn, words := 0, 0
		h := uint64(0x6a09e667f3bcc909)
		e := uint64(0xbb67ae8584caa73b)
		for j := 0; j < kgramSize; j++ {
			t := toks[s+j]
			v := t.norm
			if t.isName() || t.keyword {
				words++
			}
			if t.isName() {
				id := -1
				for k := 0; k < nn; k++ {
					if names[k] == t.exact {
						id = k
						break
					}
				}
				if id < 0 {
					id = nn
					names[nn] = t.exact
					nn++
				}
				v = 0x9e3779b97f4a7c15 ^ uint64(id+1)
			}
			vals[j] = v
			h = mix64(h ^ (v + uint64(j)*0x632be59bd9b4e019))
			e = mix64(e ^ (t.exact + uint64(j)*0x632be59bd9b4e019))
		}
		switch {
		case distinct(vals[:]) < minDistinct:
			h = invalidHash
		case words == 0:
			// Only numbers, strings and punctuation: a table of constants
			// such as SHA-256's, the same in every language that has it.
			h = invalidHash
		case h == invalidHash:
			h--
		}
		norm[s] = h
		exact[s] = uint32(e >> 32)
	}
	return norm, exact
}

// kgramHashes returns the k-gram hashes of a file with the ones that mean
// nothing marked invalid.
func kgramHashes(toks []token, sx *syntax) (norm []uint64, exact []uint32) {
	norm, exact = rawKgramHashes(toks)
	n := len(norm)
	// K-grams that recur four or more times in one file are its own
	// boilerplate: runs of similar declarations, table rows, repeated case
	// arms. They say nothing about where the file came from.
	counts := make(map[uint64]int32, n)
	for _, h := range norm {
		if h != invalidHash {
			counts[h]++
		}
	}
	idioms := idiomLanguage(sx)
	for i, h := range norm {
		if h != invalidHash && (counts[h] >= maxRepeats || (idioms && isIdiom(h))) {
			norm[i] = invalidHash
		}
	}
	return norm, exact
}

func distinct(vals []uint64) int {
	count := 0
	for i, v := range vals {
		seen := false
		for _, w := range vals[:i] {
			if w == v {
				seen = true
				break
			}
		}
		if !seen {
			count++
		}
	}
	return count
}

// winnow selects fingerprints from a token stream: in every window of W
// consecutive k-grams, the k-gram with the smallest hash (the rightmost one
// on ties) is kept, and each kept k-gram is recorded once.
func winnow(toks []token, sx *syntax) []fingerprint {
	norm, exact := kgramHashes(toks, sx)
	n := len(norm)
	if n == 0 {
		return nil
	}
	w := windowSize
	if n < w {
		w = n
	}
	out := make([]fingerprint, 0, n*2/(windowSize+1)+1)
	last := -1
	for start := 0; start+w <= n; start++ {
		best := -1
		for j := start; j < start+w; j++ {
			if best < 0 || norm[j] <= norm[best] {
				best = j
			}
		}
		if norm[best] == invalidHash || best == last {
			continue
		}
		last = best
		out = append(out, fingerprint{
			hash:  norm[best],
			exact: exact[best],
			pos:   uint32(best),
			line:  uint32(toks[best].line),
			end:   uint32(toks[best+kgramSize-1].line),
		})
	}
	return out
}
