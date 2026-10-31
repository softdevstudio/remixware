package remixware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This file reads the lockfiles the dependency gate compares: go.mod and
// go.sum, package-lock.json, requirements files and poetry.lock. Every
// reader fails loudly on what it can't make sense of: a gate that skipped
// a broken lockfile would pass whatever the lockfile hides.

// Ecosystems, named as in package URLs.
const (
	EcoGo   = "golang"
	EcoNPM  = "npm"
	EcoPyPI = "pypi"
)

// lockPackage is one package version a lockfile names.
type lockPackage struct {
	eco, name, version string
	direct             bool
	lockfile           string
	scripts            int8 // scriptsUnknown, scriptsNo or scriptsYes
	registry           bool // comes from the ecosystem's public registry
	pinned             bool // version is one exact release
}

const (
	scriptsUnknown int8 = iota
	scriptsNo
	scriptsYes
)

// maxLockfile is the largest lockfile read. A package-lock.json of a big
// monorepo is tens of megabytes; past this, the gate stops with an error
// instead of reading part of it.
const maxLockfile = 128 << 20

// depTree is a set of files to read lockfiles from: a folder on disk or a
// git revision.
type depTree interface {
	files() ([]string, error)              // slash paths relative to the project
	read(rel string) ([]byte, bool, error) // contents, whether the file exists
	label() string
}

// depSkipDirs are folders the lockfile search never enters: installed
// packages and version control, not the project's own lockfiles.
var depSkipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, ".tox": true, "site-packages": true, ".hg": true, ".svn": true}

func skippedDepPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if depSkipDirs[part] {
			return true
		}
	}
	return false
}

type dirTree struct{ root string }

// DepsDir is a folder whose lockfiles the dependency gate reads.
func DepsDir(root string) (DepsTree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return DepsTree{}, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return DepsTree{}, err
	}
	if !st.IsDir() {
		return DepsTree{}, fmt.Errorf("%s is not a folder", root)
	}
	if r, err := realPath(abs); err == nil {
		abs = r
	}
	return DepsTree{t: dirTree{abs}}, nil
}

func (d dirTree) label() string { return d.root }

func (d dirTree) files() ([]string, error) {
	var out []string
	err := filepath.WalkDir(d.root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			if p == d.root {
				return err
			}
			return nil
		}
		if p == d.root {
			return nil
		}
		if e.IsDir() {
			if depSkipDirs[e.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if e.Type().IsRegular() {
			out = append(out, relSlash(d.root, p))
		}
		return nil
	})
	return out, err
}

func (d dirTree) read(rel string) ([]byte, bool, error) {
	if !safeRel(rel) {
		return nil, false, fmt.Errorf("%s points outside the project", rel)
	}
	p := filepath.Join(d.root, filepath.FromSlash(rel))
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", rel)
	}
	if st.Size() > maxLockfile {
		return nil, true, fmt.Errorf("%s is larger than %d MB", rel, maxLockfile>>20)
	}
	b, err := os.ReadFile(p)
	return b, true, err
}

type gitTree struct {
	dir, rev string
	blobs    map[string]string // project-relative path -> blob id
	sizes    map[string]int64
}

// DepsGit is a git revision (a branch, tag or commit) of the repository
// that holds project, whose lockfiles the dependency gate reads with
// git ls-tree and git cat-file. Nothing is fetched.
func DepsGit(project, rev string) (DepsTree, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return DepsTree{}, fmt.Errorf("bad git revision %q", rev)
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return DepsTree{}, err
	}
	prefix, err := git(abs, "rev-parse", "--show-prefix")
	if err != nil {
		return DepsTree{}, err
	}
	pre := strings.TrimSpace(string(prefix))
	if _, err := git(abs, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
		return DepsTree{}, fmt.Errorf("git revision %q not found in %s (in CI, fetch it first, for example with fetch-depth: 0)", rev, project)
	}
	out, err := git(abs, "ls-tree", "-r", "-z", "-l", "--full-tree", rev)
	if err != nil {
		return DepsTree{}, err
	}
	t := gitTree{dir: abs, rev: rev, blobs: map[string]string{}, sizes: map[string]int64{}}
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return DepsTree{}, fmt.Errorf("git ls-tree gave an entry it shouldn't: %q", entry)
		}
		f := strings.Fields(string(meta))
		if len(f) != 4 || f[1] != "blob" || f[0] == "120000" {
			continue // folders of submodules, symbolic links
		}
		rel := string(name)
		if pre != "" {
			if !strings.HasPrefix(rel, pre) {
				continue
			}
			rel = rel[len(pre):]
		}
		t.blobs[rel] = f[2]
		var size int64
		fmt.Sscan(f[3], &size)
		t.sizes[rel] = size
	}
	return DepsTree{t: t}, nil
}

func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out, nil
}

func (g gitTree) label() string { return g.rev }

func (g gitTree) files() ([]string, error) {
	var out []string
	for rel := range g.blobs {
		if !skippedDepPath(rel) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (g gitTree) read(rel string) ([]byte, bool, error) {
	if !safeRel(rel) {
		return nil, false, fmt.Errorf("%s points outside the project", rel)
	}
	id, ok := g.blobs[rel]
	if !ok {
		return nil, false, nil
	}
	if g.sizes[rel] > maxLockfile {
		return nil, true, fmt.Errorf("%s at %s is larger than %d MB", rel, g.rev, maxLockfile>>20)
	}
	b, err := git(g.dir, "cat-file", "blob", id)
	return b, true, err
}

// safeRel reports whether a slash path stays inside the tree.
func safeRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") || filepath.IsAbs(rel) {
		return false
	}
	c := path.Clean(rel)
	return c != ".." && !strings.HasPrefix(c, "../") && c == rel
}

// DepsTree is where the dependency gate reads lockfiles from: a folder
// (DepsDir) or a git revision (DepsGit).
type DepsTree struct{ t depTree }

// ReadFile reads a file of the tree, given as a slash path relative to
// the project, and says whether it exists.
func (t DepsTree) ReadFile(rel string) ([]byte, bool, error) {
	if t.t == nil {
		return nil, false, nil
	}
	return t.t.read(rel)
}

// Label is the folder or revision.
func (t DepsTree) Label() string {
	if t.t == nil {
		return ""
	}
	return t.t.label()
}

// Lockfile kinds.
const (
	kindGoMod       = "go.mod"
	kindNPM         = "package-lock.json"
	kindRequirement = "requirements.txt"
	kindPoetry      = "poetry.lock"
	kindUnsupported = "unsupported"
)

var requirementsName = regexp.MustCompile(`^requirements.*\.txt$`)

// unsupportedLockfiles are lockfiles the gate doesn't read. When one of
// them changes, the change is not-checked: nothing passes unread.
var unsupportedLockfiles = map[string]bool{"yarn.lock": true, "pnpm-lock.yaml": true, "bun.lock": true, "bun.lockb": true,
	"Pipfile.lock": true, "pdm.lock": true, "uv.lock": true, "Cargo.lock": true, "Gemfile.lock": true, "composer.lock": true,
	"packages.lock.json": true, "gradle.lockfile": true, "Package.resolved": true, "mix.lock": true, "pubspec.lock": true,
	"conan.lock": true, "deno.lock": true}

// lockfileKind says what kind of lockfile a path is, or "".
func lockfileKind(rel string) string {
	base := path.Base(rel)
	switch {
	case base == "go.mod":
		return kindGoMod
	case base == "package-lock.json", base == "npm-shrinkwrap.json":
		return kindNPM
	case base == "poetry.lock":
		return kindPoetry
	case requirementsName.MatchString(base):
		return kindRequirement
	case strings.HasSuffix(base, ".txt") && path.Base(path.Dir(rel)) == "requirements":
		return kindRequirement
	case unsupportedLockfiles[base]:
		return kindUnsupported
	}
	return ""
}

// findLockfiles lists the lockfiles in a tree, leaving out ignored paths.
func findLockfiles(t depTree, ignore []string) ([]string, error) {
	all, err := t.files()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range all {
		if skippedDepPath(rel) || matchAny(ignore, rel) {
			continue
		}
		if lockfileKind(rel) != "" {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

// readLockfile reads one lockfile from a tree. Sibling files (go.sum,
// package.json, pyproject.toml, included requirements) come from the same
// tree.
func readLockfile(t depTree, rel string, data []byte) ([]lockPackage, []string, error) {
	var pkgs []lockPackage
	var sources []string
	var err error
	switch lockfileKind(rel) {
	case kindGoMod:
		sum, _, serr := t.read(path.Join(path.Dir(rel), "go.sum"))
		if serr != nil {
			return nil, nil, serr
		}
		pkgs, err = parseGoMod(data, sum)
	case kindNPM:
		pj, _, perr := t.read(path.Join(path.Dir(rel), "package.json"))
		if perr != nil {
			return nil, nil, perr
		}
		pkgs, err = parseNPMLock(data, pj)
	case kindRequirement:
		pkgs, err = parseRequirements(t, rel, data, 0, &sources)
	case kindPoetry:
		py, _, perr := t.read(path.Join(path.Dir(rel), "pyproject.toml"))
		if perr != nil {
			return nil, nil, perr
		}
		pkgs, err = parsePoetryLock(data, py)
	default:
		return nil, nil, fmt.Errorf("%s: not a lockfile Remixware reads", rel)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s at %s: %v", rel, t.label(), err)
	}
	for i := range pkgs {
		pkgs[i].lockfile = rel
	}
	return pkgs, sources, nil
}

// --- Go -------------------------------------------------------------------

type goReplace struct{ oldVer, newPath, newVer string }

// parseGoMod reads go.mod's requirements (direct, or "// indirect") and
// go.sum's code hashes, the rest of the module graph. A replacement by
// another module counts as that module; one by a local folder is the
// project's own code and drops the module.
func parseGoMod(mod, sum []byte) ([]lockPackage, error) {
	var reqs []goReq
	replaces := map[string][]goReplace{}
	block := ""
	sawModule := false
	for n, raw := range strings.Split(string(mod), "\n") {
		line := strings.TrimSpace(raw)
		comment := ""
		if i := strings.Index(line, "//"); i >= 0 {
			comment = strings.TrimSpace(line[i+2:])
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			if err := goDirective(block, line, comment, n+1, &reqs, replaces); err != nil {
				return nil, err
			}
			continue
		}
		verb, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		if verb == "module" {
			sawModule = true
			continue
		}
		if rest == "(" {
			block = verb
			continue
		}
		if strings.HasSuffix(verb, "(") {
			return nil, fmt.Errorf("go.mod line %d: %q", n+1, raw)
		}
		if err := goDirective(verb, rest, comment, n+1, &reqs, replaces); err != nil {
			return nil, err
		}
	}
	if block != "" {
		return nil, fmt.Errorf("go.mod: %s ( block never closed", block)
	}
	if !sawModule {
		return nil, fmt.Errorf("go.mod has no module line")
	}
	seen := map[string]int{}
	var out []lockPackage
	add := func(p, v string, direct bool) {
		for _, r := range replaces[p] {
			if r.oldVer != "" && r.oldVer != v {
				continue
			}
			if r.newVer == "" {
				return // a local folder: the project's own code
			}
			p, v = r.newPath, r.newVer
			break
		}
		k := p + "@" + v
		if i, ok := seen[k]; ok {
			out[i].direct = out[i].direct || direct
			return
		}
		seen[k] = len(out)
		out = append(out, lockPackage{eco: EcoGo, name: p, version: v, direct: direct, scripts: scriptsNo,
			registry: true, pinned: true})
	}
	for _, r := range reqs {
		add(r.path, r.ver, r.direct)
	}
	for n, raw := range strings.Split(string(sum), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 || !strings.HasPrefix(f[1], "v") || !goSumHash.MatchString(f[2]) {
			return nil, fmt.Errorf("go.sum line %d: %q", n+1, raw)
		}
		if strings.HasSuffix(f[1], "/go.mod") {
			continue // only the module's go.mod was read, not its code
		}
		add(f[0], f[1], false)
	}
	return out, nil
}

var goSumHash = regexp.MustCompile(`^h[0-9]+:[A-Za-z0-9+/]+=*$`)

type goReq struct {
	path, ver string
	direct    bool
}

func goDirective(verb, rest, comment string, n int, reqs *[]goReq, replaces map[string][]goReplace) error {
	switch verb {
	case "require":
		f := strings.Fields(rest)
		if len(f) != 2 || !strings.HasPrefix(f[1], "v") {
			return fmt.Errorf("go.mod line %d: require %q", n, rest)
		}
		indirect := comment == "indirect" || strings.HasPrefix(comment, "indirect;")
		*reqs = append(*reqs, goReq{unquoteGo(f[0]), f[1], !indirect})
	case "replace":
		left, right, ok := strings.Cut(rest, "=>")
		if !ok {
			return fmt.Errorf("go.mod line %d: replace %q", n, rest)
		}
		l, r := strings.Fields(left), strings.Fields(right)
		if len(l) < 1 || len(l) > 2 || len(r) < 1 || len(r) > 2 {
			return fmt.Errorf("go.mod line %d: replace %q", n, rest)
		}
		rep := goReplace{newPath: unquoteGo(r[0])}
		if len(l) == 2 {
			rep.oldVer = l[1]
		}
		if len(r) == 2 {
			rep.newVer = r[1]
		} else if !goLocalPath(rep.newPath) {
			return fmt.Errorf("go.mod line %d: replace %q names a module without a version", n, rest)
		}
		p := unquoteGo(l[0])
		replaces[p] = append(replaces[p], rep)
	}
	return nil
}

func goLocalPath(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || filepath.IsAbs(p) ||
		p == "." || p == ".."
}

func unquoteGo(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '`') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// --- npm ------------------------------------------------------------------

type npmLockFile struct {
	LockfileVersion *int                    `json:"lockfileVersion"`
	Packages        map[string]npmLockEntry `json:"packages"`
	Dependencies    map[string]npmV1Entry   `json:"dependencies"`
}

type npmLockEntry struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Link                 bool              `json:"link"`
	HasInstallScript     bool              `json:"hasInstallScript"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

type npmV1Entry struct {
	Version      string                `json:"version"`
	Resolved     string                `json:"resolved"`
	Dependencies map[string]npmV1Entry `json:"dependencies"`
}

// NPMRegistry is the public npm registry. Packages resolved from
// elsewhere aren't looked up there.
const NPMRegistry = "https://registry.npmjs.org"

func (e npmLockEntry) allDeps() []map[string]string {
	return []map[string]string{e.Dependencies, e.DevDependencies, e.OptionalDependencies, e.PeerDependencies}
}

// parseNPMLock reads package-lock.json and npm-shrinkwrap.json, versions
// 1 to 3. Versions 2 and 3 record install scripts (hasInstallScript),
// version 1 doesn't.
func parseNPMLock(data, packageJSON []byte) ([]lockPackage, error) {
	var lf npmLockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	if lf.LockfileVersion == nil {
		return nil, fmt.Errorf("no lockfileVersion")
	}
	direct := map[string]bool{}
	var out []lockPackage
	switch v := *lf.LockfileVersion; v {
	case 2, 3:
		if lf.Packages == nil {
			return nil, fmt.Errorf("lockfileVersion %d without packages", v)
		}
		// The root package and workspace folders name the direct ones.
		for k, e := range lf.Packages {
			if k == "" || !strings.Contains(k, "node_modules/") {
				for _, m := range e.allDeps() {
					for name := range m {
						direct[name] = true
					}
				}
			}
		}
		keys := make([]string, 0, len(lf.Packages))
		for k := range lf.Packages {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := lf.Packages[k]
			i := strings.LastIndex(k, "node_modules/")
			if i < 0 || e.Link {
				continue // the root, workspace folders and links to them
			}
			name := e.Name
			if name == "" {
				name = k[i+len("node_modules/"):]
			}
			if !npmName.MatchString(name) {
				return nil, fmt.Errorf("package %q has a name npm doesn't allow", k)
			}
			if e.Version == "" {
				return nil, fmt.Errorf("package %q has no version", k)
			}
			p := npmPackage(name, e.Version, e.Resolved)
			p.direct = k == "node_modules/"+name && direct[name]
			p.scripts = scriptsNo
			if e.HasInstallScript {
				p.scripts = scriptsYes
			}
			out = append(out, p)
		}
	case 1:
		if len(packageJSON) > 0 {
			var root npmLockEntry
			if err := json.Unmarshal(packageJSON, &root); err != nil {
				return nil, fmt.Errorf("package.json: %v", err)
			}
			for _, m := range root.allDeps() {
				for name := range m {
					direct[name] = true
				}
			}
		}
		var walk func(deps map[string]npmV1Entry, top bool) error
		walk = func(deps map[string]npmV1Entry, top bool) error {
			names := make([]string, 0, len(deps))
			for n := range deps {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, name := range names {
				e := deps[name]
				if !npmName.MatchString(name) {
					return fmt.Errorf("package %q has a name npm doesn't allow", name)
				}
				if e.Version == "" {
					return fmt.Errorf("package %q has no version", name)
				}
				p := npmPackage(name, e.Version, e.Resolved)
				// Without package.json, every top-level entry counts as direct.
				p.direct = top && (direct[name] || len(packageJSON) == 0)
				out = append(out, p)
				if err := walk(e.Dependencies, false); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(lf.Dependencies, true); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("lockfileVersion %d, which Remixware doesn't read (it reads 1, 2 and 3)", v)
	}
	return out, nil
}

var npmName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-zA-Z0-9._~-][a-zA-Z0-9._~-]*$`)

var npmVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func npmPackage(name, version, resolved string) lockPackage {
	p := lockPackage{eco: EcoNPM, name: name, version: version, scripts: scriptsUnknown}
	p.pinned = npmVersion.MatchString(version)
	// npm leaves resolved out for some registry packages; a version that's
	// a URL, a path or an alias isn't from the registry.
	p.registry = p.pinned && (resolved == "" || strings.HasPrefix(resolved, NPMRegistry+"/"))
	return p
}

// --- Python -----------------------------------------------------------------

var pyNameSep = regexp.MustCompile(`[-_.]+`)

// pyName normalises a Python package name (PEP 503).
func pyName(s string) string { return strings.ToLower(pyNameSep.ReplaceAllString(s, "-")) }

var pyRequirement = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(\[[^\]]*\])?\s*(.*)$`)

var pyPinned = regexp.MustCompile(`^===?\s*([A-Za-z0-9][A-Za-z0-9.+!_-]*)$`)

// parseRequirements reads a pip requirements file. Every requirement is
// direct, except where pip-compile's "# via" comments say a package came
// in through others. -r includes are read from the same tree.
//
// Options that change where pip looks for packages (--index-url,
// --extra-index-url, --find-links, --trusted-host, --no-index) go to
// sources: a change can point pip at another index without touching a
// single requirement.
func parseRequirements(t depTree, rel string, data []byte, depth int, sources *[]string) ([]lockPackage, error) {
	if depth > 10 {
		return nil, fmt.Errorf("-r includes go deeper than 10 files")
	}
	// Join continued lines, and keep comment lines for "# via".
	var lines []string
	var lineNo []int
	cont := ""
	start := 0
	for n, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if cont == "" {
			start = n + 1
		}
		if strings.HasSuffix(raw, "\\") && !strings.HasPrefix(strings.TrimSpace(raw), "#") {
			cont += strings.TrimSuffix(raw, "\\") + " "
			continue
		}
		lines = append(lines, cont+raw)
		lineNo = append(lineNo, start)
		cont = ""
	}
	if cont != "" {
		lines = append(lines, cont)
		lineNo = append(lineNo, start)
	}
	var out []lockPackage
	last := -1 // index in out of the last requirement, for "# via"
	viaSeen := map[int]bool{}
	viaDirect := map[int]bool{}
	inVia := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			c := strings.TrimSpace(strings.TrimPrefix(trim, "#"))
			if strings.HasPrefix(c, "via") && last >= 0 && strings.HasPrefix(line, " ") {
				viaSeen[last] = true
				inVia = true
				c = strings.TrimSpace(strings.TrimPrefix(c, "via"))
			} else if !inVia || !strings.HasPrefix(line, " ") {
				inVia = false
				continue
			}
			if strings.HasPrefix(c, "-r ") {
				viaDirect[last] = true
			}
			continue
		}
		inVia = false
		if j := commentStart(trim); j >= 0 {
			trim = strings.TrimSpace(trim[:j])
		}
		if trim == "" {
			continue
		}
		// --hash options ride on requirement lines.
		var kept []string
		for _, f := range strings.Fields(trim) {
			if !strings.HasPrefix(f, "--hash") {
				kept = append(kept, f)
			}
		}
		trim = strings.Join(kept, " ")
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "-") {
			opt, val := splitOption(trim)
			switch opt {
			case "-r", "--requirement":
				inc := path.Join(path.Dir(rel), val)
				if val == "" || strings.HasPrefix(val, "/") || !safeRel(inc) {
					return nil, fmt.Errorf("line %d: -r %q points outside the project", lineNo[i], val)
				}
				b, ok, err := t.read(inc)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("line %d: -r %s: no such file", lineNo[i], val)
				}
				sub, err := parseRequirements(t, inc, b, depth+1, sources)
				if err != nil {
					return nil, fmt.Errorf("%s: %v", inc, err)
				}
				out = append(out, sub...)
			case "-e", "--editable":
				if p, ok := pyURLRequirement(val); ok {
					out = append(out, p)
				} else if !pyLocal(val) {
					return nil, fmt.Errorf("line %d: can't read %q", lineNo[i], trim)
				}
			case "-i", "--index-url", "--extra-index-url", "-f", "--find-links", "--trusted-host", "--no-index":
				long := map[string]string{"-i": "--index-url", "-f": "--find-links"}[opt]
				if long == "" {
					long = opt
				}
				*sources = append(*sources, strings.TrimSpace(long+" "+val))
			case "-c", "--constraint", "--pre", "--no-binary", "--only-binary", "--prefer-binary", "--require-hashes",
				"--use-feature", "--config-settings", "--global-option", "--hash":
				// Options that change how pip installs, not what or from where.
			default:
				return nil, fmt.Errorf("line %d: unknown option %q", lineNo[i], opt)
			}
			continue
		}
		if pyLocal(trim) {
			continue
		}
		if p, ok := pyURLRequirement(trim); ok {
			out = append(out, p)
			last = len(out) - 1
			continue
		}
		m := pyRequirement.FindStringSubmatch(trim)
		if m == nil {
			return nil, fmt.Errorf("line %d: can't read %q", lineNo[i], trim)
		}
		spec := m[3]
		if j := strings.Index(spec, ";"); j >= 0 {
			spec = spec[:j]
		}
		spec = strings.TrimSpace(spec)
		p := lockPackage{eco: EcoPyPI, name: pyName(m[1]), direct: true, registry: true}
		switch {
		case strings.HasPrefix(spec, "@"):
			p.version = strings.TrimSpace(spec[1:])
			p.registry = false
		case pyPinned.MatchString(spec):
			p.version = pyPinned.FindStringSubmatch(spec)[1]
			p.pinned = true
		case spec == "" || pySpec.MatchString(spec):
			p.version = strings.ReplaceAll(spec, " ", "")
		default:
			return nil, fmt.Errorf("line %d: can't read the version in %q", lineNo[i], trim)
		}
		out = append(out, p)
		last = len(out) - 1
	}
	for i := range out {
		if viaSeen[i] {
			out[i].direct = viaDirect[i]
		}
	}
	return out, nil
}

var pySpec = regexp.MustCompile(`^(\(?\s*(~=|==|!=|<=|>=|<|>|===)\s*[A-Za-z0-9.*+!_-]+\s*,?\s*\)?)+$`)

// commentStart finds a # that starts a comment: at the start of a line or
// after whitespace (a # inside a URL fragment is not a comment).
func commentStart(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return i
		}
	}
	return -1
}

func splitOption(s string) (string, string) {
	if i := strings.IndexAny(s, " \t="); i >= 0 {
		return s[:i], strings.TrimSpace(strings.TrimLeft(s[i:], " \t="))
	}
	return s, ""
}

func pyLocal(s string) bool {
	return strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "file:") ||
		strings.HasPrefix(s, "~")
}

var pyEgg = regexp.MustCompile(`[#&]egg=([A-Za-z0-9][A-Za-z0-9._-]*)`)

// pyURLRequirement reads a requirement given as a URL (a VCS checkout or an
// archive): it comes from outside the registry.
func pyURLRequirement(s string) (lockPackage, bool) {
	if !strings.Contains(s, "://") || strings.Contains(s, " @ ") || strings.HasPrefix(s, "file:") {
		return lockPackage{}, false
	}
	url := strings.Fields(s)[0]
	name := url
	if m := pyEgg.FindStringSubmatch(url); m != nil {
		name = pyName(m[1])
	}
	return lockPackage{eco: EcoPyPI, name: name, version: url, direct: true}, true
}

var tomlFile = regexp.MustCompile(`file\s*=\s*"([^"]+)"`)

// parsePoetryLock reads poetry.lock's packages. A package whose files hold
// no wheel builds from source when installed, which runs its build code:
// that counts as an install script. pyproject.toml names the direct ones.
func parsePoetryLock(data, pyproject []byte) ([]lockPackage, error) {
	type pkg struct {
		name, version, source string
		files                 []string
		line                  int
	}
	var pkgs []*pkg
	metaFiles := map[string][]string{}
	var cur *pkg
	table := ""
	inArray := false
	var collect func(string) // where the file names of an open files array go
	sawMeta := false
	for n, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if inArray {
			// Inside a multi-line array: items or its end, nothing else.
			if !strings.HasPrefix(line, "{") && !strings.HasPrefix(line, `"`) && !strings.HasPrefix(line, "'") &&
				!strings.HasPrefix(line, "]") {
				return nil, fmt.Errorf("line %d: %q inside an array", n+1, raw)
			}
			if collect != nil {
				for _, m := range tomlFile.FindAllStringSubmatch(line, -1) {
					collect(m[1])
				}
			}
			if strings.HasPrefix(line, "]") {
				inArray, collect = false, nil
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: %q", n+1, raw)
			}
			table = strings.Trim(line, "[] ")
			switch {
			case line == "[[package]]":
				cur = &pkg{line: n + 1}
				pkgs = append(pkgs, cur)
			case table == "metadata":
				sawMeta = true
				cur = nil
			case strings.HasPrefix(table, "package."):
			default:
				cur = nil
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: %q", n+1, raw)
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		val = strings.TrimSpace(val)
		str := ""
		if strings.HasPrefix(val, `"`) {
			if end := strings.Index(val[1:], `"`); end >= 0 {
				str = val[1 : end+1]
			}
		}
		opens := strings.HasPrefix(val, "[") && !strings.HasSuffix(val, "]")
		var into func(string)
		switch {
		case table == "package" && cur != nil:
			switch key {
			case "name":
				cur.name = str
			case "version":
				cur.version = str
			case "files":
				p := cur
				into = func(f string) { p.files = append(p.files, f) }
			}
		case table == "package.source" && cur != nil && key == "type":
			cur.source = str
		case table == "metadata.files":
			name := pyName(key)
			into = func(f string) { metaFiles[name] = append(metaFiles[name], f) }
		}
		if into != nil {
			for _, m := range tomlFile.FindAllStringSubmatch(val, -1) {
				into(m[1])
			}
		}
		if opens {
			inArray, collect = true, into
		}
	}
	if inArray {
		return nil, fmt.Errorf("an array is never closed")
	}
	if len(pkgs) == 0 && !sawMeta {
		return nil, fmt.Errorf("no [[package]] tables and no [metadata]: not a poetry.lock")
	}
	direct := pyprojectDirect(string(pyproject))
	var out []lockPackage
	for _, p := range pkgs {
		if p.name == "" || p.version == "" {
			return nil, fmt.Errorf("the [[package]] at line %d has no name or version", p.line)
		}
		if p.source == "directory" {
			continue // a folder in the project: its own code
		}
		name := pyName(p.name)
		lp := lockPackage{eco: EcoPyPI, name: name, version: p.version, direct: direct[name],
			registry: p.source == "", pinned: true}
		files := p.files
		if len(files) == 0 {
			files = metaFiles[name]
		}
		switch {
		case p.source == "git":
			lp.scripts = scriptsYes
		case len(files) > 0:
			lp.scripts = scriptsYes
			for _, f := range files {
				if strings.HasSuffix(strings.ToLower(f), ".whl") {
					lp.scripts = scriptsNo
					break
				}
			}
		}
		out = append(out, lp)
	}
	return out, nil
}

// pyprojectDirect lists the packages pyproject.toml asks for: Poetry's
// dependency tables and PEP 621's dependencies and optional-dependencies.
func pyprojectDirect(text string) map[string]bool {
	out := map[string]bool{}
	table := ""
	inArray := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if inArray {
			for _, m := range pyQuoted.FindAllStringSubmatch(line, -1) {
				if r := pyRequirement.FindStringSubmatch(m[1]); r != nil {
					out[pyName(r[1])] = true
				}
			}
			if strings.Contains(line, "]") {
				inArray = false
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(line, "[] ")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		val = strings.TrimSpace(val)
		poetryTable := table == "tool.poetry.dependencies" || table == "tool.poetry.dev-dependencies" ||
			strings.HasPrefix(table, "tool.poetry.group.") && strings.HasSuffix(table, ".dependencies")
		switch {
		case poetryTable && key != "python":
			out[pyName(key)] = true
		case table == "project" && key == "dependencies", table == "project.optional-dependencies":
			for _, m := range pyQuoted.FindAllStringSubmatch(val, -1) {
				if r := pyRequirement.FindStringSubmatch(m[1]); r != nil {
					out[pyName(r[1])] = true
				}
			}
			if strings.HasPrefix(val, "[") && !strings.Contains(val, "]") {
				inArray = true
			}
		}
	}
	return out
}

var pyQuoted = regexp.MustCompile(`["']([^"']+)["']`)
