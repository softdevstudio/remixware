package remixware

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"go/build/constraint"
	"go/parser"
	gotoken "go/token"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
)

// What a crate or a Go module does when it's built isn't in the lockfile,
// so --online reads it from the published archive: a crate's .crate file
// from static.crates.io, a module's zip from the Go module proxy.

// CratesDownloadURL is where crates.io serves .crate files.
const CratesDownloadURL = "https://static.crates.io/crates"

// GoProxyURL is the public Go module proxy.
const GoProxyURL = "https://proxy.golang.org"

// maxArchive bounds a downloaded archive, and maxArchiveFile a file read
// from one.
const (
	maxArchive     = 64 << 20
	maxArchiveFile = 16 << 20
)

func (r *Registry) getBytes(u string) ([]byte, error) {
	c := r.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "remixware/"+Version)
	r.Fetched++
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking %s: %v", shownURL(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, fmt.Errorf("%s: %w", shownURL(u), errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", shownURL(u), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", shownURL(u), err)
	}
	if len(data) > maxArchive {
		return nil, fmt.Errorf("%s is larger than %d MB", shownURL(u), maxArchive>>20)
	}
	return data, nil
}

// crateFacts reads a crate's .crate file: it runs code when it's compiled
// if it has a build script (build.rs at its root, which Cargo builds unless
// the manifest says build = false, or another file named by build) or is a
// procedural macro ([lib] proc-macro = true).
func (r *Registry) crateFacts(name, version string) (VersionFacts, error) {
	base := strings.TrimSuffix(r.CratesURL, "/")
	if base == "" {
		base = CratesDownloadURL
	}
	if !crateName.MatchString(name) || !crateVersion.MatchString(version) {
		return VersionFacts{}, fmt.Errorf("crate %s %s: not a name and version crates.io has", name, version)
	}
	// Build metadata ("300.3.1+3.3.1") is written %2B, so no server can take
	// the + for a space.
	ev := strings.ReplaceAll(version, "+", "%2B")
	data, err := r.getBytes(fmt.Sprintf("%s/%s/%s-%s.crate", base, name, name, ev))
	if err != nil {
		return VersionFacts{}, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return VersionFacts{}, fmt.Errorf("crate %s %s: %v", name, version, err)
	}
	tr := tar.NewReader(io.LimitReader(zr, 1<<30))
	root := name + "-" + version + "/"
	var manifest []byte
	hasBuildRS := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return VersionFacts{}, fmt.Errorf("crate %s %s: %v", name, version, err)
		}
		switch {
		case strings.EqualFold(h.Name, root+"build.rs"):
			// Cargo finds Build.rs too on a filesystem that ignores case.
			hasBuildRS = true
		case h.Name == root+"Cargo.toml":
			manifest, err = io.ReadAll(io.LimitReader(tr, maxArchiveFile+1))
			if err != nil {
				return VersionFacts{}, fmt.Errorf("crate %s %s: %v", name, version, err)
			}
			if len(manifest) > maxArchiveFile {
				return VersionFacts{}, fmt.Errorf("crate %s %s: Cargo.toml is larger than %d MB", name, version, maxArchiveFile>>20)
			}
		}
	}
	if manifest == nil {
		return VersionFacts{}, fmt.Errorf("crate %s %s has no Cargo.toml", name, version)
	}
	build, procMacro := crateManifestBuild(string(manifest))
	runs := procMacro || build == "yes" || (build == "" && hasBuildRS)
	return VersionFacts{InstallScripts: runs}, nil
}

// crateManifestBuild reads what a published Cargo.toml says about building:
// build is "yes" for build = "<file>" or build = true, "no" for
// build = false, "" when the manifest doesn't say; procMacro is
// proc-macro = true in [lib]. The manifest is read as TOML, so comments,
// inline tables and dotted keys count as Cargo reads them. One the TOML
// reader can't read is searched line by line instead, and any proc-macro
// or build key there counts as yes: the safe side.
func crateManifestBuild(m string) (build string, procMacro bool) {
	if doc, err := parseTOML([]byte(m)); err == nil {
		if pkg, ok := doc["package"].(map[string]any); ok {
			switch v := pkg["build"].(type) {
			case tomlRaw:
				if v == "false" {
					build = "no"
				} else {
					build = "yes"
				}
			case nil:
			default:
				build = "yes" // a file name
			}
		}
		if lib, ok := doc["lib"].(map[string]any); ok {
			for _, k := range []string{"proc-macro", "proc_macro"} {
				if v, ok := lib[k].(tomlRaw); ok && v == "true" {
					procMacro = true
				}
			}
		}
		return build, procMacro
	}
	for _, line := range strings.Split(m, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		low := strings.ReplaceAll(line, "_", "-")
		if strings.Contains(low, "proc-macro") && strings.Contains(low, "true") {
			procMacro = true
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasSuffix(strings.TrimSpace(strings.Trim(k, " \"'")), "build") {
			if strings.HasPrefix(strings.TrimSpace(v), "false") {
				if build == "" {
					build = "no"
				}
			} else {
				build = "yes"
			}
		}
	}
	return build, procMacro
}

// goFacts reads a module's zip from the Go module proxy: native code is a
// Go file that imports "C" (cgo) or a .syso object, outside test files and
// testdata, vendor and nested modules' folders.
func (r *Registry) goFacts(module, version string) (VersionFacts, error) {
	base := strings.TrimSuffix(r.GoProxy, "/")
	if base == "" {
		base = GoProxyURL
	}
	ep, err := escapeModulePath(module)
	if err != nil {
		return VersionFacts{}, err
	}
	ev, err := escapeModulePath(version)
	if err != nil {
		return VersionFacts{}, err
	}
	data, err := r.getBytes(base + "/" + ep + "/@v/" + ev + ".zip")
	if err != nil {
		return VersionFacts{}, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return VersionFacts{}, fmt.Errorf("module %s %s: %v", module, version, err)
	}
	prefix := module + "@" + version + "/"
	// Folders holding their own go.mod are other modules.
	nested := map[string]bool{}
	for _, f := range zr.File {
		if rel, ok := strings.CutPrefix(f.Name, prefix); ok && path.Base(rel) == "go.mod" && path.Dir(rel) != "." {
			nested[path.Dir(rel)] = true
		}
	}
	inNested := func(rel string) bool {
		for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
			if nested[d] {
				return true
			}
		}
		return false
	}
	for _, f := range zr.File {
		rel, ok := strings.CutPrefix(f.Name, prefix)
		if !ok || inNested(rel) {
			continue
		}
		skip := false
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		for _, part := range strings.Split(dir, "/") {
			if part != "" && (part == "testdata" || part == "vendor" || strings.HasPrefix(part, "_") || strings.HasPrefix(part, ".")) {
				skip = true
			}
		}
		if skip {
			continue
		}
		switch {
		case strings.HasSuffix(rel, ".syso"), strings.HasSuffix(rel, ".swig"), strings.HasSuffix(rel, ".swigcxx"):
			// Prebuilt objects; SWIG files make go build run SWIG and the
			// C or C++ compiler.
			if buildsOnMainPlatform(path.Base(rel), nil) {
				return VersionFacts{NativeCode: true}, nil
			}
		case strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
			rc, err := f.Open()
			if err != nil {
				return VersionFacts{}, fmt.Errorf("module %s %s: %v", module, version, err)
			}
			src, err := io.ReadAll(io.LimitReader(rc, maxArchiveFile+1))
			rc.Close()
			if err != nil {
				return VersionFacts{}, fmt.Errorf("module %s %s: %v", module, version, err)
			}
			if len(src) > maxArchiveFile {
				// A cut file can't be read for its imports, and skipping it
				// could miss cgo.
				return VersionFacts{}, fmt.Errorf("module %s %s: %s is larger than %d MB", module, version, rel, maxArchiveFile>>20)
			}
			if !buildsOnMainPlatform(path.Base(rel), src) {
				// Built only elsewhere (zsyscall_aix_ppc.go, //go:build hurd),
				// or never (//go:build ignore, a generator's input).
				continue
			}
			pf, err := parser.ParseFile(gotoken.NewFileSet(), rel, src, parser.ImportsOnly)
			if err != nil {
				continue // a file Go wouldn't build either
			}
			for _, im := range pf.Imports {
				if im.Path.Value == `"C"` {
					return VersionFacts{NativeCode: true}, nil
				}
			}
		}
	}
	return VersionFacts{}, nil
}

// escapeModulePath writes a module path or version as the module proxy
// wants it: each upper-case letter as ! and its lower case.
func escapeModulePath(s string) (string, error) {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '!' || c > unicode.MaxASCII || c < ' ':
			return "", fmt.Errorf("%q isn't a module path or version the proxy serves", s)
		case unicode.IsUpper(c):
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(c))
		default:
			b.WriteRune(c)
		}
	}
	return b.String(), nil
}

// mainPlatforms are the platforms native code is looked for on: a cgo
// file golang.org/x/sys builds only for AIX shouldn't flag every program
// that imports it.
var mainPlatforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"}}

var (
	knownOS = map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
		"hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true,
		"openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true}
	knownArch = map[string]bool{"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true,
		"arm64": true, "arm64be": true, "loong64": true, "mips": true, "mipsle": true, "mips64": true,
		"mips64le": true, "mips64p32": true, "mips64p32le": true, "ppc": true, "ppc64": true, "ppc64le": true,
		"riscv": true, "riscv64": true, "s390": true, "s390x": true, "sparc": true, "sparc64": true, "wasm": true}
)

// buildsOnMainPlatform says whether Go would build a file (name and, for a
// Go file, its source) for one of mainPlatforms with cgo on: the GOOS and
// GOARCH in its name (x_aix_ppc.go) and its build constraint must allow it.
// Tags Remixware can't know (a project's own) count as set; ignore and
// gccgo don't.
func buildsOnMainPlatform(name string, src []byte) bool {
	// As go/build reads a name: up to its first dot, without _test.
	base, _, _ := strings.Cut(name, ".")
	parts := strings.Split(strings.TrimSuffix(base, "_test"), "_")
	var fileOS, fileArch string
	if n := len(parts); n >= 2 {
		last, prev := parts[n-1], parts[n-2]
		switch {
		case n >= 3 && knownOS[prev] && knownArch[last]:
			fileOS, fileArch = prev, last
		case knownOS[last]:
			fileOS = last
		case knownArch[last]:
			fileArch = last
		}
	}
	exprs := buildConstraints(src)
	for _, pl := range mainPlatforms {
		goos, goarch := pl[0], pl[1]
		if (fileOS != "" && fileOS != goos) || (fileArch != "" && fileArch != goarch) {
			continue
		}
		tag := func(t string) bool {
			switch {
			case t == goos || t == goarch || t == "cgo" || t == "gc" || strings.HasPrefix(t, "go1."):
				return true
			case t == "unix":
				return goos != "windows"
			case t == "ignore" || t == "gccgo" || knownOS[t] || knownArch[t]:
				return false
			}
			return true
		}
		ok := true
		for _, e := range exprs {
			if !e.Eval(tag) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// buildConstraints reads a Go file's build constraints as go/build does:
// from the line comments before the package clause (not from inside a
// /* */ block), the //go:build line alone when there is one, otherwise
// every // +build line.
func buildConstraints(src []byte) []constraint.Expr {
	if src == nil {
		return nil
	}
	var goBuild constraint.Expr
	var plus, pending []constraint.Expr
	inBlock, afterBlock := false, false
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				inBlock = false
				if strings.TrimSpace(line[i+2:]) != "" {
					break // code after the comment: the header is over
				}
			}
			continue
		}
		switch {
		case line == "":
			// A // +build line counts only with a blank line after it.
			plus, pending = append(plus, pending...), nil
			continue
		case strings.HasPrefix(line, "/*"):
			afterBlock = true
			if i := strings.Index(line[2:], "*/"); i < 0 {
				inBlock = true
			} else if strings.TrimSpace(line[2+i+2:]) != "" {
				return keepBuild(goBuild, plus)
			}
			continue
		case strings.HasPrefix(line, "//"):
			if constraint.IsGoBuild(line) && goBuild == nil {
				if e, err := constraint.Parse(line); err == nil {
					goBuild = e
				}
			} else if constraint.IsPlusBuild(line) && !afterBlock {
				// go/build ignores // +build lines after a /* */ comment.
				if e, err := constraint.Parse(line); err == nil {
					pending = append(pending, e)
				}
			}
			continue
		}
		break // the package clause, or anything else, ends the header
	}
	return keepBuild(goBuild, plus)
}

func keepBuild(goBuild constraint.Expr, plus []constraint.Expr) []constraint.Expr {
	if goBuild != nil {
		return []constraint.Expr{goBuild}
	}
	return plus
}
