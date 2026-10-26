package remixware

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// SourceFile is one indexed file.
type SourceFile struct {
	Comp       int      `json:"c"`
	Path       string   `json:"p"` // relative to the component root
	Abs        string   `json:"a"`
	License    string   `json:"l,omitempty"` // set when the file's own header names a licence
	Copyrights []string `json:"cr,omitempty"`
	Lines      int      `json:"n"`
	Tokens     int      `json:"t"`
	Also       []int    `json:"also,omitempty"` // other components (often other versions) holding the same file
}

type indexHeader struct {
	Format      int            `json:"format"`
	Tool        string         `json:"tool"`
	Created     string         `json:"created"`
	K           int            `json:"k"`
	W           int            `json:"w"`
	MinDistinct int            `json:"min_distinct"`
	Norm        int            `json:"norm"`
	Roots       []string       `json:"roots"`
	Components  []*Component   `json:"components"`
	Files       []*SourceFile  `json:"files"`
	Lines       int            `json:"lines"`
	Duplicates  int            `json:"duplicates"` // files skipped because another version holds the same code
	Skipped     map[string]int `json:"skipped,omitempty"`
	MaxDF       int            `json:"max_df"`
	Dropped     int            `json:"dropped_common"` // fingerprints dropped as too common to mean anything
}

// indexFP is a fingerprint as stored in an index.
type indexFP struct {
	hash  uint64
	exact uint32
	file  uint32
	pos   uint32
	line  uint32
	end   uint32
}

const indexRecordSize = 28

// Index is a set of fingerprinted source files to compare projects with.
type Index struct {
	Path string
	indexHeader
	fps []indexFP // sorted by hash
}

// IndexOptions controls BuildIndex.
type IndexOptions struct {
	Name     string   // name of the root component (one root only)
	Version  string   // version of the root component (one root only)
	License  string   // licence for every file, overriding what is found
	MaxDF    int      // drop fingerprints found in more than this many components; 0 picks a value, -1 keeps all
	Exclude  []string // patterns of paths to leave out
	Workers  int
	Progress func(format string, args ...any)
}

type pendingFile struct {
	abs  string
	comp int
	rel  string
}

type fileResult struct {
	skip    string
	sf      *SourceFile
	fps     []fingerprint
	content [32]byte
	tokens  int
}

// BuildIndex walks folders of third-party code and fingerprints every
// source file, recording which component and licence each file belongs to.
func BuildIndex(roots []string, opt IndexOptions) (*Index, error) {
	if len(roots) == 0 {
		return nil, errors.New("nothing to index: name at least one folder")
	}
	if (opt.Name != "" || opt.Version != "") && len(roots) > 1 {
		return nil, errors.New("--name and --version need a single folder")
	}
	progress := opt.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}
	ix := &Index{indexHeader: indexHeader{Format: 1, Tool: "remixware " + Version,
		Created: time.Now().UTC().Format(time.RFC3339), K: kgramSize, W: windowSize, MinDistinct: minDistinct,
		Norm: normVersion, Skipped: map[string]int{}}}

	var pending []pendingFile
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("%s is a file: index needs a folder", root)
		}
		ix.Roots = append(ix.Roots, abs)
		files, err := ix.discover(abs, opt)
		if err != nil {
			return nil, err
		}
		pending = append(pending, files...)
	}
	progress("found %d source files in %d components", len(pending), len(ix.Components))

	results := make([]fileResult, len(pending))
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
				results[i] = fingerprintSourceFile(pending[i], ix.Components[pending[i].comp].License, opt.License == "")
			}
		}()
	}
	for i := range pending {
		next <- i
		if i > 0 && i%5000 == 0 {
			progress("fingerprinted %d of %d files", i, len(pending))
		}
	}
	close(next)
	wg.Wait()

	type contentKey struct {
		name string
		sum  [32]byte
	}
	seen := map[contentKey]int{}
	var fileFPs [][]fingerprint
	for i, r := range results {
		if r.skip != "" {
			ix.Skipped[r.skip]++
			continue
		}
		comp := ix.Components[pending[i].comp]
		key := contentKey{comp.Name, r.content}
		if prev, ok := seen[key]; ok && r.tokens > 0 {
			f := ix.Files[prev]
			// Keep the project's own copy of a file over a vendored copy of it.
			if depth(r.sf.Abs) < depth(f.Abs) {
				old := *f
				*f = *r.sf
				f.Also = old.Also
				if old.Comp != f.Comp && !containsInt(f.Also, old.Comp) {
					f.Also = append(f.Also, old.Comp)
				}
				// The same tokens can be laid out on other lines: keep the
				// fingerprints of the copy that is kept.
				fileFPs[prev] = r.fps
			} else if f.Comp != pending[i].comp && !containsInt(f.Also, pending[i].comp) {
				f.Also = append(f.Also, pending[i].comp)
			}
			ix.Duplicates++
			continue
		}
		seen[key] = len(ix.Files)
		ix.Files = append(ix.Files, r.sf)
		fileFPs = append(fileFPs, r.fps)
	}
	var fps []indexFP
	for id, list := range fileFPs {
		ix.Lines += ix.Files[id].Lines
		for _, f := range list {
			fps = append(fps, indexFP{hash: f.hash, exact: f.exact, file: uint32(id), pos: f.pos, line: f.line, end: f.end})
		}
	}
	ix.dropEmptyComponents()
	ix.fps = fps
	ix.filterCommon(opt.MaxDF)
	return ix, nil
}

// depth counts the dependency folders (vendor, node_modules and the like)
// in a path.
func depth(path string) int {
	n := 0
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if dependencyBoundary(part) {
			n++
		}
	}
	return n
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// discover walks one root, finds the components in it and the source files
// that belong to each.
func (ix *Index) discover(root string, opt IndexOptions) ([]pendingFile, error) {
	dirs := map[string]*dirInfo{}
	owners := map[string][]string{}    // path of a Python package or module -> the dist-info folders installing it
	recordOwner := map[string]string{} // file -> the dist-info folder whose RECORD lists it
	distInfo := map[string]*manifest{} // dist-info folder -> metadata
	distLic := map[string][]string{}
	type codeFile struct{ abs, dir string }
	var code []codeFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			ix.Skipped["unreadable"]++
			return nil
		}
		rel := relSlash(root, path)
		if d.IsDir() {
			if path != root && (skipIndexDirs[d.Name()] || matchAny(opt.Exclude, rel)) {
				return filepath.SkipDir
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				ix.Skipped["unreadable"]++
				return filepath.SkipDir
			}
			info := readDirInfo(path, entries)
			dirs[path] = info
			if strings.HasSuffix(d.Name(), ".dist-info") || strings.HasSuffix(d.Name(), ".egg-info") {
				m, lic := readDistInfo(path)
				if m.name != "" {
					distInfo[path] = m
					distLic[path] = lic
					parent := filepath.Dir(path)
					for _, top := range m.topLevel {
						top = strings.ReplaceAll(top, "/", string(filepath.Separator))
						for _, q := range []string{filepath.Join(parent, top), filepath.Join(parent, top+".py")} {
							if !containsString(owners[q], path) {
								owners[q] = append(owners[q], path)
							}
						}
					}
					for _, rp := range m.record {
						if f := filepath.Join(parent, filepath.FromSlash(rp)); !filepath.IsAbs(rp) && within(f, parent) && f != parent {
							recordOwner[f] = path
						}
					}
				}
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || languageFor(d.Name()) == nil || matchAny(opt.Exclude, rel) {
			return nil
		}
		code = append(code, codeFile{abs: path, dir: filepath.Dir(path)})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Component roots: the indexed folder, folders with a licence or a
	// manifest, and the packages a Python distribution installed.
	compByRoot := map[string]int{}
	var rootsInOrder []string
	for p, info := range dirs {
		if p == root || info.isComponentRoot() {
			rootsInOrder = append(rootsInOrder, p)
		}
	}
	for p := range owners {
		if _, ok := dirs[p]; ok || strings.HasSuffix(p, ".py") {
			rootsInOrder = append(rootsInOrder, p)
		}
	}
	sort.Strings(rootsInOrder)
	rootsInOrder = dedupe(rootsInOrder)

	var kept []string
	var created []int               // components made from this root
	distComps := map[string][]int{} // dist-info folder -> its components
	for _, p := range rootsInOrder {
		if dis := owners[p]; len(dis) > 0 {
			// One component per distribution, even when several install
			// into the same folder (a namespace package such as google/).
			sort.Strings(dis)
			for _, di := range dis {
				m := distInfo[di]
				c := &Component{Root: p, Name: m.name, Version: m.version, URL: m.url, PURL: m.purl()}
				if m.license != "" {
					c.License, c.LicenseFrom = NormalizeLicense(m.license), filepath.Base(di)+"/METADATA"
				}
				expr, from, text, cr := licenseFromFiles(distLic[di])
				if c.License == "" && expr != "" {
					c.License, c.LicenseFrom = expr, filepath.Base(di)+"/"+from
				}
				if c.License == "" {
					c.License = LicenseNone
				}
				c.LicenseText, c.Copyrights = text, cr
				compByRoot[p] = len(ix.Components)
				distComps[di] = append(distComps[di], len(ix.Components))
				created = append(created, len(ix.Components))
				ix.Components = append(ix.Components, c)
			}
			if len(dis) > 1 {
				// Files no RECORD claims could belong to any of them, so
				// they take all their licences.
				var names, lics []string
				for _, di := range dis {
					c := ix.Components[distComps[di][len(distComps[di])-1]]
					names = append(names, c.Name)
					lics = append(lics, c.License)
				}
				c := &Component{Root: p, Name: relSlash(filepath.Dir(root), p), License: joinLicenses(lics),
					LicenseFrom: "shared by " + strings.Join(names, ", ")}
				compByRoot[p] = len(ix.Components)
				created = append(created, len(ix.Components))
				ix.Components = append(ix.Components, c)
			}
			kept = append(kept, p)
			continue
		}
		c := &Component{Root: p}
		info := dirs[p]
		m := info.manifest
		if m != nil {
			c.Name, c.Version, c.URL, c.PURL = m.name, m.version, m.url, m.purl()
			if m.license != "" {
				if l := NormalizeLicense(m.license); licenseCategory(l) != CatUnknown {
					c.License, c.LicenseFrom = l, manifestFile(m.kind)
				}
			}
		}
		var licPaths []string
		for _, f := range info.licenseFiles {
			licPaths = append(licPaths, filepath.Join(p, f))
		}
		if m != nil && m.licFile != "" {
			licPaths = append(licPaths, filepath.Join(p, m.licFile))
		}
		if info.reuseDir {
			sub, _ := os.ReadDir(filepath.Join(p, "LICENSES"))
			for _, s := range sub {
				if !s.IsDir() {
					licPaths = append(licPaths, filepath.Join(p, "LICENSES", s.Name()))
				}
			}
		}
		expr, from, text, cr := licenseFromFiles(dedupe(licPaths))
		if p != root && expr == "" && len(cr) == 0 && !info.reuseDir && (m == nil || m.name == "") {
			// Its only "licence file" is a page about the licence, such
			// as docs/license.rst: the folder is part of the one above.
			continue
		}
		if c.License == "" && expr != "" {
			c.License, c.LicenseFrom = expr, from
		}
		c.LicenseText, c.Copyrights = text, cr
		if info.noticeFile != "" {
			c.NoticeText = strings.TrimSpace(readSmall(filepath.Join(p, info.noticeFile), 16<<10))
		}
		if c.URL == "" {
			c.URL = gitRemote(p)
		}
		if c.Name == "" {
			c.Name = relSlash(filepath.Dir(root), p)
			// Go's vendor folders are laid out by import path.
			if i := strings.LastIndex("/"+c.Name, "/vendor/"); i >= 0 && i+len("vendor/") < len(c.Name) {
				c.Name = c.Name[i+len("vendor/"):]
			}
			if c.Version == "" {
				if b := filepath.Base(p); strings.Contains(b, "@") {
					c.Name = strings.TrimSuffix(c.Name, b) + b[:strings.LastIndex(b, "@")]
					c.Version = b[strings.LastIndex(b, "@")+1:]
				}
			}
		}
		if p == root {
			if opt.Name != "" {
				c.Name = opt.Name
			}
			if opt.Version != "" {
				c.Version = opt.Version
			}
		}
		compByRoot[p] = len(ix.Components)
		created = append(created, len(ix.Components))
		ix.Components = append(ix.Components, c)
		kept = append(kept, p)
	}
	rootsInOrder = kept

	// A folder without a licence of its own takes the licence of the folder
	// above it, unless it sits in a dependency folder such as node_modules.
	for _, p := range rootsInOrder {
		c := ix.Components[compByRoot[p]]
		if c.License != "" {
			continue
		}
		for q := filepath.Dir(p); within(q, root) && q != filepath.Dir(q); q = filepath.Dir(q) {
			if dependencyBoundary(relSlash(q, p)) {
				break
			}
			if i, ok := compByRoot[q]; ok && ix.Components[i].License != "" {
				up := ix.Components[i]
				c.License, c.LicenseFrom = up.License, "inherited from "+up.Name
				if len(c.Copyrights) == 0 {
					c.Copyrights = up.Copyrights
				}
				if c.LicenseText == "" {
					c.LicenseText = up.LicenseText
				}
				if c.URL == "" {
					c.URL = up.URL
				}
				break
			}
			if q == root {
				break
			}
		}
		if c.License == "" {
			c.License, c.LicenseFrom = LicenseNone, ""
		}
	}
	if opt.License != "" {
		l := NormalizeLicense(opt.License)
		for _, i := range created {
			ix.Components[i].License, ix.Components[i].LicenseFrom = l, "--license"
		}
	}

	// Each file belongs to the distribution whose RECORD lists it, or else
	// to the nearest component root above it.
	var out []pendingFile
	for _, f := range code {
		ci := -1
		if di, ok := recordOwner[f.abs]; ok {
			for _, k := range distComps[di] {
				if r := ix.Components[k].Root; r == f.abs || within(f.abs, r) {
					ci = k
					break
				}
			}
			if ci < 0 && len(distComps[di]) > 0 {
				ci = distComps[di][0]
			}
		}
		if _, ok := owners[f.abs]; ok && ci < 0 {
			ci = compByRoot[f.abs]
		}
		for q := f.dir; ci < 0; q = filepath.Dir(q) {
			if i, ok := compByRoot[q]; ok {
				ci = i
				break
			}
			if q == root || q == filepath.Dir(q) {
				ci = compByRoot[root]
				break
			}
		}
		c := ix.Components[ci]
		rel := relSlash(c.Root, f.abs)
		if c.Root == f.abs {
			rel = filepath.Base(f.abs)
		}
		out = append(out, pendingFile{abs: f.abs, comp: ci, rel: rel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].abs < out[j].abs })
	return out, nil
}

func manifestFile(kind string) string {
	switch kind {
	case "go":
		return "go.mod"
	case "npm":
		return "package.json"
	case "cargo":
		return "Cargo.toml"
	case "composer":
		return "composer.json"
	case "pypi":
		return "pyproject.toml"
	}
	return kind
}

// fingerprintSourceFile reads and fingerprints one file. Its own licence
// comes from the comments at its top (see fileLicense), unless the user
// named the licence of all the code with --license.
func fingerprintSourceFile(p pendingFile, compLicense string, headers bool) fileResult {
	data, skip := readSource(p.abs)
	if skip != "" {
		return fileResult{skip: skip}
	}
	sx := languageFor(p.abs)
	toks := tokenize(data, sx)
	fps := winnow(toks, sx)
	sf := &SourceFile{Comp: p.comp, Path: p.rel, Abs: p.abs, Lines: countLines(data), Tokens: len(toks)}
	head := leadingComments(data, sx, 16<<10)
	if headers {
		// An SPDX tag is the file's own explicit statement of its licence.
		if id := spdxTagLicense(head); id != "" {
			if !strings.EqualFold(id, NormalizeLicense(compLicense)) {
				sf.License = id
			}
		} else {
			sf.License = fileLicense(compLicense, headerLicense(head), head)
		}
	}
	sf.Copyrights = copyrightLines(head, 5)
	h := sha256.New()
	var buf [8]byte
	for _, t := range toks {
		binary.LittleEndian.PutUint64(buf[:], t.exact)
		h.Write(buf[:])
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return fileResult{sf: sf, fps: fps, content: sum, tokens: len(toks)}
}

// dropEmptyComponents removes components that ended up with no files, and
// renumbers the rest.
func (ix *Index) dropEmptyComponents() {
	used := make([]bool, len(ix.Components))
	for _, f := range ix.Files {
		used[f.Comp] = true
		for _, a := range f.Also {
			used[a] = true
		}
	}
	remap := make([]int, len(ix.Components))
	var kept []*Component
	for i, c := range ix.Components {
		if used[i] {
			remap[i] = len(kept)
			kept = append(kept, c)
		} else {
			remap[i] = -1
		}
	}
	for _, f := range ix.Files {
		f.Comp = remap[f.Comp]
		for k, a := range f.Also {
			f.Also[k] = remap[a]
		}
	}
	ix.Components = kept
	// Keys must be unique: they name components in notices.
	count := map[string]int{}
	for _, c := range kept {
		c.Key = c.Name
		if c.Version != "" {
			c.Key += "@" + c.Version
		}
		count[c.Key]++
	}
	for _, c := range kept {
		if count[c.Key] > 1 {
			c.Key += " (" + ix.shortRoot(c.Root) + ")"
		}
	}
}

// shortRoot names a component folder from the indexed folder it is in, as
// in "coyim/vendor/github.com/sirupsen/logrus", so keys and reports don't
// depend on where the code sits on this machine.
func (ix *Index) shortRoot(p string) string {
	best := ""
	for _, r := range ix.Roots {
		if within(p, r) && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return filepath.ToSlash(p)
	}
	return relSlash(filepath.Dir(best), p)
}

// filterCommon sorts the fingerprints and drops those that appear in so
// many different components that they are boilerplate, not a sign of
// copying.
func (ix *Index) filterCommon(maxDF int) {
	sort.Slice(ix.fps, func(i, j int) bool {
		a, b := ix.fps[i], ix.fps[j]
		if a.hash != b.hash {
			return a.hash < b.hash
		}
		if a.file != b.file {
			return a.file < b.file
		}
		return a.pos < b.pos
	})
	names := map[string]bool{}
	for _, c := range ix.Components {
		names[c.Name] = true
	}
	if maxDF == 0 {
		maxDF = -1
		if len(names) >= 20 {
			maxDF = max(8, len(names)/20)
		}
	}
	ix.MaxDF = maxDF
	if maxDF < 0 {
		return
	}
	out := ix.fps[:0]
	for i := 0; i < len(ix.fps); {
		j := i
		for j < len(ix.fps) && ix.fps[j].hash == ix.fps[i].hash {
			j++
		}
		run := ix.fps[i:j]
		distinctNames := map[string]bool{}
		for _, f := range run {
			distinctNames[ix.Components[ix.Files[f.file].Comp].Name] = true
			if len(distinctNames) > maxDF {
				break
			}
		}
		if len(distinctNames) > maxDF || len(run) > 2000 {
			ix.Dropped += len(run)
		} else {
			out = append(out, run...)
		}
		i = j
	}
	ix.fps = out
}

// lookup returns the stored fingerprints with a hash.
func (ix *Index) lookup(h uint64) []indexFP {
	i := sort.Search(len(ix.fps), func(i int) bool { return ix.fps[i].hash >= h })
	j := i
	for j < len(ix.fps) && ix.fps[j].hash == h {
		j++
	}
	return ix.fps[i:j]
}

// Fingerprints returns how many fingerprints the index holds.
func (ix *Index) Fingerprints() int { return len(ix.fps) }

const indexMagic = "REMIXWARE-INDEX\n"

// Save writes the index to a file.
func (ix *Index) Save(path string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.WriteString(indexMagic); err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	bw := bufio.NewWriterSize(zw, 1<<20)
	head, err := json.Marshal(ix.indexHeader)
	if err != nil {
		return err
	}
	var vb [binary.MaxVarintLen64]byte
	bw.Write(vb[:binary.PutUvarint(vb[:], uint64(len(head)))])
	bw.Write(head)
	bw.Write(vb[:binary.PutUvarint(vb[:], uint64(len(ix.fps)))])
	var rec [indexRecordSize]byte
	for _, p := range ix.fps {
		binary.LittleEndian.PutUint64(rec[0:], p.hash)
		binary.LittleEndian.PutUint32(rec[8:], p.exact)
		binary.LittleEndian.PutUint32(rec[12:], p.file)
		binary.LittleEndian.PutUint32(rec[16:], p.pos)
		binary.LittleEndian.PutUint32(rec[20:], p.line)
		binary.LittleEndian.PutUint32(rec[24:], p.end)
		bw.Write(rec[:])
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	ix.Path = path
	return os.Rename(tmp, path)
}

// LoadIndex reads an index written by Save.
func LoadIndex(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Deflate packs at most about 1,000 bytes into one, so the file's size
	// caps how much it can hold: a small file that claims a lot is damaged,
	// and memory is set aside only for what it can really hold.
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	capacity := uint64(st.Size())*1100 + 1<<16
	magic := make([]byte, len(indexMagic))
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != indexMagic {
		return nil, fmt.Errorf("%s is not a Remixware index (make one with remixware index)", path)
	}
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: damaged index (%v)", path, err)
	}
	br := bufio.NewReaderSize(zr, 1<<20)
	n, err := binary.ReadUvarint(br)
	if err != nil || n > 1<<30 || n > capacity {
		return nil, fmt.Errorf("%s: damaged index header", path)
	}
	// Read what is there rather than trust the length: a damaged or made-up
	// file can't make Remixware allocate memory it doesn't need.
	head, err := io.ReadAll(io.LimitReader(br, int64(n)))
	if err != nil || uint64(len(head)) != n {
		return nil, fmt.Errorf("%s: damaged index header", path)
	}
	ix := &Index{Path: path}
	if err := json.Unmarshal(head, &ix.indexHeader); err != nil {
		return nil, fmt.Errorf("%s: damaged index header: %v", path, err)
	}
	if ix.Format != 1 || ix.K != kgramSize || ix.W != windowSize || ix.MinDistinct != minDistinct || ix.Norm != normVersion {
		return nil, fmt.Errorf("%s was made by %s with other settings: rebuild it with remixware index", path, ix.Tool)
	}
	if err := ix.validate(); err != nil {
		return nil, fmt.Errorf("%s: damaged index (%v)", path, err)
	}
	count, err := binary.ReadUvarint(br)
	if err != nil || count > capacity/indexRecordSize {
		return nil, fmt.Errorf("%s: damaged index", path)
	}
	ix.fps = make([]indexFP, 0, count)
	var rec [indexRecordSize]byte
	nfiles := uint32(len(ix.Files))
	for i := uint64(0); i < count; i++ {
		if _, err := io.ReadFull(br, rec[:]); err != nil {
			return nil, fmt.Errorf("%s: damaged index (it ends early)", path)
		}
		p := indexFP{
			hash:  binary.LittleEndian.Uint64(rec[0:]),
			exact: binary.LittleEndian.Uint32(rec[8:]),
			file:  binary.LittleEndian.Uint32(rec[12:]),
			pos:   binary.LittleEndian.Uint32(rec[16:]),
			line:  binary.LittleEndian.Uint32(rec[20:]),
			end:   binary.LittleEndian.Uint32(rec[24:]),
		}
		if p.file >= nfiles {
			return nil, fmt.Errorf("%s: damaged index (bad file number)", path)
		}
		if f := ix.Files[p.file]; int64(p.pos)+kgramSize > int64(f.Tokens) || p.line < 1 || p.line > p.end || int64(p.end) > int64(max(f.Lines, 1)) {
			return nil, fmt.Errorf("%s: damaged index (a fingerprint outside its file)", path)
		}
		ix.fps = append(ix.fps, p)
	}
	// Reading to the end makes gzip check its checksum.
	if _, err := br.ReadByte(); err != io.EOF {
		return nil, fmt.Errorf("%s: damaged index (checksum failed or data after the end)", path)
	}
	for i := 1; i < len(ix.fps); i++ {
		if ix.fps[i].hash < ix.fps[i-1].hash {
			return nil, fmt.Errorf("%s: damaged index (not sorted)", path)
		}
	}
	return ix, nil
}

// validate checks that the index header holds together: every component
// and file is there, and every number that points at one points inside.
func (ix *Index) validate() error {
	for i, c := range ix.Components {
		if c == nil {
			return fmt.Errorf("component %d is missing", i)
		}
	}
	for i, f := range ix.Files {
		if f == nil {
			return fmt.Errorf("file %d is missing", i)
		}
		if f.Comp < 0 || f.Comp >= len(ix.Components) {
			return fmt.Errorf("file %d has a bad component number", i)
		}
		for _, a := range f.Also {
			if a < 0 || a >= len(ix.Components) {
				return fmt.Errorf("file %d has a bad component number", i)
			}
		}
		if f.Tokens < 0 || f.Lines < 0 {
			return fmt.Errorf("file %d has a bad size", i)
		}
	}
	return nil
}

// readable reports whether a scan may read an indexed file again: it must
// be an absolute, clean path to a source file inside one of the folders
// the index was made from. An index made elsewhere, or made up, can't
// point a scan at other files on this machine.
func (ix *Index) readable(p string) bool {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || languageFor(p) == nil {
		return false
	}
	for _, r := range ix.Roots {
		if filepath.IsAbs(r) && p != filepath.Clean(r) && within(p, filepath.Clean(r)) {
			return true
		}
	}
	return false
}

// LicenseCounts returns how many components carry each licence.
func (ix *Index) LicenseCounts() map[string]int {
	out := map[string]int{}
	for _, c := range ix.Components {
		out[c.License]++
	}
	return out
}
