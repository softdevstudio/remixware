package remixware

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Component is one body of third-party code in an index: a Go module, an npm
// package, a crate, a Python distribution, or a folder with its own licence.
type Component struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Root        string   `json:"root"`
	License     string   `json:"license"`
	LicenseFrom string   `json:"license_from,omitempty"`
	Copyrights  []string `json:"copyrights,omitempty"`
	URL         string   `json:"url,omitempty"`
	PURL        string   `json:"purl,omitempty"`
	LicenseText string   `json:"license_text,omitempty"`
	NoticeText  string   `json:"notice_text,omitempty"`
}

// dirInfo is what one folder says about itself.
type dirInfo struct {
	path         string
	licenseFiles []string // file names
	noticeFile   string
	reuseDir     bool // has a REUSE-style LICENSES/ folder
	manifest     *manifest
}

type manifest struct {
	kind     string // go, npm, cargo, composer, pypi
	name     string
	version  string
	license  string
	licFile  string
	url      string
	topLevel []string // Python: packages this distribution installs
	record   []string // Python: the files this distribution installed, from its RECORD
}

func (m *manifest) purl() string {
	if m == nil || m.name == "" {
		return ""
	}
	v := ""
	if m.version != "" {
		v = "@" + m.version
	}
	switch m.kind {
	case "go":
		return "pkg:golang/" + m.name + v
	case "npm":
		return "pkg:npm/" + strings.ReplaceAll(m.name, "@", "%40") + v
	case "cargo":
		return "pkg:cargo/" + m.name + v
	case "composer":
		return "pkg:composer/" + m.name + v
	case "pypi":
		return "pkg:pypi/" + strings.ReplaceAll(strings.ToLower(m.name), "_", "-") + v
	}
	return ""
}

var licenseFileName = regexp.MustCompile(`(?i)^(un)?licen[cs]e([-_.].*)?$|^copying([-_.].*)?$|^mit[-_]licen[cs]e(\..*)?$|^copyright(\.(md|txt))?$`)

func isLicenseFileName(name string) bool {
	if !licenseFileName.MatchString(name) {
		return false
	}
	low := strings.ToLower(name)
	// Not code files that happen to be called license.go or licence.py.
	if l := languageFor(low); l != nil {
		return false
	}
	return !strings.HasSuffix(low, ".json") && !strings.HasSuffix(low, ".html") && !strings.HasSuffix(low, ".xml")
}

func isNoticeFileName(name string) bool {
	low := strings.ToLower(name)
	return low == "notice" || low == "notice.txt" || low == "notice.md"
}

// readDirInfo looks for licence files and manifests in one folder.
func readDirInfo(dir string, entries []os.DirEntry) *dirInfo {
	d := &dirInfo{path: dir}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name == "LICENSES" {
				d.reuseDir = true
			}
			continue
		}
		switch {
		case isLicenseFileName(name):
			d.licenseFiles = append(d.licenseFiles, name)
		case isNoticeFileName(name):
			d.noticeFile = name
		}
		if d.manifest == nil {
			d.manifest = readManifest(dir, name)
		}
	}
	sort.Strings(d.licenseFiles)
	return d
}

func (d *dirInfo) isComponentRoot() bool {
	return len(d.licenseFiles) > 0 || d.reuseDir || (d.manifest != nil && d.manifest.name != "")
}

func readManifest(dir, name string) *manifest {
	path := filepath.Join(dir, name)
	switch name {
	case "go.mod":
		return readGoMod(path, dir)
	case "package.json":
		return readPackageJSON(path)
	case "Cargo.toml":
		return readCargoToml(path)
	case "composer.json":
		return readComposerJSON(path)
	case "pyproject.toml":
		return readPyproject(path)
	}
	return nil
}

// readSmall reads up to max bytes of a regular file. Symbolic links and
// special files read as empty: a licence or notice file that links to
// /etc/passwd, or to /dev/zero, must not end up in an index or a notice.
func readSmall(path string, max int64) string {
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	for n < int(max) {
		m, err := f.Read(buf[n:])
		n += m
		if err != nil || m == 0 {
			break
		}
	}
	return string(buf[:n])
}

func readGoMod(path, dir string) *manifest {
	text := readSmall(path, 64<<10)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") || strings.HasPrefix(line, "module\t") {
			mod := strings.TrimSpace(line[len("module"):])
			if i := strings.Index(mod, "//"); i >= 0 {
				mod = strings.TrimSpace(mod[:i])
			}
			mod = strings.Trim(mod, "\"`")
			m := &manifest{kind: "go", name: mod}
			// In the module cache a folder is named module@version.
			if base := filepath.Base(dir); strings.Contains(base, "@") {
				m.version = base[strings.LastIndex(base, "@")+1:]
			}
			m.url = goModuleURL(mod)
			return m
		}
	}
	return nil
}

var majorSuffix = regexp.MustCompile(`/v[0-9]+$`)

func goModuleURL(mod string) string {
	first := strings.SplitN(mod, "/", 2)[0]
	if !strings.Contains(first, ".") {
		return ""
	}
	switch first {
	case "github.com", "gitlab.com", "bitbucket.org", "codeberg.org":
		parts := strings.Split(majorSuffix.ReplaceAllString(mod, ""), "/")
		if len(parts) > 3 {
			parts = parts[:3]
		}
		return "https://" + strings.Join(parts, "/")
	}
	return "https://" + mod
}

func cleanRepoURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(u, "git+")
	u = strings.TrimSuffix(u, ".git")
	switch {
	case strings.HasPrefix(u, "github:"):
		u = "https://github.com/" + strings.TrimPrefix(u, "github:")
	case strings.HasPrefix(u, "gitlab:"):
		u = "https://gitlab.com/" + strings.TrimPrefix(u, "gitlab:")
	case strings.HasPrefix(u, "git://"):
		u = "https://" + strings.TrimPrefix(u, "git://")
	case strings.HasPrefix(u, "ssh://git@"):
		u = "https://" + strings.TrimPrefix(u, "ssh://git@")
	case strings.HasPrefix(u, "git@"):
		u = "https://" + strings.Replace(strings.TrimPrefix(u, "git@"), ":", "/", 1)
	case !strings.Contains(u, "://") && strings.Count(u, "/") == 1 && !strings.HasPrefix(u, "."):
		u = "https://github.com/" + u
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}

func readPackageJSON(path string) *manifest {
	var p struct {
		Name       string                  `json:"name"`
		Version    string                  `json:"version"`
		License    json.RawMessage         `json:"license"`
		Licenses   []struct{ Type string } `json:"licenses"`
		Repository json.RawMessage         `json:"repository"`
	}
	if err := json.Unmarshal([]byte(readSmall(path, 1<<20)), &p); err != nil || p.Name == "" {
		return nil
	}
	m := &manifest{kind: "npm", name: p.Name, version: p.Version}
	var s string
	var o struct{ Type, URL string }
	if json.Unmarshal(p.License, &s) == nil {
		m.license = s
	} else if json.Unmarshal(p.License, &o) == nil {
		m.license = o.Type
	}
	if m.license == "" && len(p.Licenses) > 0 {
		var ids []string
		for _, l := range p.Licenses {
			ids = append(ids, l.Type)
		}
		m.license = strings.Join(ids, " OR ")
	}
	if strings.HasPrefix(strings.ToUpper(m.license), "SEE LICEN") {
		m.license = ""
	}
	if json.Unmarshal(p.Repository, &s) == nil {
		m.url = cleanRepoURL(s)
	} else if json.Unmarshal(p.Repository, &o) == nil {
		m.url = cleanRepoURL(o.URL)
	}
	return m
}

func readComposerJSON(path string) *manifest {
	var p struct {
		Name     string          `json:"name"`
		Version  string          `json:"version"`
		License  json.RawMessage `json:"license"`
		Homepage string          `json:"homepage"`
	}
	if err := json.Unmarshal([]byte(readSmall(path, 1<<20)), &p); err != nil || p.Name == "" {
		return nil
	}
	m := &manifest{kind: "composer", name: p.Name, version: p.Version, url: cleanRepoURL(p.Homepage)}
	var s string
	var list []string
	if json.Unmarshal(p.License, &s) == nil {
		m.license = s
	} else if json.Unmarshal(p.License, &list) == nil {
		m.license = strings.Join(list, " OR ")
	}
	return m
}

// tomlSection returns the simple key = "value" pairs of one TOML table.
// Remixware needs only a handful of string fields from Cargo.toml and
// pyproject.toml, so this is not a TOML parser.
func tomlSection(text, section string) map[string]string {
	out := map[string]string{}
	in := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			in = line == "["+section+"]"
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch {
		case strings.HasPrefix(val, "\""):
			if end := strings.Index(val[1:], "\""); end >= 0 {
				out[key] = val[1 : end+1]
			}
		case strings.HasPrefix(val, "'"):
			if end := strings.Index(val[1:], "'"); end >= 0 {
				out[key] = val[1 : end+1]
			}
		case strings.HasPrefix(val, "{"):
			// license = { text = "MIT" } or { file = "LICENSE" }, and Cargo's
			// license = { workspace = true }
			for _, sub := range []string{"text", "file"} {
				re := regexp.MustCompile(sub + `\s*=\s*["']([^"']+)["']`)
				if m := re.FindStringSubmatch(val); m != nil {
					out[key+"."+sub] = m[1]
				}
			}
			if workspaceTrue.MatchString(val) {
				out[key+".workspace"] = "true"
			}
		default:
			// license.workspace = true
			if i := strings.Index(val, "#"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
			out[key] = val
		}
	}
	return out
}

var workspaceTrue = regexp.MustCompile(`workspace\s*=\s*true`)

func readCargoToml(path string) *manifest {
	pkg := tomlSection(readSmall(path, 256<<10), "package")
	if pkg["name"] == "" {
		return nil
	}
	// A crate in a workspace can take these from the workspace's
	// Cargo.toml: license.workspace = true.
	var ws map[string]string
	inherit := func(key string) string {
		if pkg[key+".workspace"] != "true" {
			return pkg[key]
		}
		if ws == nil {
			ws = cargoWorkspace(filepath.Dir(path))
		}
		return ws[key]
	}
	return &manifest{kind: "cargo", name: pkg["name"], version: inherit("version"), license: inherit("license"),
		licFile: inherit("license-file"), url: cleanRepoURL(inherit("repository"))}
}

// cargoWorkspace returns the [workspace.package] table of the Cargo
// workspace a crate folder belongs to: the nearest Cargo.toml at or above it
// with a [workspace] table.
func cargoWorkspace(dir string) map[string]string {
	for i := 0; i < 8; i++ {
		text := readSmall(filepath.Join(dir, "Cargo.toml"), 256<<10)
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "[workspace]" {
				return tomlSection(text, "workspace.package")
			}
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	return map[string]string{}
}

func readPyproject(path string) *manifest {
	text := readSmall(path, 256<<10)
	p := tomlSection(text, "project")
	if p["name"] == "" {
		p = tomlSection(text, "tool.poetry")
		if p["name"] == "" {
			return nil
		}
	}
	m := &manifest{kind: "pypi", name: p["name"], version: p["version"], license: p["license"], licFile: p["license.file"]}
	if m.license == "" {
		m.license = p["license.text"]
	}
	return m
}

var pyClassifierLicense = map[string]string{
	"mit license": "MIT", "apache software license": "Apache-2.0", "bsd license": "BSD-3-Clause",
	"isc license (iscl)": "ISC", "the unlicense (unlicense)": "Unlicense", "public domain": "LicenseRef-Public-Domain",
	"mozilla public license 2.0 (mpl 2.0)": "MPL-2.0", "python software foundation license": "PSF-2.0",
	"gnu general public license v2 (gplv2)": "GPL-2.0-only", "gnu general public license v2 or later (gplv2+)": "GPL-2.0-or-later",
	"gnu general public license v3 (gplv3)": "GPL-3.0-only", "gnu general public license v3 or later (gplv3+)": "GPL-3.0-or-later",
	"gnu lesser general public license v2 (lgplv2)": "LGPL-2.0-only", "gnu lesser general public license v2 or later (lgplv2+)": "LGPL-2.1-or-later",
	"gnu lesser general public license v3 (lgplv3)": "LGPL-3.0-only", "gnu lesser general public license v3 or later (lgplv3+)": "LGPL-3.0-or-later",
	"gnu affero general public license v3": "AGPL-3.0-only", "gnu affero general public license v3 or later (agplv3+)": "AGPL-3.0-or-later",
	"eclipse public license 2.0 (epl-2.0)": "EPL-2.0", "zlib/libpng license": "Zlib", "boost software license 1.0 (bsl-1.0)": "BSL-1.0",
}

// readDistInfo reads a Python *.dist-info folder in site-packages.
func readDistInfo(dir string) (*manifest, []string) {
	text := readSmall(filepath.Join(dir, "METADATA"), 256<<10)
	if text == "" {
		text = readSmall(filepath.Join(dir, "PKG-INFO"), 256<<10)
	}
	m := &manifest{kind: "pypi"}
	var classifiers []string
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Name":
			m.name = v
		case "Version":
			m.version = v
		case "License-Expression":
			m.license = v
		case "License":
			if m.license == "" && len(v) < 80 {
				if id := canonicalID(v); lookupLicense(id).Cat != CatUnknown {
					m.license = id
				}
			}
		case "Home-page":
			m.url = cleanRepoURL(v)
		case "Classifier":
			if strings.HasPrefix(v, "License :: ") {
				parts := strings.Split(v, " :: ")
				if id, ok := pyClassifierLicense[strings.ToLower(parts[len(parts)-1])]; ok {
					classifiers = append(classifiers, id)
				}
			}
		}
	}
	if m.license == "" && len(classifiers) > 0 {
		m.license = strings.Join(dedupe(classifiers), " OR ")
	}
	var licFiles []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && isLicenseFileName(e.Name()) {
			licFiles = append(licFiles, filepath.Join(dir, e.Name()))
		}
		if e.IsDir() && (e.Name() == "licenses" || e.Name() == "LICENSES") {
			sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
			for _, s := range sub {
				if !s.IsDir() {
					licFiles = append(licFiles, filepath.Join(dir, e.Name(), s.Name()))
				}
			}
		}
	}
	for _, l := range strings.Split(readSmall(filepath.Join(dir, "top_level.txt"), 64<<10), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			m.topLevel = append(m.topLevel, l)
		}
	}
	if len(m.topLevel) == 0 && m.name != "" {
		m.topLevel = []string{strings.ReplaceAll(strings.ToLower(m.name), "-", "_")}
	}
	// RECORD lists every file the distribution installed, relative to
	// site-packages. It is what tells two distributions that share a
	// namespace package apart: google-auth and protobuf both install into
	// google/.
	if rec := readSmall(filepath.Join(dir, "RECORD"), 8<<20); rec != "" {
		r := csv.NewReader(strings.NewReader(rec))
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		for {
			row, err := r.Read()
			if err != nil {
				break
			}
			if len(row) > 0 && row[0] != "" {
				m.record = append(m.record, row[0])
			}
		}
	}
	return m, licFiles
}

// namedLicence maps what follows LICENSE- in a file name, as in LICENSE-MIT
// or LICENSE.APACHE, to the start of the licence id it stands for.
var namedLicence = map[string]string{"mit": "MIT", "apache": "Apache-", "apache2": "Apache-2.0", "apache-2.0": "Apache-2.0",
	"apache-2": "Apache-2.0", "apache_2_0": "Apache-2.0", "apache2.0": "Apache-2.0", "bsd": "BSD-", "isc": "ISC",
	"zlib": "Zlib", "unlicense": "Unlicense", "cc0": "CC0-", "boost": "BSL-", "bsl": "BSL-", "mpl": "MPL-",
	"mpl2": "MPL-2.0", "mpl-2.0": "MPL-2.0", "gpl": "GPL-", "gpl2": "GPL-2.0", "gpl3": "GPL-3.0", "lgpl": "LGPL-",
	"agpl": "AGPL-", "0bsd": "0BSD", "wtfpl": "WTFPL"}

// licenceNamedBy returns the start of the licence id a licence file's name
// names, as in LICENSE-MIT, LICENSE-APACHE or UNLICENSE, or "".
func licenceNamedBy(base string) string {
	low := strings.ToLower(base)
	for _, ext := range []string{".txt", ".md", ".markdown", ".rst"} {
		low = strings.TrimSuffix(low, ext)
	}
	if low == "unlicense" {
		return "Unlicense"
	}
	for _, pre := range []string{"license", "licence", "copying"} {
		if len(low) > len(pre)+1 && strings.HasPrefix(low, pre) && strings.ContainsRune("-_.", rune(low[len(pre)])) {
			return namedLicence[low[len(pre)+1:]]
		}
	}
	return ""
}

// licenseFromFiles reads licence files and names the licences in them.
// Licence files usually all apply, joined with AND. Files that each carry
// one licence in their name, such as LICENSE-MIT and LICENSE-APACHE, are a
// choice, joined with OR: that is how Rust crates and many others say
// "MIT or Apache-2.0". So is a short file that says the project is dual
// licensed.
func licenseFromFiles(paths []string) (expr string, from string, text string, copyrights []string) {
	var ids []string
	var froms []string
	var option []bool // the file's licence is one of a choice
	var choiceText string
	for _, p := range paths {
		t := readSmall(p, 256<<10)
		if strings.TrimSpace(t) == "" {
			continue
		}
		id := DetectLicense(t)
		base := filepath.Base(p)
		if id == "" && len(t) < 2000 && offersChoice(normText(t)) {
			// "This project is dual-licensed under the Unlicense and MIT."
			choiceText = normText(t)
			copyrights = append(copyrights, copyrightLines(t, 10)...)
			continue
		}
		if id == "" {
			// REUSE: LICENSES/MIT.txt names the licence in the file name.
			if dir := filepath.Base(filepath.Dir(p)); strings.EqualFold(dir, "LICENSES") {
				if l := lookupLicense(strings.TrimSuffix(base, filepath.Ext(base))); l.Cat != CatUnknown {
					id = l.ID
				}
			}
		}
		if short := strings.TrimSpace(t); id == "" && len(short) <= 60 {
			// A licence file holding only a name: "MIT", "Apache-2.0".
			if l := lookupLicense(short); l.Cat != CatUnknown && l.Cat != CatNone {
				id = l.ID
			}
		}
		if id == "" {
			switch {
			case licensePointer.MatchString(t):
				// docs/license.rst saying ".. include:: ../LICENSE.rst" is a
				// page about the licence, not a licence.
				continue
			case !licenseWording.MatchString(licensePointerSentence.ReplaceAllString(withoutCopyrightLines(t), "")):
				// A file that only lists copyright holders, perhaps saying
				// the licence is in the LICENSE file, isn't a licence.
				copyrights = append(copyrights, copyrightLines(t, 10)...)
				continue
			}
			if id = licenseInTitle(t); id == "" {
				// Licence wording we can't place: say so rather than guess.
				id = LicenseUnrecognised
			}
		}
		ids = append(ids, id)
		froms = append(froms, base)
		named := licenceNamedBy(base)
		option = append(option, named != "" && strings.HasPrefix(id, named))
		// Every licence file's text: a notice for an "Apache-2.0 AND MIT"
		// component needs both.
		if text != "" {
			text += "\n\n----- " + base + " -----\n\n"
		}
		text += strings.TrimSpace(t)
		copyrights = append(copyrights, copyrightLines(t, 10)...)
	}
	// An LGPL-3.0 project ships the GPL-3.0 text too, because the LGPL is
	// written as additions to it. That GPL text is not a second licence.
	hasLGPL3 := false
	for _, id := range ids {
		if strings.HasPrefix(id, "LGPL-3.0") {
			hasLGPL3 = true
		}
	}
	if hasLGPL3 {
		var keep []string
		var keepOpt []bool
		for i, id := range ids {
			if !strings.HasPrefix(id, "GPL-3.0") {
				keep = append(keep, id)
				keepOpt = append(keepOpt, option[i])
			}
		}
		ids, option = keep, keepOpt
	}
	if len(ids) == 0 {
		return "", "", "", dedupe(copyrights)
	}
	if len(text) > 64<<10 {
		text = text[:64<<10]
	}
	// A dual-licence statement makes every licence it names an option.
	if choiceText != "" {
		for i, id := range ids {
			if !option[i] && mentionsLicence(choiceText, id) {
				option[i] = true
			}
		}
	}
	var opts, rest []string
	for i, id := range ids {
		if option[i] {
			opts = append(opts, id)
		} else {
			rest = append(rest, id)
		}
	}
	if opts = dedupe(opts); len(opts) >= 2 {
		sort.Strings(opts)
		rest = append(rest, strings.Join(opts, " OR "))
	} else {
		rest = append(rest, opts...)
	}
	return joinLicenses(rest), strings.Join(froms, ", "), text, dedupe(copyrights)
}

// mentionsLicence reports whether a normalised text names a licence, by its
// id ("mit", "unlicense", "apache 2 0") or by its full name.
func mentionsLicence(t, id string) bool {
	l := lookupLicense(id)
	for _, name := range []string{l.ID, l.Name, strings.SplitN(l.ID, "-", 2)[0]} {
		if n := strings.TrimSpace(normText(name)); len(n) >= 3 && strings.Contains(t, " "+n+" ") {
			return true
		}
	}
	return false
}

var (
	licensePointer = regexp.MustCompile(`(?m)^\s*(\.\. (literal)?include::|\{%-? *include|\{\{<? *include)`)
	licenseWording = regexp.MustCompile(`(?i)permission|permitted|licen[cs]|redistribut|warrant|granted|free software|terms|conditions|copyleft|public domain`)
	// "... a BSD-style license that can be found in the LICENSE file."
	licensePointerSentence = regexp.MustCompile(`(?i)[^.]*\b(can|may) be found in the [^.]*\bfiles?\b\.?|\bsee (the )?\S*licen[cs]e\S*( files?)?\.?`)
)

// withoutCopyrightLines drops the copyright lines from a text.
func withoutCopyrightLines(t string) string {
	var keep []string
	for _, l := range strings.Split(t, "\n") {
		if len(copyrightLines(l, 1)) == 0 {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// licenseInTitle finds an SPDX identifier such as "BSD-3-Clause" in the
// first line of a short licence file that doesn't hold the full text.
func licenseInTitle(t string) string {
	if len(t) > 2000 {
		return ""
	}
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(t), "\n", 2)[0])
	for _, w := range strings.FieldsFunc(first, func(r rune) bool { return r == ' ' || r == ',' || r == ':' || r == '(' || r == ')' }) {
		if !strings.ContainsAny(w, "-.") || !strings.ContainsAny(w, "0123456789") {
			continue
		}
		if l := lookupLicense(w); l.Cat != CatUnknown && l.Cat != CatNone {
			return l.ID
		}
	}
	return ""
}

// dependencyBoundary reports whether a path between a component and the
// folder above it passes through a folder that holds other people's code,
// in which case the inner component must not inherit the outer licence.
func dependencyBoundary(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch strings.ToLower(part) {
		case "node_modules", "vendor", "third_party", "third-party", "thirdparty", "external", "extern",
			"deps", "bower_components", "site-packages", "dist-packages", "pods", "submodules":
			return true
		}
	}
	return false
}
