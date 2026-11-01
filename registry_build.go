package remixware

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
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
	maxArchiveFile = 1 << 20
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
	data, err := r.getBytes(fmt.Sprintf("%s/%s/%s-%s.crate", base, name, name, version))
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
		switch h.Name {
		case root + "build.rs":
			hasBuildRS = true
		case root + "Cargo.toml":
			manifest, err = io.ReadAll(io.LimitReader(tr, maxArchiveFile))
			if err != nil {
				return VersionFacts{}, fmt.Errorf("crate %s %s: %v", name, version, err)
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
// [lib] proc-macro = true. Published manifests are normalised by Cargo,
// one key per line.
func crateManifestBuild(m string) (build string, procMacro bool) {
	section := ""
	sc := bufio.NewScanner(strings.NewReader(m))
	sc.Buffer(make([]byte, 64<<10), maxArchiveFile)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.Trim(strings.TrimSpace(k), `"'`)
		v = strings.TrimSpace(v)
		switch {
		case section == "package" && k == "build":
			if v == "false" {
				build = "no"
			} else {
				build = "yes"
			}
		case section == "lib" && (k == "proc-macro" || k == "proc_macro"):
			procMacro = v == "true"
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
		case strings.HasSuffix(rel, ".syso"):
			return VersionFacts{NativeCode: true}, nil
		case strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
			rc, err := f.Open()
			if err != nil {
				return VersionFacts{}, fmt.Errorf("module %s %s: %v", module, version, err)
			}
			src, err := io.ReadAll(io.LimitReader(rc, maxArchiveFile))
			rc.Close()
			if err != nil {
				return VersionFacts{}, fmt.Errorf("module %s %s: %v", module, version, err)
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
