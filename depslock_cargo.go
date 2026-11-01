package remixware

import (
	"fmt"
	"regexp"
	"strings"
)

// EcoCargo is Rust's crates.io ecosystem, as package URLs name it.
const EcoCargo = "cargo"

// cratesIOSources are the source strings Cargo.lock writes for crates.io:
// the git index and the sparse one.
var cratesIOSources = map[string]bool{
	"registry+https://github.com/rust-lang/crates.io-index": true,
	"sparse+https://index.crates.io/":                       true,
}

var (
	crateName    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	crateVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
)

// parseCargoLock reads Cargo.lock: versions 3 and 4, and the older files
// with no version line. Packages without a source are the workspace's own
// crates (the root and path dependencies); the crates they list in
// dependencies are the direct ones. A crate from crates.io (its git or
// sparse index) is a registry package; git sources and other registries
// aren't. Cargo.lock doesn't say which crates have a build script or are
// procedural macros, which run code when they're compiled: for crates.io
// crates that's left to --online; crates from git or another registry
// count as running code, since they can't be looked up.
func parseCargoLock(data []byte) ([]lockPackage, error) {
	doc, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	if v, ok := doc["version"]; ok {
		raw, isRaw := v.(tomlRaw)
		if !isRaw || (raw != "3" && raw != "4") {
			return nil, fmt.Errorf("Cargo.lock version %v, which Remixware doesn't read (it reads 3 and 4)", v)
		}
	}
	for k := range doc {
		switch k {
		case "version", "package", "metadata", "patch":
		default:
			return nil, fmt.Errorf("a top-level %q, which Cargo.lock doesn't have", k)
		}
	}
	raw, ok := doc["package"]
	if !ok {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("package isn't an array of tables")
	}
	type entry struct {
		name, version, source string
		deps                  []string
	}
	var entries []entry
	for i, x := range list {
		t, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %d isn't a table", i+1)
		}
		e := entry{}
		e.name, _ = t["name"].(string)
		e.version, _ = t["version"].(string)
		if !crateName.MatchString(e.name) {
			return nil, fmt.Errorf("package %d has the name %q, which crates.io doesn't allow", i+1, e.name)
		}
		if !crateVersion.MatchString(e.version) {
			return nil, fmt.Errorf("package %s has the version %q, which isn't a semantic version", e.name, e.version)
		}
		if s, present := t["source"]; present {
			if e.source, ok = s.(string); !ok || e.source == "" {
				return nil, fmt.Errorf("package %s has a source that isn't a string", e.name)
			}
		}
		if d, present := t["dependencies"]; present {
			ds, ok := d.([]any)
			if !ok {
				return nil, fmt.Errorf("package %s: dependencies isn't an array", e.name)
			}
			for _, x := range ds {
				s, ok := x.(string)
				if !ok {
					return nil, fmt.Errorf("package %s: a dependency that isn't a string", e.name)
				}
				// "name", "name version" or "name version (source)".
				n := strings.Fields(s)
				if len(n) == 0 || !crateName.MatchString(n[0]) {
					return nil, fmt.Errorf("package %s: the dependency %q", e.name, short(s))
				}
				e.deps = append(e.deps, n[0])
			}
		}
		for k := range t {
			switch k {
			case "name", "version", "source", "checksum", "dependencies", "replace":
			default:
				return nil, fmt.Errorf("package %s has a key Remixware doesn't know: %s", e.name, k)
			}
		}
		if _, replaced := t["replace"]; replaced {
			// [replace] in Cargo.toml: this entry stands for another crate.
			// Reading it as the crate it names could hide the replacement.
			return nil, fmt.Errorf("package %s uses replace, which Remixware doesn't read", e.name)
		}
		entries = append(entries, e)
	}
	direct := map[string]bool{}
	for _, e := range entries {
		if e.source == "" {
			for _, d := range e.deps {
				direct[d] = true
			}
		}
	}
	var out []lockPackage
	for _, e := range entries {
		if e.source == "" {
			continue // the workspace's own crate
		}
		registry := cratesIOSources[e.source]
		if !registry && !strings.HasPrefix(e.source, "git+") && !strings.HasPrefix(e.source, "registry+") &&
			!strings.HasPrefix(e.source, "sparse+") {
			return nil, fmt.Errorf("package %s has a source Remixware doesn't know: %q", e.name, short(e.source))
		}
		// A crate from git or another registry can't be looked up, and
		// building it may run its build script: it counts as running code,
		// as pip's VCS requirements do. crates.io crates are looked up with
		// --online.
		scripts := scriptsUnknown
		if !registry {
			scripts = scriptsYes
		}
		out = append(out, lockPackage{eco: EcoCargo, name: e.name, version: e.version, direct: direct[e.name],
			registry: registry, pinned: true, scripts: scripts, source: e.source})
	}
	return out, nil
}

// crateKey is a crate name as crates.io compares names: case and - and _
// don't matter.
func crateKey(n string) string { return strings.ReplaceAll(strings.ToLower(n), "_", "-") }
