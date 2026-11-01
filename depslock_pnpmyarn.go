package remixware

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// npmRegistries are the hosts that serve the public npm registry: npm's
// own and the address yarn v1 writes for it.
var npmRegistries = []string{NPMRegistry + "/", "https://registry.yarnpkg.com/"}

func onNPMRegistry(url string) bool {
	for _, r := range npmRegistries {
		if strings.HasPrefix(url, r) {
			return true
		}
	}
	return false
}

// npmNameAt splits "name@rest" at the '@' after the name (scoped names
// start with '@').
func npmNameAt(s string) (name, rest string, ok bool) {
	i := strings.Index(s[min(1, len(s)):], "@")
	if i < 0 {
		return "", "", false
	}
	i++
	return s[:i], s[i+1:], true
}

func jsPackage(name, version string, registry bool, scripts int8) (lockPackage, error) {
	if !npmName.MatchString(name) {
		return lockPackage{}, fmt.Errorf("package %q has a name npm doesn't allow", name)
	}
	if version == "" {
		return lockPackage{}, fmt.Errorf("package %q has no version", name)
	}
	p := lockPackage{eco: EcoNPM, name: name, version: version, scripts: scripts}
	p.pinned = npmVersion.MatchString(version)
	p.registry = registry && p.pinned
	return p, nil
}

// --- pnpm ---------------------------------------------------------------

// parsePnpmLock reads pnpm-lock.yaml, lockfile versions 5, 6 and 9, and
// every document in it (pnpm 9 and later can put the package manager's own
// lockfile first). The importers name the direct dependencies. Install
// scripts come from requiresBuild where the lockfile version records it
// (5 and 6); lockfile 9 doesn't, so they're unknown there and left to
// --online. Packages from a folder (directory or link) are the project's
// own code.
func parsePnpmLock(data []byte) ([]lockPackage, error) {
	docs, err := parseYAMLDocs(data)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("empty: not a pnpm-lock.yaml")
	}
	var out []lockPackage
	for _, doc := range docs {
		pkgs, err := pnpmDoc(doc)
		if err != nil {
			return nil, err
		}
		out = append(out, pkgs...)
	}
	return out, nil
}

func pnpmDoc(doc map[string]any) ([]lockPackage, error) {
	lv, ok := doc["lockfileVersion"].(string)
	if !ok || lv == "" {
		return nil, fmt.Errorf("no lockfileVersion: not a pnpm-lock.yaml")
	}
	major, err := strconv.Atoi(strings.SplitN(lv, ".", 2)[0])
	if err != nil || (major != 5 && major != 6 && major != 9) {
		return nil, fmt.Errorf("lockfileVersion %s, which Remixware doesn't read (it reads 5, 6 and 9)", lv)
	}
	direct := map[string]bool{}
	addDirect := func(deps any, where string) error {
		if deps == nil || deps == "" {
			return nil
		}
		m, ok := deps.(map[string]any)
		if !ok {
			return fmt.Errorf("%s isn't a mapping", where)
		}
		for name, v := range m {
			version := ""
			switch x := v.(type) {
			case string:
				version = x
			case map[string]any:
				version, _ = x["version"].(string)
			default:
				return fmt.Errorf("%s: %s has no version", where, name)
			}
			if strings.HasPrefix(version, "link:") || strings.HasPrefix(version, "file:") || strings.HasPrefix(version, "workspace:") {
				continue // the project's own folders
			}
			direct[name] = true
			// An alias (npm:real@1.0.0, /real@1.0.0, real@1.0.0) also makes
			// the real package direct.
			v := strings.TrimPrefix(strings.TrimPrefix(version, "npm:"), "/")
			if real, _, ok := npmNameAt(v); ok && npmName.MatchString(real) {
				direct[real] = true
			}
		}
		return nil
	}
	sections := []string{"dependencies", "devDependencies", "optionalDependencies", "packageManagerDependencies"}
	if imp, ok := doc["importers"]; ok {
		importers, ok := imp.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("importers isn't a mapping")
		}
		for path, x := range importers {
			if x == "" {
				continue
			}
			im, ok := x.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("importer %q isn't a mapping", path)
			}
			for _, s := range sections {
				if err := addDirect(im[s], "importer "+path+" "+s); err != nil {
					return nil, err
				}
			}
		}
	} else {
		for _, s := range sections {
			if err := addDirect(doc[s], s); err != nil {
				return nil, err
			}
		}
	}
	pk, ok := doc["packages"]
	if !ok || pk == "" {
		return nil, nil
	}
	packages, ok := pk.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("packages isn't a mapping")
	}
	keys := make([]string, 0, len(packages))
	for k := range packages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []lockPackage
	for _, key := range keys {
		e, ok := packages[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %q isn't a mapping", key)
		}
		res, ok := e["resolution"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %q has no resolution", key)
		}
		if _, isDir := res["directory"]; isDir || res["type"] == "directory" {
			continue // a folder in the project
		}
		name, version, err := pnpmKey(key, major)
		if err != nil {
			return nil, err
		}
		if n, ok := e["name"].(string); ok && n != "" {
			name = n
		}
		if v, ok := e["version"].(string); ok && v != "" {
			version = v
		}
		registry := false
		switch {
		case res["type"] == "git" || res["repo"] != nil || res["commit"] != nil:
		case res["tarball"] != nil:
			tb, _ := res["tarball"].(string)
			registry = onNPMRegistry(tb)
		case res["integrity"] != nil:
			registry = true
		default:
			return nil, fmt.Errorf("package %q has a resolution Remixware doesn't know", key)
		}
		scripts := scriptsUnknown
		if rb, ok := e["requiresBuild"]; ok {
			scripts = scriptsNo
			if rb == "true" {
				scripts = scriptsYes
			}
		} else if major < 9 {
			scripts = scriptsNo
		}
		p, err := jsPackage(name, version, registry, scripts)
		if err != nil {
			return nil, err
		}
		p.direct = direct[name]
		out = append(out, p)
	}
	return out, nil
}

// pnpmKey reads a packages key: "/name/1.0.0_peer" (lockfile 5),
// "/name@1.0.0(peer@2)" (6) or "name@1.0.0" (9).
func pnpmKey(key string, major int) (string, string, error) {
	k := strings.TrimPrefix(key, "/")
	if i := strings.Index(k, "("); i > 0 {
		k = k[:i]
	}
	if major == 5 {
		i := strings.LastIndex(k, "/")
		if i <= 0 {
			return "", "", fmt.Errorf("package key %q isn't name/version", key)
		}
		name, version := k[:i], k[i+1:]
		if j := strings.Index(version, "_"); j > 0 {
			version = version[:j]
		}
		return name, version, nil
	}
	name, version, ok := npmNameAt(k)
	if !ok {
		return "", "", fmt.Errorf("package key %q isn't name@version", key)
	}
	return name, version, nil
}

// --- yarn berry -----------------------------------------------------------

// parseYarnBerry reads the yarn.lock of yarn 2 and later (YAML, with
// __metadata). Workspace, link and portal entries are the project's own
// code; the workspaces' dependencies are the direct ones. npm: resolutions
// come from the registry; patched, git, file, http and exec ones don't.
// Yarn records no install scripts, so those are left to --online.
func parseYarnBerry(data []byte) ([]lockPackage, error) {
	docs, err := parseYAMLDocs(data)
	if err != nil {
		return nil, err
	}
	if len(docs) != 1 {
		return nil, fmt.Errorf("%d YAML documents; a yarn.lock has one", len(docs))
	}
	doc := docs[0]
	meta, ok := doc["__metadata"].(map[string]any)
	if !ok || meta["version"] == nil {
		return nil, fmt.Errorf("no __metadata version: not a yarn 2+ yarn.lock")
	}
	type entry struct {
		key, name, version, protocol string
		e                            map[string]any
	}
	var entries []entry
	direct := map[string]bool{}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "__metadata" {
			continue
		}
		e, ok := doc[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entry %q isn't a mapping", key)
		}
		res, ok := e["resolution"].(string)
		if !ok || res == "" {
			return nil, fmt.Errorf("entry %q has no resolution", key)
		}
		name, ref, ok := npmNameAt(res)
		if !ok {
			return nil, fmt.Errorf("entry %q: resolution %q isn't name@reference", key, res)
		}
		protocol, _, hasProto := strings.Cut(ref, ":")
		if !hasProto {
			return nil, fmt.Errorf("entry %q: resolution %q has no protocol", key, res)
		}
		version, _ := e["version"].(string)
		switch protocol {
		case "workspace", "link", "portal":
			if protocol == "workspace" {
				deps, _ := e["dependencies"].(map[string]any)
				for dn, dv := range deps {
					spec, _ := dv.(string)
					if strings.HasPrefix(spec, "workspace:") || strings.HasPrefix(spec, "link:") || strings.HasPrefix(spec, "portal:") {
						continue
					}
					direct[dn] = true
					if real, _, ok := npmNameAt(strings.TrimPrefix(spec, "npm:")); ok && strings.HasPrefix(spec, "npm:") && npmName.MatchString(real) {
						direct[real] = true
					}
				}
			}
			continue
		}
		entries = append(entries, entry{key: key, name: name, version: version, protocol: protocol, e: e})
	}
	var out []lockPackage
	for _, en := range entries {
		registry := false
		scripts := scriptsUnknown
		switch en.protocol {
		case "npm":
			registry = true
		case "patch":
			// patch:name@npm%3A1.2.3#<patch>: yarn's own compat patch or a
			// patch file in the project, applied to a registry package.
			registry = berryPatchOnNPM(en.e["resolution"].(string))
		case "file", "http", "https", "git", "git+ssh", "git+https", "git+http", "github", "ssh":
		case "exec":
			scripts = scriptsYes // runs a script to make the package
		default:
			return nil, fmt.Errorf("entry %q uses the %s: protocol, which Remixware doesn't know", en.key, en.protocol)
		}
		p, err := jsPackage(en.name, en.version, registry, scripts)
		if err != nil {
			return nil, err
		}
		p.direct = direct[en.name]
		out = append(out, p)
	}
	return out, nil
}

// berryPatchOnNPM says whether a patch: resolution patches a package from
// the npm registry ("name@patch:name@npm%3A1.2.3#...").
func berryPatchOnNPM(res string) bool {
	_, ref, ok := npmNameAt(res)
	if !ok || !strings.HasPrefix(ref, "patch:") {
		return false
	}
	inner := strings.TrimPrefix(ref, "patch:")
	if i := strings.Index(inner, "#"); i >= 0 {
		inner = inner[:i]
	}
	inner, err := url.PathUnescape(inner)
	if err != nil {
		return false
	}
	_, base, ok := npmNameAt(inner)
	return ok && strings.HasPrefix(base, "npm:") && npmVersion.MatchString(strings.TrimPrefix(base, "npm:"))
}

// --- yarn v1 ----------------------------------------------------------------

// parseYarnV1 reads the yarn.lock of yarn 1 (its own format: unindented
// descriptor lines, "key value" fields, indented dependency blocks).
// package.json names the direct dependencies; packages resolved from the
// npm registry (registry.yarnpkg.com or registry.npmjs.org) are registry
// packages; file: and link: entries are the project's own code. Yarn
// records no install scripts, so those are left to --online.
func parseYarnV1(data, packageJSON []byte) ([]lockPackage, error) {
	direct := map[string]bool{}
	if len(packageJSON) > 0 {
		root, err := decodeNPMEntry(packageJSON, "package.json")
		if err != nil {
			return nil, err
		}
		for _, m := range root.allDeps() {
			for name := range m {
				direct[name] = true
			}
		}
	}
	type entry struct {
		line                         int
		descriptors                  []string
		version, resolved, integrity string
		fields                       map[string]bool
	}
	var entries []*entry
	var cur *entry
	block := "" // the nested block a 4-space line belongs to
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " ")
		if line == "" || strings.HasPrefix(strings.TrimLeft(line, " "), "#") {
			continue
		}
		if strings.ContainsRune(line, '\t') {
			return nil, fmt.Errorf("line %d: a tab", n+1)
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := line[indent:]
		switch indent {
		case 0:
			if !strings.HasSuffix(body, ":") {
				return nil, fmt.Errorf("line %d: %q isn't an entry header", n+1, short(body))
			}
			descs, err := yarnDescriptors(strings.TrimSuffix(body, ":"))
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", n+1, err)
			}
			cur = &entry{line: n + 1, descriptors: descs, fields: map[string]bool{}}
			entries = append(entries, cur)
			block = ""
		case 2:
			if cur == nil {
				return nil, fmt.Errorf("line %d: a field outside an entry", n+1)
			}
			key, val, err := yarnField(body)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", n+1, err)
			}
			if cur.fields[key] {
				return nil, fmt.Errorf("line %d: %s given twice", n+1, key)
			}
			cur.fields[key] = true
			block = ""
			switch key {
			case "version":
				cur.version = val
			case "resolved":
				cur.resolved = val
			case "integrity":
				cur.integrity = val
			case "dependencies", "optionalDependencies":
				if val != "" {
					return nil, fmt.Errorf("line %d: %s with a value", n+1, key)
				}
				block = key
			case "uid", "bin", "os", "cpu", "engines", "peerDependencies", "peerDependenciesMeta":
				if val == "" {
					block = key
				}
			default:
				return nil, fmt.Errorf("line %d: a field Remixware doesn't know: %s", n+1, key)
			}
		case 4:
			if block == "" {
				return nil, fmt.Errorf("line %d: indented text outside a block", n+1)
			}
			if _, _, err := yarnField(body); err != nil {
				return nil, fmt.Errorf("line %d: %v", n+1, err)
			}
		default:
			return nil, fmt.Errorf("line %d: unexpected indentation", n+1)
		}
	}
	if len(entries) == 0 && !strings.Contains(text, "yarn lockfile v1") {
		return nil, fmt.Errorf("no entries and no \"yarn lockfile v1\" line: not a yarn.lock")
	}
	var out []lockPackage
	seen := map[string]bool{}
	for _, e := range entries {
		var name, real string
		local := false
		isDirect := false
		for _, d := range e.descriptors {
			dn, rng, ok := npmNameAt(d)
			if !ok {
				return nil, fmt.Errorf("line %d: %q isn't name@range", e.line, d)
			}
			if direct[dn] {
				isDirect = true
			}
			r := dn
			if strings.HasPrefix(rng, "npm:") {
				if rn, _, ok := npmNameAt(strings.TrimPrefix(rng, "npm:")); ok {
					r = rn
				} else {
					r = strings.TrimPrefix(rng, "npm:") // npm:name with no range
				}
			}
			if strings.HasPrefix(rng, "file:") || strings.HasPrefix(rng, "link:") {
				local = true
			}
			if real == "" {
				name, real = dn, r
			} else if r != real {
				return nil, fmt.Errorf("line %d: one entry names two packages, %s and %s", e.line, real, r)
			}
		}
		_ = name
		if local {
			continue
		}
		if e.version == "" {
			return nil, fmt.Errorf("line %d: %s has no version", e.line, real)
		}
		key := real + "@" + e.version + "@" + e.resolved
		if seen[key] {
			continue
		}
		seen[key] = true
		p, err := jsPackage(real, e.version, onNPMRegistry(e.resolved), scriptsUnknown)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", e.line, err)
		}
		p.direct = isDirect || direct[real]
		out = append(out, p)
	}
	return out, nil
}

// yarnDescriptors splits an entry header: descriptors separated by ", ",
// each plain or double-quoted.
func yarnDescriptors(s string) ([]string, error) {
	var out []string
	for s != "" {
		var d string
		if s[0] == '"' {
			end := strings.Index(s[1:], `"`)
			if end < 0 {
				return nil, fmt.Errorf("a quote that isn't closed")
			}
			d = s[1 : end+1]
			s = s[end+2:]
		} else {
			i := strings.Index(s, ", ")
			if i < 0 {
				i = len(s)
			}
			d = s[:i]
			s = s[i:]
			if strings.ContainsAny(d, `"`) {
				return nil, fmt.Errorf("a quote inside a descriptor")
			}
		}
		if d == "" {
			return nil, fmt.Errorf("an empty descriptor")
		}
		out = append(out, d)
		if s == "" {
			break
		}
		if !strings.HasPrefix(s, ", ") {
			return nil, fmt.Errorf("expected \", \" between descriptors")
		}
		s = s[2:]
	}
	return out, nil
}

// yarnField reads `key value`, `key "value"` or `key:` (a block).
func yarnField(s string) (string, string, error) {
	var key, rest string
	if s[0] == '"' {
		end := strings.Index(s[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("a quote that isn't closed")
		}
		key, rest = s[1:end+1], s[end+2:]
	} else {
		i := strings.IndexAny(s, " :")
		if i < 0 {
			return "", "", fmt.Errorf("%q has no value", short(s))
		}
		key, rest = s[:i], s[i:]
	}
	if rest == ":" {
		return key, "", nil
	}
	if !strings.HasPrefix(rest, " ") {
		return "", "", fmt.Errorf("%q isn't key value", short(s))
	}
	val := rest[1:]
	if strings.HasPrefix(val, `"`) {
		if len(val) < 2 || !strings.HasSuffix(val, `"`) {
			return "", "", fmt.Errorf("a quote that isn't closed")
		}
		u, err := strconv.Unquote(val)
		if err != nil {
			return "", "", fmt.Errorf("a quoted value Remixware can't read: %q", short(val))
		}
		val = u
	}
	return key, val, nil
}

// yarnBerry says whether a yarn.lock is yarn 2+'s YAML (it has a
// __metadata entry at the top level) rather than yarn 1's format.
func yarnBerry(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "__metadata:") {
			return true
		}
	}
	return false
}
