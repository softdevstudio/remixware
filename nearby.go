package remixware

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Credit is often kept with the code rather than in a notices file at the
// project root: a vendored library keeps its LICENSE in its own folder
// (typer/_click/LICENSE.txt, pip/_vendor/requests/LICENSE), and a project
// keeps a second licence file at its root for code it took
// (LICENSE.xmpp-client). nearbyNotices reads the licence and notice files
// in the project's folders and says whether one of them credits a copy's
// source. The root's own notice files are readNotices' job (a policy's
// notice_files replaces them), so at the root it reads licence files only.
type nearbyNotices struct {
	project string
	exclude []string                 // patterns the walk leaves out, as a scan does
	folders map[string][]*nearbyFile // by slash-separated folder, "." for the root
	all     []*nearbyFile            // every licence and notice file, once walked
	walked  bool
	byKey   map[string]string   // what credits a source wherever its copy is
	texts   map[string][]string // a source's licence texts, then its NOTICE text, normalised
	// Reading stops at a budget, one for the folders above copies and one
	// for the walk, so the walk for one copy can't use up what the folders
	// of another need. Past them, and past the walk's file cap, files go
	// unread and are counted, once each.
	chainBudget, walkBudget int64
	unread                  map[string]bool
}

type nearbyFile struct {
	rel     string // slash-separated, relative to the project
	license bool   // a licence file (LICENSE, COPYING...), not a notice
	cred    string // the text as credited compares it
	norm    string // normText of the text, for whole licence and NOTICE texts
}

const (
	// nearbyBudget caps what each of the two readings takes in: a project
	// with thousands of vendored folders still gets an answer, and the
	// files left unread are counted in the results.
	nearbyBudget = 64 << 20
	// nearbyMaxFiles caps how many licence and notice files the walk keeps.
	nearbyMaxFiles = 20000
	// nearbyMaxFile is how much of one file is read, as for notice files at
	// the root.
	nearbyMaxFile = 4 << 20
)

func newNearbyNotices(project string, exclude []string) *nearbyNotices {
	if real, err := filepath.EvalSymlinks(project); err == nil {
		project = real
	}
	return &nearbyNotices{project: project, exclude: exclude, folders: map[string][]*nearbyFile{},
		byKey: map[string]string{}, texts: map[string][]string{}, chainBudget: nearbyBudget, walkBudget: nearbyBudget,
		unread: map[string]bool{}}
}

// read reads one licence or notice file against a budget, or returns nil
// when it isn't text or the budget is spent.
func (n *nearbyNotices) read(rel string, license bool, budget *int64) *nearbyFile {
	if *budget <= 0 {
		n.unread[rel] = true
		return nil
	}
	t := readSmall(filepath.Join(n.project, filepath.FromSlash(rel)), nearbyMaxFile)
	if t == "" || strings.ContainsRune(t[:min(len(t), 8<<10)], 0) {
		return nil
	}
	*budget -= int64(len(t))
	// An entry remixware notice left for the user to finish holds the
	// licence text already, but it doesn't credit anything yet.
	low := finishedNotices(strings.ToLower(t))
	return &nearbyFile{rel: rel, license: license,
		cred: strings.Join(strings.Fields(oneLine(low)), " "), norm: normText(textBlock(low))}
}

// noticeKind says whether a file in a folder is a licence file (license),
// a notice file (notice), or neither. The root's notice files and its
// licenses/ and notices/ folders are left to readNotices.
func noticeKind(dir, name string) (license, notice bool) {
	parent := path.Base(dir)
	inNoticeDir := dir != "." && (noticeDirs[strings.ToLower(parent)] || parent == "LICENSES")
	switch {
	case inNoticeDir && path.Dir(dir) == ".":
		return false, false
	case isLicenseFileName(name):
		return true, false
	case dir == "." || !noticeText(name):
		// A source file called attribution.go or third_party_split.py is
		// code, a Python package called licenses (packaging.licenses)
		// holds code, and ThirdParty.cmake lists dependencies: none of
		// them is a notice.
		return false, false
	case isNoticeFileName(name) || noticeName.MatchString(name) || inNoticeDir:
		return false, true
	}
	return false, false
}

// chain returns the folders from the copied file's own up to the project
// root, nearest first, the root as ".". A folder that is a symbolic link, or
// isn't a folder, ends the chain below it: nothing is read through a link.
func (n *nearbyNotices) chain(rel string) []string {
	var dirs []string // root first
	for d := path.Dir(path.Clean("/" + rel)); ; d = path.Dir(d) {
		dirs = append([]string{strings.TrimPrefix(d, "/")}, dirs...)
		if d == "/" {
			break
		}
	}
	var out []string
	for _, d := range dirs {
		if d == "" {
			d = "."
		} else if st, err := os.Lstat(filepath.Join(n.project, filepath.FromSlash(d))); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			break
		}
		out = append([]string{d}, out...)
	}
	return out
}

// folder reads the licence and notice files of one folder, once, with those
// in a licenses/ or notices/ folder inside it.
func (n *nearbyNotices) folder(dir string) []*nearbyFile {
	if files, ok := n.folders[dir]; ok {
		return files
	}
	var files []*nearbyFile
	join := func(a, b string) string {
		if a == "." {
			return b
		}
		return a + "/" + b
	}
	entries, _ := os.ReadDir(filepath.Join(n.project, filepath.FromSlash(dir)))
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() {
			if license, notice := noticeKind(dir, name); license || notice {
				if f := n.read(join(dir, name), license, &n.chainBudget); f != nil {
					files = append(files, f)
				}
			}
			continue
		}
		if sub := join(dir, name); e.IsDir() && (noticeDirs[strings.ToLower(name)] || name == "LICENSES") {
			inner, _ := os.ReadDir(filepath.Join(n.project, filepath.FromSlash(sub)))
			for _, s := range inner {
				if license, notice := noticeKind(sub, s.Name()); (license || notice) && s.Type().IsRegular() {
					if f := n.read(sub+"/"+s.Name(), license, &n.chainBudget); f != nil {
						files = append(files, f)
					}
				}
			}
		}
	}
	n.folders[dir] = files
	return files
}

// walk returns every licence and notice file in the project, once, read in
// the walk's order against its own budget. It leaves out what a scan leaves
// out: the folders it skips, where a licence in vendor/ or node_modules/
// belongs to a dependency the package manager may update or drop (a copy
// inside such a folder is still credited by its chain), and the paths
// --exclude and the policy's ignore list name. Symbolic links aren't
// followed.
func (n *nearbyNotices) walk() []*nearbyFile {
	if n.walked {
		return n.all
	}
	n.walked = true
	filepath.WalkDir(n.project, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != n.project {
				return filepath.SkipDir
			}
			return nil
		}
		rel := relSlash(n.project, p)
		if d.IsDir() {
			if p != n.project && (skipDirs[d.Name()] || skipIndexDirs[d.Name()] || matchAny(n.exclude, rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || matchAny(n.exclude, rel) {
			return nil
		}
		license, notice := noticeKind(path.Dir(rel), d.Name())
		switch {
		case !license && !notice:
		case len(n.all) >= nearbyMaxFiles:
			n.unread[rel] = true
		default:
			if f := n.read(rel, license, &n.walkBudget); f != nil {
				n.all = append(n.all, f)
			}
		}
		return nil
	})
	return n.all
}

// creditedBy reports whether a file credits a source the way a notices
// file does: by its full copyright line, its name with a version or a URL,
// or its package URL.
func (f *nearbyFile) creditedBy(needles []string) bool {
	for _, nd := range needles {
		if containsWord(f.cred, nd) {
			return true
		}
	}
	return false
}

// licenseTextPart splits the licence texts of a component with several
// licence files, as licenseFromFiles joins them.
var licenseTextPart = regexp.MustCompile(`\n\n----- [^\n]+ -----\n\n`)

// credit returns the licence or notice file that credits a copy's source, or
// "", and whether that file is kept with the copy: in its folder or a folder
// above it. A file credits a source by the source's full copyright line, its
// name with a version or a URL, or its package URL, wherever in the project
// it is: a licence notice that ships with the project ships with the copy.
//
// A source whose licence names no copyright holder, as the bare Apache and
// Mozilla texts don't, can only be told apart by where its licence is kept:
// its own licence text, whole, in the copy's folder or the nearest folder
// above it with a licence file, short of the root, as a vendored library
// keeps it. A text at the root, or in a folder further up, may be any other
// library's under the same licence. A source that ships a NOTICE needs its
// NOTICE text there too; textOnly names the file that keeps the licence text
// when the NOTICE text is missing.
func (n *nearbyNotices) credit(rel string, s Source, texts *ComponentTexts) (by string, withCopy bool, textOnly string) {
	needles := creditNeedles(s)
	chain := n.chain(rel)
	for _, d := range chain {
		for _, f := range n.folder(d) {
			if f.creditedBy(needles) {
				return f.rel, true, ""
			}
		}
	}
	key := strings.Join(needles, "\x00")
	by, ok := n.byKey[key]
	if !ok {
		for _, f := range n.walk() {
			if f.creditedBy(needles) {
				by = f.rel
				break
			}
		}
		n.byKey[key] = by
	}
	if by != "" {
		return by, false, ""
	}
	if texts == nil || strings.TrimSpace(texts.LicenseText) == "" {
		return "", false, ""
	}
	for _, c := range s.Copyrights {
		if len(c) >= 15 {
			return "", false, "" // the copyright line is what has to be there
		}
	}
	var own []*nearbyFile // the nearest folder below the root with a licence file
	for _, d := range chain {
		if d == "." {
			break
		}
		files := n.folder(d)
		for _, f := range files {
			if f.license {
				own = files
				break
			}
		}
		if own != nil {
			break
		}
	}
	if own == nil {
		return "", false, ""
	}
	// The source's texts, normalised once: normText compares licence texts
	// without case, punctuation or line breaks. The last one is the NOTICE
	// text, "" when there is none.
	parts, ok := n.texts[s.Key]
	if !ok {
		for _, p := range licenseTextPart.Split(strings.TrimSpace(texts.LicenseText), -1) {
			parts = append(parts, normText(textBlock(p)))
		}
		nt := normText(textBlock(texts.NoticeText))
		if len(strings.TrimSpace(nt)) < 15 {
			nt = ""
		}
		parts = append(parts, nt)
		n.texts[s.Key] = parts
	}
	find := func(t string) string {
		for _, f := range own {
			if strings.Contains(f.norm, t) {
				return f.rel
			}
		}
		return ""
	}
	var first string
	for _, t := range parts[:len(parts)-1] {
		w := ""
		if len(strings.TrimSpace(t)) >= 100 { // a near-empty text can't credit anything
			w = find(t)
		}
		if w == "" {
			return "", false, ""
		}
		if first == "" {
			first = w
		}
	}
	if nt := parts[len(parts)-1]; nt != "" && find(nt) == "" {
		return "", false, first
	}
	return first, true, ""
}
