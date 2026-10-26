package remixware

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed licensetexts/*.txt
var licenseTextFS embed.FS

// standardLicenseText returns the standard text of a short permissive or
// common licence, without its "Copyright <year> <owner>" template line.
func standardLicenseText(id string) string {
	data, err := licenseTextFS.ReadFile("licensetexts/" + id + ".txt")
	if err != nil {
		return ""
	}
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "copyright") && strings.Contains(t, "<") {
			continue
		}
		keep = append(keep, strings.TrimRight(l, " "))
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// unfinishedMark ends every line remixware notice writes for the user to
// finish. An entry that still has one doesn't count as credit.
const unfinishedMark = "before you ship this"

// NoticeHeader starts a new third-party notices file.
const NoticeHeader = `# Third-party notices

This project includes code from the open source projects below. Each entry
keeps the copyright lines and licence text that the project's licence asks for.
`

type noticeEntry struct {
	src    Source
	usedIn []string
}

// NoticeResult is what Notices produced.
type NoticeResult struct {
	Text     string   // Markdown to add to the notices file
	Written  []string // components written
	Present  []string // components already in the existing notices
	Skipped  []string // components left out, with the reason
	Warnings []string // things to finish by hand
}

// Notices writes third-party notice entries for the code a project copied.
// existing is the current notices file, if any: components it already
// mentions are not written again. all writes entries for every finding that
// needs attribution, not only the ones still missing.
func Notices(res *Results, existing string, all bool) NoticeResult {
	var out NoticeResult
	existingLow := strings.ToLower(existing)
	byKey := map[string]*noticeEntry{}
	var order []string
	for _, f := range res.Findings {
		cat := licenseCategory(f.Source.License)
		switch {
		case f.Status == StatusConflict || f.Status == StatusNoLicense:
			out.Skipped = append(out.Skipped, fmt.Sprintf("%s (%s): %s, a notice doesn't fix it", f.Source.label(), f.File, strings.ReplaceAll(f.Status, "-", " ")))
			continue
		case cat == CatPublicDomain || cat == CatNone:
			continue
		case f.Status == StatusAttributed && !all:
			continue
		}
		e, ok := byKey[f.Source.Key]
		if !ok {
			e = &noticeEntry{src: f.Source}
			byKey[f.Source.Key] = e
			order = append(order, f.Source.Key)
		}
		loc := fmt.Sprintf("`%s` lines %s", f.File, f.Lines)
		if !containsString(e.usedIn, loc) {
			e.usedIn = append(e.usedIn, loc)
		}
		for _, c := range f.Source.Copyrights {
			if !containsString(e.src.Copyrights, c) {
				e.src.Copyrights = append(e.src.Copyrights, c)
			}
		}
	}
	sort.Strings(order)
	var b strings.Builder
	for _, key := range order {
		e := byKey[key]
		if existing != "" && strings.Contains(existingLow, "source: "+strings.ToLower(key)) {
			out.Present = append(out.Present, key)
			continue
		}
		writeNoticeEntry(&b, e, res.Texts[key], &out)
		out.Written = append(out.Written, key)
	}
	out.Text = b.String()
	return out
}

func writeNoticeEntry(b *strings.Builder, e *noticeEntry, texts *ComponentTexts, out *NoticeResult) {
	s := e.src
	fmt.Fprintf(b, "\n## %s\n\n", s.label())
	fmt.Fprintf(b, "- Source: %s\n", s.Key)
	fmt.Fprintf(b, "- Licence: %s\n", s.License)
	if s.URL != "" {
		fmt.Fprintf(b, "- Home: %s\n", s.URL)
	}
	fmt.Fprintf(b, "- Used in: %s\n\n", strings.Join(e.usedIn, ", "))
	// The component's own licence files come first. Every licence the
	// copy carries needs its text: a file headed Apache-2.0 in an MIT
	// project needs both.
	var parts []string
	covered := map[string]bool{}
	ids := noticeLicenseIDs(s.License)
	if texts != nil && strings.TrimSpace(texts.LicenseText) != "" {
		own := strings.TrimSpace(texts.LicenseText)
		parts = append(parts, own)
		for _, id := range licensesIn(normText(own)) {
			covered[strings.ToLower(id)] = true
		}
		if len(ids) == 1 {
			covered[strings.ToLower(ids[0])] = true // the text is that licence, even if unnamed
		}
	}
	for _, id := range ids {
		if covered[strings.ToLower(id)] {
			continue
		}
		switch t := standardLicenseText(id); {
		case t != "":
			parts = append(parts, t)
		case strings.HasPrefix(id, "LicenseRef-") || id == LicenseNoAssertion || id == LicenseNone:
			parts = append(parts, "Licence text: copy it from the source "+unfinishedMark+".")
			out.Warnings = append(out.Warnings, s.label()+": the licence text isn't known, copy it from the source")
		default:
			parts = append(parts, fmt.Sprintf("Full text of %s: https://spdx.org/licenses/%s.html (copy it here %s)", id, id, unfinishedMark))
			out.Warnings = append(out.Warnings, s.label()+": the "+id+" text isn't built in, copy it from https://spdx.org/licenses/"+id+".html")
		}
	}
	text := strings.Join(parts, "\n\n")
	// Copyright lines the licence text doesn't already carry.
	var lines []string
	lowText := strings.ToLower(text)
	for _, c := range s.Copyrights {
		if !strings.Contains(lowText, strings.ToLower(c)) {
			lines = append(lines, c)
		}
	}
	switch {
	case len(lines) > 0:
		for _, c := range lines {
			fmt.Fprintf(b, "%s  \n", c)
		}
		b.WriteString("\n")
	case len(s.Copyrights) == 0:
		b.WriteString("Copyright: not known. Copy the copyright line from the source " + unfinishedMark + ".\n\n")
		out.Warnings = append(out.Warnings, s.label()+": copyright line not known, copy it from the source")
	}
	b.WriteString("```text\n")
	b.WriteString(strings.ReplaceAll(text, "```", "'''"))
	b.WriteString("\n```\n")
	if texts != nil && texts.NoticeText != "" {
		b.WriteString("\nNOTICE file of this component:\n\n```text\n")
		b.WriteString(strings.ReplaceAll(texts.NoticeText, "```", "'''"))
		b.WriteString("\n```\n")
	}
}

// noticeLicenseIDs returns the licences whose text a notice must carry: all
// of them for AND, the most permissive option for OR.
func noticeLicenseIDs(expr string) []string {
	var walk func(e *licExpr) []string
	walk = func(e *licExpr) []string {
		switch e.op {
		case "OR":
			best := e.kids[0]
			for _, k := range e.kids[1:] {
				if exprCategory(k) < exprCategory(best) {
					best = k
				}
			}
			return walk(best)
		case "AND":
			var out []string
			for _, k := range e.kids {
				out = append(out, walk(k)...)
			}
			return out
		}
		return []string{e.id}
	}
	return dedupe(walk(parseLicenseExpr(expr)))
}
