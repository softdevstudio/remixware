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
// the folder the scanned paths are relative to. Entries for paths outside
// the project, or with characters a terminal acts on, are left out and
// counted in the Input's Dropped.
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
			// inside the project. Its text goes to terminals and notice
			// files, so it can't hold characters a terminal acts on.
			var kept []Finding
			dropped := 0
			perFile := map[string]int{}
			for _, f := range r.Findings {
				rel, ok := importPath(f.File)
				if !ok {
					dropped++
					continue
				}
				if perFile[rel]++; perFile[rel] > importMaxFindings {
					return nil, Input{}, nil, fmt.Errorf("%s: more than %d findings for %q, more than a scan gives one file", path, importMaxFindings, rel)
				}
				f.File = rel
				f.Source.Abs = ""
				cleanSource(&f.Source)
				for i := range f.Also {
					f.Also[i].Abs = ""
					cleanSource(&f.Also[i])
				}
				f.Match.Kind = oneLine(f.Match.Kind)
				kept = append(kept, f)
			}
			texts := map[string]*ComponentTexts{}
			for k, t := range r.Texts {
				if t != nil {
					texts[oneLine(k)] = &ComponentTexts{LicenseText: textBlock(t.LicenseText), NoticeText: textBlock(t.NoticeText)}
				}
			}
			return kept, Input{Kind: "remixware", Path: path, Matches: len(kept), Dropped: dropped}, texts, nil
		}
	}
	findings, dropped, err := scanossFindings(probe, path, project, false)
	if err != nil {
		return nil, Input{}, nil, err
	}
	return findings, Input{Kind: "scanoss", Path: path, Matches: len(findings), Dropped: dropped}, nil, nil
}

const (
	// scanossMaxMatches is the most matches one file may get. The engine
	// gives a file one match, or a few; more is a broken or made-up answer,
	// and merging them costs time that grows with their square.
	scanossMaxMatches = 100
	// scanossMaxListed is the most licences, copyright lines or package
	// URLs one match may list.
	scanossMaxListed = 100
	// importMaxFindings is the most findings a findings file may give one
	// file.
	importMaxFindings = 1000
)

// countFileLines counts a project file's lines, for a match of the whole
// file (a variable for the tests).
var countFileLines = func(path string) int {
	return countLines([]byte(readSmall(path, 16<<20)))
}

// scanossFindings reads SCANOSS results, an object that maps each scanned
// file to its list of matches, into findings. what names the results in an
// error. Paths from a results file are untrusted: entries for paths that
// would point outside the project, or that hold characters a terminal acts
// on, are left out, and those with matches are counted as dropped. trusted
// says the paths are the project's own, as when they come back from
// ScanSCANOSS.
func scanossFindings(results map[string]json.RawMessage, what, project string, trusted bool) ([]Finding, int, error) {
	var findings []Finding
	dropped := 0
	files := make([]string, 0, len(results))
	for f := range results {
		files = append(files, f)
	}
	sort.Strings(files)
	// Counted by project file, so that other spellings of a path ("./a.go",
	// "x/../a.go") can't get around the limit or the single read.
	perFile := map[string]int{}
	fileLines := map[string]int{}
	for _, file := range files {
		var matches []scanossMatch
		if err := json.Unmarshal(results[file], &matches); err != nil {
			return nil, 0, fmt.Errorf("%s: the entry for %q isn't a list of matches", what, file)
		}
		has := false
		for _, m := range matches {
			has = has || m.ID == "file" || m.ID == "snippet"
		}
		if !has {
			continue
		}
		rel, ok := file, true
		if !trusted {
			rel, ok = importPath(file)
		}
		if !ok {
			dropped++
			continue
		}
		for _, m := range matches {
			if m.ID != "file" && m.ID != "snippet" {
				continue
			}
			if perFile[rel]++; perFile[rel] > scanossMaxMatches {
				return nil, 0, fmt.Errorf("%s: %q gets more than %d matches, more than SCANOSS gives for one file", what, file, scanossMaxMatches)
			}
			n, counted := fileLines[rel]
			if !counted && len(parseRanges(m.Lines)) == 0 {
				// "all": the whole file, whose lines are counted once.
				n = max(countFileLines(filepath.Join(project, filepath.FromSlash(rel))), 1)
				fileLines[rel] = n
			}
			f, err := scanossFinding(rel, m, n)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: a match for %q %v", what, file, err)
			}
			findings = append(findings, f)
		}
	}
	return findings, dropped, nil
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

// importPath is projectPath for a path that will be shown: it also reports
// false for paths with characters a terminal acts on.
func importPath(p string) (string, bool) {
	if unsafeText(p) {
		return "", false
	}
	return projectPath(p)
}

// scanossFinding turns one match into a finding. fileLines is the length
// of the project file, for a match of the whole file. The text a match
// carries came from elsewhere and goes to terminals, reports and notices,
// so characters a terminal acts on are taken out of it (see oneLine).
func scanossFinding(rel string, m scanossMatch, fileLines int) (Finding, error) {
	if len(m.Licenses) > scanossMaxListed || len(m.Copyrights) > scanossMaxListed || len(m.PURL) > scanossMaxListed {
		return Finding{}, fmt.Errorf("lists more than %d licences, copyright lines or package URLs", scanossMaxListed)
	}
	lines := parseRanges(m.Lines)
	if len(lines) == 0 {
		lines = []Range{{1, max(fileLines, 1)}}
	}
	oss := parseRanges(m.OSSLines)
	m.Component, m.Vendor, m.Version = oneLine(m.Component), oneLine(m.Vendor), oneLine(m.Version)
	m.File, m.URL = oneLine(m.File), oneLine(m.URL)
	for i := range m.PURL {
		m.PURL[i] = oneLine(m.PURL[i])
	}
	for i := range m.Licenses {
		m.Licenses[i].Name, m.Licenses[i].Source = oneLine(m.Licenses[i].Name), oneLine(m.Licenses[i].Source)
	}
	name := m.Component
	vendor := m.Vendor
	if strings.EqualFold(vendor, "none") {
		vendor = "" // how the service writes a missing vendor
	}
	if vendor != "" && !strings.Contains(name, "/") && !strings.EqualFold(vendor, m.Component) {
		name = vendor + "/" + m.Component
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
	seen := map[string]bool{}
	for _, c := range m.Copyrights {
		if c.Name = oneLine(c.Name); c.Name != "" && !seen[c.Name] {
			seen[c.Name] = true
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
	return f, nil
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

// cleanSource takes the characters a terminal acts on out of a source's
// text from elsewhere (see oneLine).
func cleanSource(s *Source) {
	s.Key, s.Component, s.Version = oneLine(s.Key), oneLine(s.Component), oneLine(s.Version)
	for i := range s.OtherVersions {
		s.OtherVersions[i] = oneLine(s.OtherVersions[i])
	}
	s.File, s.URL, s.PURL = oneLine(s.File), oneLine(s.URL), oneLine(s.PURL)
	s.License, s.LicenseFrom, s.Via = oneLine(s.License), oneLine(s.LicenseFrom), oneLine(s.Via)
	for i := range s.Copyrights {
		s.Copyrights[i] = oneLine(s.Copyrights[i])
	}
}
