package remixware

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default thresholds for reporting a match. A match must cover at least
// MinTokens tokens of the scanned file and span at least MinLines lines.
const (
	DefaultMinTokens = 50
	DefaultMinLines  = 5
)

// ScanOptions controls Scan.
type ScanOptions struct {
	Indexes     []*Index
	MinTokens   int
	MinLines    int
	Exclude     []string // patterns of project paths to leave out
	IncludeDeps bool     // also read vendor/, node_modules/ and similar folders
	Workers     int
	Progress    func(format string, args ...any)
}

// Scan fingerprints a project and compares it with the indexes.
func Scan(project string, opt ScanOptions) (*Results, error) {
	abs, err := realPath(project)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New(project + " is a file: scan needs the project folder")
	}
	if opt.MinTokens <= 0 {
		opt.MinTokens = DefaultMinTokens
	}
	if opt.MinLines <= 0 {
		opt.MinLines = DefaultMinLines
	}
	progress := opt.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}
	res := &Results{Tool: "remixware " + Version, Created: time.Now().UTC().Format(time.RFC3339), Project: abs,
		Skipped: map[string]int{}, MinTokens: opt.MinTokens, MinLines: opt.MinLines, Texts: map[string]*ComponentTexts{}}
	for _, ix := range opt.Indexes {
		res.Inputs = append(res.Inputs, Input{Kind: "index", Path: ix.Path, Components: len(ix.Components), Files: len(ix.Files)})
	}

	// What each indexed file is to this project:
	//  - outside it: code the project may have copied from;
	//  - inside it, in a component that is third-party code (one under a
	//    folder such as vendor or third_party, one with a licence of its
	//    own that differs from the project's, or one in an index made from
	//    a folder inside the project, such as ./vendor): the rest of the
	//    project may have copied from it, and a file isn't reported as a
	//    copy of the component it's part of;
	//  - inside it otherwise: the project's own code, scanned like the
	//    rest, and nothing is reported as copied from it.
	// No index makes a scan skip a file: what an index says, made up or not,
	// can only add matches against its own entries or leave them out.
	projLicense, _ := DetectProjectLicense(abs)
	cs := classifyIndexes(opt.Indexes, abs, projLicense)

	var files, unreadable []string
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == abs {
				return err
			}
			unreadable = append(unreadable, relSlash(abs, path))
			return nil
		}
		rel := relSlash(abs, path)
		if d.IsDir() {
			if path == abs {
				return nil
			}
			if skipIndexDirs[d.Name()] || (!opt.IncludeDeps && skipDirs[d.Name()]) || matchAny(opt.Exclude, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchAny(opt.Exclude, rel) || skipIndexDirs[d.Name()] || (!opt.IncludeDeps && skipDirs[d.Name()]) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Links aren't followed, but a check says how many it passed
			// over: a linked file or folder of code isn't checked.
			if st, err := os.Stat(path); languageFor(d.Name()) != nil || (err == nil && st.IsDir()) {
				res.Skipped["symlink"]++
			}
			return nil
		}
		if !d.Type().IsRegular() || languageFor(d.Name()) == nil {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	progress("scanning %d source files", len(files))

	type fileOut struct {
		skip     string
		lines    int
		findings []Finding
	}
	outs := make([]fileOut, len(files))
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				path := files[i]
				data, skip := readSource(path)
				if skip != "" {
					outs[i].skip = skip
					continue
				}
				sx := languageFor(path)
				toks := tokenize(data, sx)
				fps := winnow(toks, sx)
				rel := relSlash(abs, path)
				var found []Finding
				for k, ix := range opt.Indexes {
					found = append(found, matchFile(ix, cs.own[k], cs.homeOf(k, path), path, rel, toks, fps, opt)...)
				}
				outs[i] = fileOut{lines: countLines(data), findings: found}
			}
		}()
	}
	for i := range files {
		next <- i
	}
	close(next)
	wg.Wait()

	var all []Finding
	for i, o := range outs {
		if o.skip == "unreadable" {
			unreadable = append(unreadable, relSlash(abs, files[i]))
		}
		if o.skip != "" {
			res.Skipped[o.skip]++
			continue
		}
		res.FilesScanned++
		res.LinesScanned += o.lines
		all = append(all, o.findings...)
	}
	// A check that couldn't read part of the project hasn't checked it.
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		shown := unreadable
		if len(shown) > 5 {
			shown = append(shown[:5:5], "...")
		}
		them := "them"
		if len(unreadable) == 1 {
			them = "it"
		}
		return nil, fmt.Errorf("can't read %s in %s: %s. Fix the permissions, or leave %s out with --exclude",
			countNoun(len(unreadable), "path", "paths"), project, strings.Join(shown, ", "), them)
	}
	for _, ix := range opt.Indexes {
		for _, c := range ix.Components {
			if c.LicenseText != "" || c.NoticeText != "" {
				if _, ok := res.Texts[c.Key]; !ok {
					res.Texts[c.Key] = &ComponentTexts{LicenseText: c.LicenseText, NoticeText: c.NoticeText}
				}
			}
		}
	}
	res.Findings = mergeFindings(all)
	res.pruneTexts()
	return res, nil
}

// indexClasses says what the files and components of each index are to one
// project (see Scan).
type indexClasses struct {
	project string
	own     [][]bool         // per index and file: the project's own code
	homes   []map[string]int // per index: folder of a third-party component inside the project -> component
}

func classifyIndexes(indexes []*Index, project, projLicense string) *indexClasses {
	cs := &indexClasses{project: project, own: make([][]bool, len(indexes)), homes: make([]map[string]int, len(indexes))}
	inside := func(p string) bool { return localPath(p) && within(p, project) }
	for k, ix := range indexes {
		third := make([]bool, len(ix.Components))
		cs.homes[k] = map[string]int{}
		for ci, c := range ix.Components {
			if c.Root == project || !inside(c.Root) {
				continue
			}
			r := ix.rootOf(c.Root)
			fromInside := r != "" && r != project && within(r, project)
			ownLicence := c.License != "" && c.License != LicenseNone && !strings.HasPrefix(c.LicenseFrom, "inherited from") &&
				!sameLicense(c.License, projLicense)
			if fromInside || ownLicence || dependencyBoundary(relSlash(project, c.Root)) {
				third[ci] = true
				cs.homes[k][c.Root] = ci
			}
		}
		cs.own[k] = make([]bool, len(ix.Files))
		for i, f := range ix.Files {
			cs.own[k][i] = inside(f.Abs) && !third[f.Comp]
		}
	}
	return cs
}

// homeOf returns the components of index k whose folder, inside the
// project, holds the project file p. A file isn't reported as copied from
// the component it is part of, as when a vendored folder changed since it
// was indexed.
func (cs *indexClasses) homeOf(k int, p string) map[int]bool {
	if len(cs.homes[k]) == 0 {
		return nil
	}
	var out map[int]bool
	for d := filepath.Dir(p); within(d, cs.project) && d != cs.project; d = filepath.Dir(d) {
		if ci, ok := cs.homes[k][d]; ok {
			if out == nil {
				out = map[int]bool{}
			}
			out[ci] = true
		}
	}
	return out
}

// pruneTexts keeps the licence texts of components that were found.
func (r *Results) pruneTexts() {
	used := map[string]bool{}
	for _, f := range r.Findings {
		used[f.Source.Key] = true
		for _, a := range f.Also {
			used[a.Key] = true
		}
	}
	for k := range r.Texts {
		if !used[k] {
			delete(r.Texts, k)
		}
	}
}

// hit is one fingerprint shared by the scanned file and an indexed file.
type hit struct {
	src         uint32
	spos, tpos  int32
	sline, send int32
	tline, tend int32
	exact       bool
}

const (
	maxPostings = 200                          // at most this many indexed places per fingerprint
	maxGap      = 3 * (kgramSize + windowSize) // tokens between two hits of one match
	maxDrift    = 40                           // tokens a match may shift by, for inserted or removed code
)

// maxHitsPerFile is how many shared fingerprints are followed up for one
// scanned file. A variable, so the tests can lower it.
var maxHitsPerFile = 1 << 20

// matchFile finds the stretches of one scanned file that match files in an
// index. own marks the indexed files that are the project's own, and home
// holds the components the scanned file is part of.
func matchFile(ix *Index, own []bool, home map[int]bool, abs, rel string, toks []token, fps []fingerprint, opt ScanOptions) []Finding {
	skip := func(file uint32) bool {
		f := ix.Files[file]
		if own[file] || f.Abs == abs {
			return true
		}
		if home != nil {
			if home[f.Comp] {
				return true
			}
			for _, a := range f.Also {
				if home[a] {
					return true
				}
			}
		}
		return false
	}
	// The indexed places of each fingerprint, without the project's own files.
	lists := make([][]indexFP, len(fps))
	counts := map[uint32]int{}
	total := 0
	for i, f := range fps {
		posts := ix.lookup(f.hash)
		if len(posts) > maxPostings {
			posts = posts[:maxPostings]
		}
		lists[i] = posts
		for _, p := range posts {
			if !skip(p.file) {
				counts[p.file]++
				total++
			}
		}
	}
	if total == 0 {
		return nil
	}
	// Only an index holding many near-identical files, such as hundreds of
	// versions of one large file, gets past the limit; then the files that
	// share the most with this one are followed up, so the work and memory
	// for one file stay bounded.
	var keep map[uint32]bool
	if total > maxHitsPerFile {
		files := make([]uint32, 0, len(counts))
		for f := range counts {
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool {
			if counts[files[i]] != counts[files[j]] {
				return counts[files[i]] > counts[files[j]]
			}
			return files[i] < files[j]
		})
		keep = map[uint32]bool{}
		n := 0
		for _, f := range files {
			if n > 0 && n+counts[f] > maxHitsPerFile {
				break
			}
			keep[f] = true
			n += counts[f]
		}
	}
	hits := make([]hit, 0, min(total, maxHitsPerFile))
	for i, f := range fps {
		for _, p := range lists[i] {
			if (keep != nil && !keep[p.file]) || skip(p.file) {
				continue
			}
			hits = append(hits, hit{src: p.file, spos: int32(f.pos), tpos: int32(p.pos), sline: int32(f.line),
				send: int32(f.end), tline: int32(p.line), tend: int32(p.end), exact: p.exact == f.exact})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.src != b.src {
			return a.src < b.src
		}
		if a.spos != b.spos {
			return a.spos < b.spos
		}
		return a.tpos < b.tpos
	})
	var out []Finding
	for i := 0; i < len(hits); {
		j := i
		for j < len(hits) && hits[j].src == hits[i].src {
			j++
		}
		var srcToks []token
		loaded := false
		for _, ch := range chainHits(preferExact(hits[i:j])) {
			if len(ch) < 2 {
				continue
			}
			if !loaded {
				srcToks, loaded = ix.sourceTokens(ix.Files[hits[i].src]), true
			}
			if f, ok := regionFinding(ix, rel, toks, srcToks, ch, opt); ok {
				out = append(out, f)
			}
		}
		i = j
	}
	return out
}

// sourceTokens tokenizes an indexed file again, when it is still on disk
// unchanged, so a match can be followed token by token past its last
// fingerprint and the report can show it. It returns nil when the file is
// gone, has changed, or isn't one the index may point at (see
// Index.readable): its bytes must match the checksum in the index, so a
// made-up index can't pass off another file on this machine as its own.
func (ix *Index) sourceTokens(sf *SourceFile) []token {
	if !ix.readable(sf.Abs) || sf.Sum == "" {
		return nil
	}
	data, skip := readSource(sf.Abs)
	if skip != "" || fileSum(data) != sf.Sum {
		return nil
	}
	toks := tokenize(data, languageFor(sf.Abs))
	if len(toks) != sf.Tokens {
		return nil
	}
	return toks
}

// sameToken compares tokens the way fingerprints do: names match any name.
func sameToken(a, b token) bool {
	if a.isName() || b.isName() {
		return a.isName() && b.isName()
	}
	return a.norm == b.norm
}

// exactRun counts how far two token streams stay identical, names
// included, going backwards from (s0-1, t0-1) and forwards from (s1, t1).
func exactRun(s, t []token, s0, t0, s1, t1 int32) (back, fwd int) {
	for i, j := s0-1, t0-1; back < 400 && i >= 0 && j >= 0 && s[i].exact == t[j].exact; i, j = i-1, j-1 {
		back++
	}
	for i, j := s1, t1; fwd < 400 && int(i) < len(s) && int(j) < len(t) && s[i].exact == t[j].exact; i, j = i+1, j+1 {
		fwd++
	}
	return
}

// extendMatch follows a match outwards from its first and last shared
// k-gram for as long as the two token streams agree.
func extendMatch(s, t []token, s0, t0, s1, t1 int32) (int32, int32, int32, int32) {
	i, j := s0-1, t0-1
	for steps := 0; steps < 400 && i >= 0 && j >= 0 && sameToken(s[i], t[j]); steps++ {
		i--
		j--
	}
	ns0, nt0 := i+1, j+1
	i, j = s1+kgramSize, t1+kgramSize
	for steps := 0; steps < 400 && int(i) < len(s) && int(j) < len(t) && sameToken(s[i], t[j]); steps++ {
		i++
		j++
	}
	return ns0, nt0, i - 1, j - 1
}

var (
	tokOpen  = fnvHash('p', []byte("{"))
	tokClose = fnvHash('p', []byte("}"))
	tokSemi  = fnvHash('p', []byte(";"))
)

// trimEdges trims the first and last lines of a match when they aren't
// copied: shape alone carries a match into the signature of the next
// function, which only looks like the next function in the source. Up to
// two lines go at each end; then a block the match opens and doesn't close
// is dropped too.
func trimEdges(s, t []token, s0, t0, s1, t1 int32) (int32, int32, int32, int32) {
	for n := 0; n < 2 && s1 > s0; n++ {
		la, _ := lineSpan(s, s1)
		i := max(la, s0)
		if i == s0 || t1-(s1-i+1) < t0 || edgeLineKept(s, t, i, s1, t1-(s1-i), true) {
			break
		}
		t1 -= s1 - i + 1
		s1 = i - 1
	}
	for n := 0; n < 2 && s0 < s1; n++ {
		_, lb := lineSpan(s, s0)
		j := min(lb, s1)
		if j == s1 || t0+(j-s0+1) > t1 || edgeLineKept(s, t, s0, j, t0, false) {
			break
		}
		t0 += j - s0 + 1
		s0 = j + 1
	}
	s1, t1 = trimTail(s, t, s0, s1, t1)
	s0, t0 = trimHead(s, t, s1, s0, t0)
	return s0, t0, s1, t1
}

// lineSpan returns the first and last token on the line of token i.
func lineSpan(s []token, i int32) (int32, int32) {
	a, b := i, i
	for a > 0 && s[a-1].line == s[i].line {
		a--
	}
	for int(b)+1 < len(s) && s[b+1].line == s[i].line {
		b++
	}
	return a, b
}

// edgeLineKept reports whether a line at the edge of a match is copied.
// Tokens i..j of the scanned line are in the match, lined up with the
// source from token ti; tail says the line ends the match, so its side
// facing the rest of the match is its start. The line is copied when all
// of it matches one whole line of the source, or when, counting from the
// side facing the rest of the match, at least a third of it (and three
// tokens) is identical to the source, names included.
func edgeLineKept(s, t []token, i, j, ti int32, tail bool) bool {
	tj := ti + (j - i)
	if ti < 0 || int(tj) >= len(t) {
		return false
	}
	la, lb := lineSpan(s, i)
	if i == la && j == lb && t[ti].line == t[tj].line &&
		(ti == 0 || t[ti-1].line != t[ti].line) && (int(tj)+1 == len(t) || t[tj+1].line != t[tj].line) {
		return true
	}
	run := int32(0)
	if tail {
		for k := i; k <= j && s[k].exact == t[ti+(k-i)].exact; k++ {
			run++
		}
	} else {
		for k := j; k >= i && s[k].exact == t[ti+(k-i)].exact; k-- {
			run++
		}
	}
	return run >= 3 && 3*run >= lb-la+1
}

// coveredWithin counts the tokens in lo..hi covered by k-grams starting at
// the positions.
func coveredWithin(pos []int32, lo, hi int32) int {
	count, end := 0, int32(-1)
	for _, p := range pos {
		a, b := max(p, lo, end+1), min(p+kgramSize-1, hi)
		if b >= a {
			count += int(b - a + 1)
		}
		end = max(end, p+kgramSize-1)
	}
	return count
}

// trimTail pulls the end of a match back when its last tokens open a block
// that the match doesn't close and only have the shape of the source, not
// its names. That's the signature of the next function: it looks like the
// next function in the source without being a copy of it. keep is the
// start of the match.
func trimTail(s, t []token, keep, s1, t1 int32) (int32, int32) {
	open, bal := int32(-1), 0
	for i := s1; i > keep && s1-i < 30 && open < 0; i-- {
		switch s[i].norm {
		case tokClose:
			bal++
		case tokOpen:
			if bal == 0 {
				open = i
			} else {
				bal--
			}
		}
	}
	if open < 0 {
		return s1, t1
	}
	for i := open - 1; i >= keep && open-i < 40; i-- {
		if n := s[i].norm; n != tokClose && n != tokSemi {
			continue
		}
		d := s1 - i
		if t1-d+1 < 0 || int(t1) >= len(t) {
			return s1, t1
		}
		for k := int32(1); k <= d; k++ {
			if s[i+k].exact != t[t1-d+k].exact {
				return i, t1 - d
			}
		}
		return s1, t1
	}
	return s1, t1
}

// trimHead does the same at the start of a match, for the closing lines
// of the function before the copy. keep is the end of the match.
func trimHead(s, t []token, keep, s0, t0 int32) (int32, int32) {
	shut, bal := int32(-1), 0
	for i := s0; i < keep && i-s0 < 30 && shut < 0; i++ {
		switch s[i].norm {
		case tokOpen:
			bal++
		case tokClose:
			if bal == 0 {
				shut = i
			} else {
				bal--
			}
		}
	}
	if shut < 0 {
		return s0, t0
	}
	d := shut - s0 + 1
	if t0 < 0 || int(t0+d) > len(t) {
		return s0, t0
	}
	for k := int32(0); k < d; k++ {
		if s[s0+k].exact != t[t0+k].exact {
			return shut + 1, t0 + d
		}
	}
	return s0, t0
}

// preferExact drops shape-only hits where the same k-gram of the scanned
// file also matches the indexed file exactly, names included. Two lines
// such as "let a = a.chars()" and "let b = b.chars()" have the same shape:
// each matches both, and only the exact hit says which is which.
func preferExact(hs []hit) []hit {
	out := hs[:0:0]
	for i := 0; i < len(hs); {
		j := i
		exact := false
		for j < len(hs) && hs[j].spos == hs[i].spos {
			exact = exact || hs[j].exact
			j++
		}
		for _, h := range hs[i:j] {
			if h.exact || !exact {
				out = append(out, h)
			}
		}
		i = j
	}
	return out
}

// chainHits links hits on one indexed file into runs that advance together
// through both files: one run per copied stretch.
func chainHits(hs []hit) [][]hit {
	type chain struct {
		hits []hit
	}
	var active, done []*chain
	for _, h := range hs {
		// Retire chains that are too far behind to continue.
		kept := active[:0]
		for _, c := range active {
			if h.spos-c.hits[len(c.hits)-1].spos > maxGap {
				done = append(done, c)
			} else {
				kept = append(kept, c)
			}
		}
		active = kept
		best, bestCost := -1, int32(1<<30)
		for k, c := range active {
			last := c.hits[len(c.hits)-1]
			ds := h.spos - last.spos
			dt := h.tpos - last.tpos
			if ds <= 0 || dt <= -kgramSize || dt > maxGap {
				continue
			}
			drift := dt - ds
			if drift < 0 {
				drift = -drift
			}
			if drift > maxDrift {
				continue
			}
			if cost := drift*2 + ds; cost < bestCost {
				best, bestCost = k, cost
			}
		}
		if best >= 0 {
			active[best].hits = append(active[best].hits, h)
			continue
		}
		if len(active) >= 64 {
			done = append(done, active[0])
			active = active[1:]
		}
		active = append(active, &chain{hits: []hit{h}})
	}
	done = append(done, active...)
	out := make([][]hit, 0, len(done))
	for _, c := range done {
		out = append(out, c.hits)
	}
	return out
}

// covered counts the tokens covered by k-grams starting at the positions.
func covered(pos []int32) (count int, first, last int32) {
	sort.Slice(pos, func(i, j int) bool { return pos[i] < pos[j] })
	first, last = pos[0], pos[len(pos)-1]+kgramSize-1
	end := int32(-1)
	for _, p := range pos {
		s, e := p, p+kgramSize-1
		if s <= end {
			s = end + 1
		}
		if e >= s {
			count += int(e - s + 1)
		}
		if e > end {
			end = e
		}
	}
	return
}

func regionFinding(ix *Index, rel string, toks, srcToks []token, ch []hit, opt ScanOptions) (Finding, bool) {
	if len(ch) < 2 {
		return Finding{}, false
	}
	spos := make([]int32, len(ch))
	tpos := make([]int32, len(ch))
	exact := 0
	sline, send := ch[0].sline, ch[0].send
	tline, tend := ch[0].tline, ch[0].tend
	for i, h := range ch {
		spos[i], tpos[i] = h.spos, h.tpos
		if h.exact {
			exact++
		}
		sline, send = min(sline, h.sline), max(send, h.send)
		tline, tend = min(tline, h.tline), max(tend, h.tend)
	}
	scov, sfirst, slast := covered(spos)
	tcov, _, _ := covered(tpos)
	first, last := ch[0], ch[len(ch)-1]
	ratio := float64(exact) / float64(len(ch))
	// Evidence is what the fingerprints cover, plus code on either side
	// that is identical to the source, names included. Code that only has
	// the same shape doesn't count: in argument lists, case tables and
	// test tables any two files look alike.
	evidence := scov
	if srcToks != nil && int(last.tpos)+kgramSize <= len(srcToks) && int(last.spos)+kgramSize <= len(toks) {
		back, fwd := exactRun(toks, srcToks, first.spos, first.tpos, last.spos+kgramSize, last.tpos+kgramSize)
		evidence += back + fwd
	}
	// When only the structure agrees and not the names, ask for more of it,
	// and for a match without gaps: renamed copies match almost completely,
	// look-alike code doesn't.
	need, density := opt.MinTokens, 0.5
	if ratio < 0.5 {
		need, density = opt.MinTokens*6/5, 0.7
	}
	if evidence < need || int(send-sline)+1 < opt.MinLines || int(tend-tline)+1 < opt.MinLines ||
		float64(scov) < density*float64(slast-sfirst+1) {
		return Finding{}, false
	}
	// The match is followed token by token only when what comes out lies
	// inside both files: positions in an index are data, and data can be
	// wrong.
	extend := srcToks != nil && int(last.tpos)+kgramSize <= len(srcToks) && int(last.spos)+kgramSize <= len(toks)
	var ns0, nt0, ns1, nt1 int32
	if extend {
		ns0, nt0, ns1, nt1 = extendMatch(toks, srcToks, first.spos, first.tpos, last.spos, last.tpos)
		ns0, nt0, ns1, nt1 = trimEdges(toks, srcToks, ns0, nt0, ns1, nt1)
		extend = 0 <= ns0 && ns0 <= ns1 && int(ns1) < len(toks) && 0 <= nt0 && nt0 <= nt1 && int(nt1) < len(srcToks)
	}
	if extend {
		// Coverage of the final region: the fingerprints inside it plus
		// what the extension added.
		scov = coveredWithin(spos, ns0, ns1) + int(max(0, sfirst-ns0)) + int(max(0, ns1-slast))
		tcov += int(first.tpos-nt0) + int(nt1-(last.tpos+kgramSize-1))
		sfirst, slast = ns0, ns1
		sline, send = toks[ns0].line, toks[ns1].line
		tline, tend = srcToks[nt0].line, srcToks[nt1].line
		// What is reported must still clear the thresholds.
		if scov < opt.MinTokens || int(send-sline)+1 < opt.MinLines || int(tend-tline)+1 < opt.MinLines {
			return Finding{}, false
		}
	}
	span := int(slast-sfirst) + 1
	sf := ix.Files[ch[0].src]
	comp := ix.Components[sf.Comp]
	kind := "renamed"
	switch {
	case ratio >= 0.9:
		kind = "verbatim"
	case ratio >= 0.3:
		kind = "edited"
	}
	src := sourceFromIndex(ix, sf, comp)
	src.Lines = Range{int(tline), int(tend)}
	if srcToks == nil {
		// Not read back, so the report can't show it either.
		src.Abs = ""
	}
	for _, a := range sf.Also {
		if v := ix.Components[a].Version; v != "" && v != src.Version && !containsString(src.OtherVersions, v) {
			src.OtherVersions = append(src.OtherVersions, v)
		}
	}
	whole := sf.Tokens > 0 && float64(tcov) >= 0.9*float64(sf.Tokens) && float64(scov) >= 0.9*float64(len(toks))
	f := Finding{
		File:   rel,
		Lines:  Range{int(sline), int(send)},
		Source: src,
		Match:  Match{Kind: kind, Coverage: round2(float64(scov) / float64(span)), Tokens: scov, WholeFile: whole},
		score:  float64(scov) + ratio,
	}
	return f, true
}

func sourceFromIndex(ix *Index, sf *SourceFile, comp *Component) Source {
	s := Source{Key: comp.Key, Component: comp.Name, Version: comp.Version, File: sf.Path, URL: comp.URL,
		PURL: comp.PURL, License: comp.License, LicenseFrom: comp.LicenseFrom, Via: "index", Abs: sf.Abs}
	if sf.License != "" {
		s.License, s.LicenseFrom = sf.License, "header of "+sf.Path
		// A header licence added to the component's: say where both came from.
		if comp.License != "" && comp.License != LicenseNone && strings.Contains(" "+sf.License+" ", " "+comp.License+" ") {
			from := comp.LicenseFrom
			if from == "" {
				from = "the component's licence"
			}
			s.LicenseFrom = from + " and the header of " + sf.Path
		}
	}
	s.Copyrights = dedupe(append(append([]string{}, sf.Copyrights...), comp.Copyrights...))
	if len(s.Copyrights) > 8 {
		s.Copyrights = s.Copyrights[:8]
	}
	return s
}

func round2(x float64) float64 { return float64(int(x*100+0.5)) / 100 }
