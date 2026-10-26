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
	"unicode"
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
			// Its text goes to terminals and notice files: only printable
			// characters on one line, and line breaks in licence texts.
			var kept []Finding
			for _, f := range r.Findings {
				rel, ok := projectPath(f.File)
				if !ok {
					continue
				}
				f.File = rel
				f.Source.Abs = ""
				cleanSource(&f.Source)
				for i := range f.Also {
					f.Also[i].Abs = ""
					cleanSource(&f.Also[i])
				}
				f.Match.Kind = printable(f.Match.Kind, 20)
				kept = append(kept, f)
			}
			texts := map[string]*ComponentTexts{}
			for k, t := range r.Texts {
				if t != nil {
					texts[printable(k, 300)] = &ComponentTexts{LicenseText: printableText(t.LicenseText), NoticeText: printableText(t.NoticeText)}
				}
			}
			return kept, Input{Kind: "remixware", Path: path, Matches: len(kept)}, texts, nil
		}
	}
	findings, err := scanossFindings(probe, path+" is not a SCANOSS results file", project, false)
	if err != nil {
		return nil, Input{}, nil, err
	}
	return findings, Input{Kind: "scanoss", Path: path, Matches: len(findings)}, nil, nil
}

// scanossMaxMatches is the most matches one file's entry may hold. The
// engine gives a file one match, or a few; more is a broken or made-up
// answer, and merging them costs time that grows with their square.
const scanossMaxMatches = 100

// scanossFindings reads SCANOSS results, an object that maps each scanned
// file to its list of matches, into findings. what names the results in an
// error. Paths from a results file are untrusted: those that would point
// outside the project are left out. trusted says the paths are the
// project's own, as when they come back from ScanSCANOSS.
func scanossFindings(results map[string]json.RawMessage, what, project string, trusted bool) ([]Finding, error) {
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
		rel, ok := file, true
		if !trusted {
			rel, ok = projectPath(file)
		}
		if !ok {
			continue
		}
		n, lines := 0, -1
		for _, m := range matches {
			if m.ID != "file" && m.ID != "snippet" {
				continue
			}
			if n++; n > scanossMaxMatches {
				return nil, fmt.Errorf("%s: the entry for %q holds more than %d matches, more than SCANOSS gives for one file", what, file, scanossMaxMatches)
			}
			if lines < 0 && len(parseRanges(m.Lines)) == 0 {
				// "all": the whole file, whose lines are counted once.
				lines = max(countLines([]byte(readSmall(filepath.Join(project, filepath.FromSlash(rel)), 16<<20))), 1)
			}
			findings = append(findings, scanossFinding(rel, m, lines))
		}
	}
	return findings, nil
}

// projectPath cleans a path from a results file. It reports false for
// paths that would point outside the project, and for paths with
// characters that aren't printable, which a terminal could take for
// commands.
func projectPath(p string) (string, bool) {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.HasPrefix(p, "/") || filepath.VolumeName(p) != "" || (len(p) > 1 && p[1] == ':') {
		return "", false
	}
	if strings.IndexFunc(p, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// scanossFinding turns one match into a finding. fileLines is the length
// of the project file, for a match of the whole file. Every text the match
// carries came from elsewhere and goes to terminals, reports and notices,
// so it's kept to printable characters on one line.
func scanossFinding(rel string, m scanossMatch, fileLines int) Finding {
	lines := parseRanges(m.Lines)
	if len(lines) == 0 {
		lines = []Range{{1, max(fileLines, 1)}}
	}
	oss := parseRanges(m.OSSLines)
	m.Component, m.Vendor, m.Version = printable(m.Component, 200), printable(m.Vendor, 200), printable(m.Version, 100)
	m.File, m.URL = printable(m.File, 300), printable(m.URL, 300)
	for i := range m.PURL {
		m.PURL[i] = printable(m.PURL[i], 300)
	}
	for i := range m.Licenses {
		m.Licenses[i].Name, m.Licenses[i].Source = printable(m.Licenses[i].Name, 100), printable(m.Licenses[i].Source, 100)
	}
	for i := range m.Copyrights {
		m.Copyrights[i].Name = printable(m.Copyrights[i].Name, 300)
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

// cleanSource keeps a source's text from elsewhere to printable characters
// on one line (see printable).
func cleanSource(s *Source) {
	s.Key, s.Component, s.Version = printable(s.Key, 300), printable(s.Component, 200), printable(s.Version, 100)
	for i := range s.OtherVersions {
		s.OtherVersions[i] = printable(s.OtherVersions[i], 100)
	}
	s.File, s.URL, s.PURL = printable(s.File, 300), printable(s.URL, 300), printable(s.PURL, 300)
	s.License, s.LicenseFrom, s.Via = printable(s.License, 200), printable(s.LicenseFrom, 100), printable(s.Via, 20)
	for i := range s.Copyrights {
		s.Copyrights[i] = printable(s.Copyrights[i], 300)
	}
}

// printableText keeps the line breaks and tabs of a text from elsewhere,
// such as a licence text, and drops every other character that isn't
// printable.
func printableText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
}
