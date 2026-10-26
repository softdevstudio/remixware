package remixware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// scanossMatch is one match in the results file written by the SCANOSS
// scanner (scanoss-py scan -o results.json): an object that maps each
// scanned file to a list of matches.
type scanossMatch struct {
	ID        string   `json:"id"` // none, file, snippet, dependency
	Lines     string   `json:"lines"`
	OSSLines  string   `json:"oss_lines"`
	Matched   string   `json:"matched"`
	PURL      []string `json:"purl"`
	Vendor    string   `json:"vendor"`
	Component string   `json:"component"`
	Version   string   `json:"version"`
	URL       string   `json:"url"`
	File      string   `json:"file"`
	Licenses  []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"licenses"`
	Copyrights []struct {
		Name string `json:"name"`
	} `json:"copyrights"`
}

// Which licence source to trust for a snippet, most specific first.
var scanossSourceRank = map[string]int{"file_spdx_tag": 0, "file_header": 1, "license_file": 2,
	"component_declared": 3, "scancode": 4}

// ImportResults reads a results file: either one written by the SCANOSS
// scanner, or a findings file written by remixware scan --json. project is
// the folder the scanned paths are relative to.
func ImportResults(path, project string) ([]Finding, Input, map[string]*ComponentTexts, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Input{}, nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, Input{}, nil, fmt.Errorf("%s is not a SCANOSS or Remixware results file (expected a JSON object)", path)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, Input{}, nil, fmt.Errorf("%s: %v", path, err)
	}
	if tool, ok := probe["tool"]; ok {
		var t string
		if json.Unmarshal(tool, &t) == nil && strings.HasPrefix(t, "remixware") {
			var r Results
			if err := json.Unmarshal(data, &r); err != nil {
				return nil, Input{}, nil, fmt.Errorf("%s: %v", path, err)
			}
			// A findings file may come from another machine or another
			// person: its paths are never read, and its files must be
			// inside the project.
			var kept []Finding
			for _, f := range r.Findings {
				rel, ok := projectPath(f.File)
				if !ok {
					continue
				}
				f.File = rel
				f.Source.Abs = ""
				for i := range f.Also {
					f.Also[i].Abs = ""
				}
				kept = append(kept, f)
			}
			return kept, Input{Kind: "remixware", Path: path, Matches: len(kept)}, r.Texts, nil
		}
	}
	findings, err := scanossFindings(probe, path+" is not a SCANOSS results file", project)
	if err != nil {
		return nil, Input{}, nil, err
	}
	return findings, Input{Kind: "scanoss", Path: path, Matches: len(findings)}, nil, nil
}

// scanossFindings reads SCANOSS results, an object that maps each scanned
// file to its list of matches, into findings. Paths that would point outside
// the project are left out. what names the results in an error.
func scanossFindings(results map[string]json.RawMessage, what, project string) ([]Finding, error) {
	var findings []Finding
	files := make([]string, 0, len(results))
	for f := range results {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		var matches []scanossMatch
		if err := json.Unmarshal(results[file], &matches); err != nil {
			return nil, fmt.Errorf("%s: the entry for %q isn't a list of matches", what, file)
		}
		rel, ok := projectPath(file)
		if !ok {
			continue
		}
		for _, m := range matches {
			if m.ID != "file" && m.ID != "snippet" {
				continue
			}
			findings = append(findings, scanossFinding(rel, m, project))
		}
	}
	return findings, nil
}

// projectPath cleans a path from a results file. It reports false for
// paths that would point outside the project.
func projectPath(p string) (string, bool) {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.HasPrefix(p, "/") || filepath.VolumeName(p) != "" || (len(p) > 1 && p[1] == ':') {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

func scanossFinding(rel string, m scanossMatch, project string) Finding {
	lines := parseRanges(m.Lines)
	if len(lines) == 0 {
		// "all": the whole file.
		n := countLines([]byte(readSmall(filepath.Join(project, filepath.FromSlash(rel)), 16<<20)))
		lines = []Range{{1, max(n, 1)}}
	}
	oss := parseRanges(m.OSSLines)
	name := m.Component
	if m.Vendor != "" && !strings.Contains(name, "/") && !strings.EqualFold(m.Vendor, m.Component) {
		name = m.Vendor + "/" + m.Component
	}
	purl := ""
	if len(m.PURL) > 0 {
		purl = m.PURL[0]
	}
	key := name
	if purl != "" {
		key = purl
	}
	if m.Version != "" && !strings.Contains(strings.TrimPrefix(key, "pkg:"), "@") {
		key += "@" + m.Version
	}
	lic, from := scanossLicense(m)
	src := Source{Key: key, Component: name, Version: m.Version, File: m.File, URL: m.URL, PURL: purl,
		License: lic, LicenseFrom: from, Via: "scanoss"}
	if len(oss) > 0 {
		src.Lines = span(oss)
	}
	for _, c := range m.Copyrights {
		if c.Name != "" && !containsString(src.Copyrights, c.Name) {
			src.Copyrights = append(src.Copyrights, c.Name)
		}
	}
	cov := 0.0
	if v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(m.Matched), "%"), 64); err == nil {
		cov = v / 100
	}
	f := Finding{File: rel, Lines: span(lines), Source: src,
		Match: Match{Kind: m.ID, Coverage: round2(cov), WholeFile: m.ID == "file"}}
	f.score = cov * float64(f.Lines.len())
	return f
}

// scanossLicense picks the licence that governs a match: the licences
// found at the most specific source SCANOSS reports, all of which apply.
func scanossLicense(m scanossMatch) (string, string) {
	best := 100
	for _, l := range m.Licenses {
		r, ok := scanossSourceRank[l.Source]
		if !ok {
			r = 5
		}
		if r < best && l.Name != "" {
			best = r
		}
	}
	if best == 100 {
		return LicenseNoAssertion, "SCANOSS (no licence reported)"
	}
	var ids []string
	source := ""
	for _, l := range m.Licenses {
		r, ok := scanossSourceRank[l.Source]
		if !ok {
			r = 5
		}
		if r == best && l.Name != "" {
			ids = append(ids, l.Name)
			source = l.Source
		}
	}
	return joinLicenses(ids), "SCANOSS (" + strings.ReplaceAll(source, "_", " ") + ")"
}

// parseRanges reads SCANOSS line notation such as "12-40" or "1-10,25-30".
// "all" and empty strings give no ranges.
func parseRanges(s string) []Range {
	var out []Range
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == "all" {
			continue
		}
		a, b, found := strings.Cut(part, "-")
		x, err1 := strconv.Atoi(strings.TrimSpace(a))
		y := x
		var err2 error
		if found {
			y, err2 = strconv.Atoi(strings.TrimSpace(b))
		}
		if err1 != nil || err2 != nil || x <= 0 || y < x {
			continue
		}
		out = append(out, Range{x, y})
	}
	return out
}

func span(rs []Range) Range {
	r := rs[0]
	for _, x := range rs[1:] {
		r.Start, r.End = min(r.Start, x.Start), max(r.End, x.End)
	}
	return r
}
