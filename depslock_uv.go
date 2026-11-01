package remixware

import (
	"fmt"
	"sort"
	"strings"
)

// PyPISimple is the public index's address as uv.lock writes it.
const PyPISimple = "https://pypi.org/simple"

// parseUVLock reads uv.lock. The project's own packages (workspace members,
// with an editable or virtual source, or a directory source inside the
// project) are left out; their dependencies, optional dependencies and dev
// dependencies are the direct ones. A package with a wheel installs
// without running code; one with only a source archive, from git or from a
// URL to an archive builds from source, which runs its build code.
func parseUVLock(data []byte) ([]lockPackage, error) {
	doc, err := parseTOML(data)
	if err != nil {
		return nil, err
	}
	if _, ok := doc["version"].(tomlRaw); !ok {
		return nil, fmt.Errorf("no version: not a uv.lock")
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
		name, version string
		source        map[string]any
		t             map[string]any
	}
	var entries []entry
	own := map[string]bool{}
	direct := map[string]bool{}
	for i, x := range list {
		t, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %d isn't a table", i+1)
		}
		name, _ := t["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("package %d has no name", i+1)
		}
		src, ok := t["source"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("package %s has no source table", name)
		}
		version, _ := t["version"].(string)
		e := entry{name: pyName(name), version: version, source: src, t: t}
		if uvOwn(src) {
			own[e.name] = true
			for _, n := range uvDependencyNames(t) {
				direct[n] = true
			}
			continue
		}
		if version == "" {
			return nil, fmt.Errorf("package %s has no version", name)
		}
		entries = append(entries, e)
	}
	var out []lockPackage
	for _, e := range entries {
		lp := lockPackage{eco: EcoPyPI, name: e.name, version: e.version, direct: direct[e.name] && !own[e.name], pinned: true}
		reg, isReg := e.source["registry"].(string)
		switch {
		case isReg:
			// uv downloads the files the lockfile names, so they must be
			// PyPI's too.
			lp.registry = strings.TrimSuffix(reg, "/") == PyPISimple && uvFilesOnPyPI(e.t)
			lp.scripts = scriptsYes
			if wheels, ok := e.t["wheels"].([]any); ok && len(wheels) > 0 {
				lp.scripts = scriptsNo
			}
			if _, ok := e.t["sdist"]; !ok && lp.scripts == scriptsYes {
				// Neither wheel nor sdist recorded: nothing says what runs.
				lp.scripts = scriptsUnknown
			}
		case e.source["url"] != nil:
			u, _ := e.source["url"].(string)
			lp.scripts = scriptsYes
			if strings.HasSuffix(strings.ToLower(strings.SplitN(u, "#", 2)[0]), ".whl") {
				lp.scripts = scriptsNo
			}
		case e.source["path"] != nil:
			p, _ := e.source["path"].(string)
			lp.scripts = scriptsYes
			if strings.HasSuffix(strings.ToLower(p), ".whl") {
				lp.scripts = scriptsNo
			}
		case e.source["git"] != nil:
			lp.scripts = scriptsYes
		case e.source["editable"] != nil || e.source["virtual"] != nil || e.source["directory"] != nil:
			// A folder outside the project: built from source, not PyPI.
			lp.scripts = scriptsYes
		default:
			keys := make([]string, 0, len(e.source))
			for k := range e.source {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("package %s has a source Remixware doesn't know: %s", e.name, strings.Join(keys, ", "))
		}
		out = append(out, lp)
	}
	return out, nil
}

// uvOwn says whether a source is the project's own code: an editable,
// virtual or directory source whose path stays inside the project.
func uvOwn(src map[string]any) bool {
	for _, k := range []string{"editable", "virtual", "directory"} {
		if d, ok := src[k].(string); ok {
			return inProject(d)
		}
	}
	return false
}

// inProject says whether a path from a lockfile stays inside the project.
func inProject(d string) bool {
	d = strings.ReplaceAll(d, "\\", "/")
	if strings.HasPrefix(d, "/") || strings.Contains(d, ":") {
		return false
	}
	for _, part := range strings.Split(d, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// uvDependencyNames lists the packages a root package asks for: its
// dependencies, optional-dependencies and dev-dependencies.
func uvDependencyNames(t map[string]any) []string {
	var out []string
	add := func(v any) {
		list, _ := v.([]any)
		for _, x := range list {
			if d, ok := x.(map[string]any); ok {
				if n, ok := d["name"].(string); ok {
					out = append(out, pyName(n))
				}
			}
		}
	}
	add(t["dependencies"])
	for _, key := range []string{"optional-dependencies", "dev-dependencies"} {
		if groups, ok := t[key].(map[string]any); ok {
			for _, v := range groups {
				add(v)
			}
		}
	}
	return out
}

// PyPIFiles is where PyPI serves the files its simple index lists.
const PyPIFiles = "https://files.pythonhosted.org/"

// uvFilesOnPyPI says whether every wheel and source archive a package
// entry names is on PyPI's file host.
func uvFilesOnPyPI(t map[string]any) bool {
	var files []any
	if w, ok := t["wheels"].([]any); ok {
		files = append(files, w...)
	}
	if sd, ok := t["sdist"]; ok {
		files = append(files, sd)
	}
	for _, f := range files {
		m, ok := f.(map[string]any)
		if !ok {
			return false
		}
		u, ok := m["url"].(string)
		if !ok || !strings.HasPrefix(u, PyPIFiles) {
			return false
		}
	}
	return true
}
