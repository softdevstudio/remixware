package remixware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PolicyFile is the name of the policy file Remixware reads from the
// project folder when no other policy is named.
const PolicyFile = ".remixware.json"

// Policy says what the project allows. Every field is optional.
type Policy struct {
	License     string      `json:"license,omitempty"`      // the project's own licence, as an SPDX expression
	FailOn      []string    `json:"fail_on,omitempty"`      // statuses that make remixware check fail
	Allow       []AllowRule `json:"allow,omitempty"`        // matches accepted on purpose
	Ignore      []string    `json:"ignore,omitempty"`       // project paths not to scan
	NoticeFiles []string    `json:"notice_files,omitempty"` // where the project credits third-party code
	MinTokens   int         `json:"min_tokens,omitempty"`
	MinLines    int         `json:"min_lines,omitempty"`
	Deps        *DepsPolicy `json:"deps,omitempty"` // the dependency gate (remixware deps)

	Path string `json:"-"`
}

// AllowRule accepts matching findings. Set at least one of Component, File
// and License; all that are set must match. Reason is required.
type AllowRule struct {
	Component string `json:"component,omitempty"` // component name or key; * works as a wildcard
	File      string `json:"file,omitempty"`      // project path pattern
	License   string `json:"license,omitempty"`   // licence of the copied code
	Reason    string `json:"reason"`
}

// DefaultFailOn is what remixware check fails on without a policy.
var DefaultFailOn = []string{StatusConflict, StatusNoLicense, StatusAttributionMissing}

// LoadPolicy reads a policy file. Unknown fields are errors, so a typo
// can't quietly switch a rule off.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePolicy(data, path)
}

// ParsePolicy reads a policy file's contents; path names it in errors.
func ParsePolicy(data []byte, path string) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	p := &Policy{Path: path}
	if err := dec.Decode(p); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if p.FailOn != nil && len(p.FailOn) == 0 {
		// An empty list would make check pass whatever it finds.
		return nil, fmt.Errorf("%s: fail_on is empty; list the statuses that should fail the check, or leave fail_on out for the defaults (%s)",
			path, strings.Join(DefaultFailOn, ", "))
	}
	for _, s := range p.FailOn {
		if statusRank(s) >= len(AllStatuses) {
			return nil, fmt.Errorf("%s: fail_on has %q; statuses are %s", path, s, strings.Join(AllStatuses, ", "))
		}
	}
	for i, a := range p.Allow {
		if a.Component == "" && a.File == "" && a.License == "" {
			return nil, fmt.Errorf("%s: allow rule %d names no component, file or license", path, i+1)
		}
		if strings.TrimSpace(a.Reason) == "" {
			return nil, fmt.Errorf("%s: allow rule %d has no reason", path, i+1)
		}
	}
	if p.Deps != nil {
		if err := p.Deps.validate(path); err != nil {
			return nil, err
		}
	}
	if p.License != "" {
		p.License = NormalizeLicense(p.License)
	}
	return p, nil
}

// FindPolicy reads the policy file in the project folder, if there is one.
func FindPolicy(project string) (*Policy, error) {
	path := filepath.Join(project, PolicyFile)
	if !fileExists(path) {
		return &Policy{}, nil
	}
	return LoadPolicy(path)
}

// DetectProjectLicense finds the licence of the project in its own folder.
func DetectProjectLicense(project string) (expr, from string) {
	entries, err := os.ReadDir(project)
	if err != nil {
		return LicenseNone, ""
	}
	info := readDirInfo(project, entries)
	info.manifest.fromWorkspace("")
	if m := info.manifest; m != nil && m.license != "" {
		if l := NormalizeLicense(m.license); licenseCategory(l) != CatUnknown {
			return l, manifestFile(m.kind)
		}
	}
	var paths []string
	for _, f := range info.licenseFiles {
		paths = append(paths, filepath.Join(project, f))
	}
	if expr, from, _, _ := licenseFromFiles(paths); expr != "" {
		return expr, from
	}
	return LicenseNone, ""
}

var noticeName = regexp.MustCompile(`(?i)^(notice|third[-_ ]?party|thirdpartynotices|credits|attribution|acknowledg|open[-_]?source[-_]?(notices|licen[cs]es)|oss[-_]?(notices|attribution))`)

var noticeDirs = map[string]bool{"licenses": true, "third_party_licenses": true, "third-party-licenses": true,
	"notices": true, "licences": true, "third_party_notices": true}

// readNotices reads the files where a project credits third-party code.
func readNotices(project string, pol *Policy) (text string, files []string) {
	var b strings.Builder
	add := func(path string) {
		if b.Len() > 8<<20 {
			return
		}
		data := readSmall(path, 4<<20)
		if data == "" {
			return
		}
		b.WriteString(strings.ToLower(data))
		b.WriteByte('\n')
		files = append(files, relSlash(project, path))
	}
	if len(pol.NoticeFiles) > 0 {
		for _, f := range pol.NoticeFiles {
			p := filepath.Join(project, filepath.FromSlash(f))
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				sub, _ := os.ReadDir(p)
				for _, s := range sub {
					if !s.IsDir() {
						add(filepath.Join(p, s.Name()))
					}
				}
			} else if err == nil {
				add(p)
			}
		}
		return b.String(), files
	}
	entries, _ := os.ReadDir(project)
	for _, e := range entries {
		p := filepath.Join(project, e.Name())
		switch {
		case e.IsDir() && noticeDirs[strings.ToLower(e.Name())]:
			sub, _ := os.ReadDir(p)
			for _, s := range sub {
				if !s.IsDir() && noticeText(s.Name()) {
					add(filepath.Join(p, s.Name()))
				}
			}
		case !e.IsDir() && noticeName.MatchString(e.Name()) && noticeText(e.Name()):
			// attribution.go is code and ThirdParty.cmake a build file,
			// whatever their names say.
			add(p)
		}
	}
	return b.String(), files
}

// creditNeedles returns the strings that show a notice credits a source.
func creditNeedles(s Source) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if len(v) >= 4 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	// A name like "errors" or "click" is too common a word to count on its
	// own; "github.com/pkg/errors" or "click@8.1.7" isn't. The entries
	// remixware notice writes start with "Source: <key>", which always counts.
	add("source: " + s.Key)
	if strings.ContainsAny(s.Key, "/.@") {
		add(s.Key)
	}
	if strings.ContainsAny(s.Component, "/.") {
		add(s.Component)
	}
	if u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://"), "/"); strings.Contains(u, "/") {
		add(u)
	}
	if s.PURL != "" {
		p := s.PURL
		if i := strings.LastIndex(p, "@"); i > len("pkg:") {
			p = p[:i]
		}
		add(p)
	}
	// A full copyright line counts; a holder's name on its own doesn't,
	// because names like "The Go Authors" cover many projects.
	for _, c := range s.Copyrights {
		if len(c) >= 15 {
			add(strings.Join(strings.Fields(c), " "))
		}
	}
	return out
}

// credited reports whether lowercased notice text credits a source. Entries
// that remixware notice left for the user to finish don't count until they
// are finished, and a name only counts as a whole word:
// github.com/acme/tool-extras doesn't credit github.com/acme/tool.
func credited(text string, s Source) bool {
	if text == "" {
		return false
	}
	text = finishedNotices(text)
	// Cleaned as the source's names and copyright lines may have been.
	text = strings.Join(strings.Fields(oneLine(text)), " ")
	for _, n := range creditNeedles(s) {
		if containsWord(text, n) {
			return true
		}
	}
	return false
}

// unfinishedEntry reports whether the notices hold an entry for the source
// that remixware notice wrote and that still waits to be finished.
func unfinishedEntry(text string, s Source) bool {
	for _, p := range strings.Split(text, "\n## ") {
		if strings.Contains(p, unfinishedMark) && containsWord(strings.Join(strings.Fields(p), " "), "source: "+strings.ToLower(s.Key)) {
			return true
		}
	}
	return false
}

// finishedNotices drops the entries of a notices text that still say
// something has to be copied in "before you ship this".
func finishedNotices(text string) string {
	if !strings.Contains(text, unfinishedMark) {
		return text
	}
	parts := strings.Split(text, "\n## ")
	var keep []string
	for _, p := range parts {
		if !strings.Contains(p, unfinishedMark) {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "\n## ")
}

// containsWord reports whether needle occurs in text with no letter, digit,
// '_' or '-' right after it, and none of those or '.' right before it,
// where the needle itself starts or ends with one.
func containsWord(text, needle string) bool { return wordIndex(text, needle) >= 0 }

// wordIndex is where needle first occurs in text as a whole word, as
// containsWord decides it, or -1.
func wordIndex(text, needle string) int {
	if needle == "" {
		return -1
	}
	isWord := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
	}
	for i := 0; ; {
		j := strings.Index(text[i:], needle)
		if j < 0 {
			return -1
		}
		at, end := i+j, i+j+len(needle)
		before := at == 0 || !isWord(needle[0]) || !(isWord(text[at-1]) || text[at-1] == '.')
		after := end == len(text) || !isWord(needle[len(needle)-1]) || !isWord(text[end])
		if before && after {
			return at
		}
		i = at + 1
	}
}

// keepsNotice reports whether the copied code still carries its original
// copyright line and licence notice in the file itself, as happens when a
// whole file is copied with its header.
func keepsNotice(path, fileText string, s Source) bool {
	if len(s.Copyrights) == 0 {
		return false
	}
	// The source's copyright lines may have been cleaned (see oneLine), so
	// the file is cleaned the same way before they're looked for in it.
	low := strings.ToLower(textBlock(fileText))
	found := false
	for _, c := range s.Copyrights {
		if len(c) >= 15 && strings.Contains(low, strings.ToLower(c)) {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	head := leadingComments([]byte(fileText), languageFor(path), 32<<10)
	id := spdxTagLicense(head)
	if id == "" {
		id = headerLicense(head)
	}
	return id != "" && sameLicense(id, s.License)
}

// needsNotice reports whether code under a licence has to ship with its
// copyright line and licence text, as every open source licence short of
// the public domain asks. For a choice, the option that asks least counts.
func needsNotice(expr string) bool {
	for _, id := range noticeLicenseIDs(expr) {
		switch l := lookupLicense(id); {
		case l.Cat == CatPermissive, l.Cat == CatWeakCopyleft, l.Cat == CatCopyleft, l.Cat == CatNetworkCopyleft, l.ID == "JSON":
			return true
		}
	}
	return false
}

// Evaluate gives every finding a status and a reason, given the project's
// licence, its notice files and the policy. licenseOverride, when set, is
// the project's licence and wins over the policy and the licence files.
func Evaluate(res *Results, pol *Policy, licenseOverride string) {
	if pol == nil {
		pol = &Policy{}
	}
	switch {
	case licenseOverride != "":
		res.ProjectLicense, res.ProjectLicenseFrom = NormalizeLicense(licenseOverride), "--license"
	case pol.License != "":
		res.ProjectLicense, res.ProjectLicenseFrom = pol.License, filepath.Base(pol.Path)
	default:
		res.ProjectLicense, res.ProjectLicenseFrom = DetectProjectLicense(res.Project)
	}
	res.ProjectNote = classifyProject(parseLicenseExpr(res.ProjectLicense).leaves()[0].id).note
	notices, files := readNotices(res.Project, pol)
	res.NoticeFiles = files
	near := newNearbyNotices(res.Project, append(append([]string{}, res.Exclude...), pol.Ignore...))

	fileTexts := map[string]string{}
	fileText := func(rel string) string {
		if t, ok := fileTexts[rel]; ok {
			return t
		}
		t := readSmall(filepath.Join(res.Project, filepath.FromSlash(rel)), 4<<20)
		fileTexts[rel] = t
		return t
	}

	var kept []Finding
	for _, f := range res.Findings {
		if matchAny(pol.Ignore, f.File) {
			continue
		}
		f.Status, f.Reason, f.Allowed, f.Credited = "", "", "", false
		v := evaluate(f.Source.License, res.ProjectLicense)
		f.Reason = v.reason
		notice := false // the copyright line and licence text have to ship with the copy
		switch v.status {
		case baseOK:
			f.Status = StatusOK
		case baseAttribution:
			f.Status, notice = StatusAttributed, true
		case baseReview:
			f.Status, notice = StatusReview, needsNotice(f.Source.License)
		case baseNoLicense:
			f.Status = StatusNoLicense
		default:
			f.Status = StatusConflict
		}
		// The same code also appears elsewhere under stricter terms: it
		// matters which one it was copied from.
		if f.Status == StatusOK || f.Status == StatusAttributed {
			for _, a := range f.Also {
				if av := evaluate(a.License, res.ProjectLicense); baseRank[av.status] > baseRank[v.status] {
					f.Status = StatusReview
					f.Reason = fmt.Sprintf("The same code is also in %s under %s, whose terms are stricter: check which one it came from. %s",
						a.label(), a.License, f.Reason)
					break
				}
			}
		}
		// A copy that needs a review needs its notice all the same, and a
		// review doesn't fail a check by default: until the notice is
		// there, the copy counts as attribution missing.
		if notice {
			var by, textOnly string
			var withCopy bool
			switch {
			case credited(notices, f.Source):
				f.Reason, f.Credited = "Credited in the project's notices. "+f.Reason, true
			case keepsNotice(f.File, fileText(f.File), f.Source):
				f.Reason, f.Credited = "The file keeps the original copyright and licence notice. "+f.Reason, true
			case func() bool {
				by, withCopy, textOnly = near.credit(f.File, f.Source, res.Texts[f.Source.Key])
				return by != ""
			}():
				f.Credited = true
				if withCopy {
					f.Reason = "Credited in " + by + ", kept with the copy. " + f.Reason
				} else {
					f.Reason = "Credited in " + by + ". " + f.Reason
				}
			default:
				if f.Status == StatusReview {
					f.Reason += " Its copyright line and licence text also have to ship with your copy: until your notices carry them, this counts as attribution missing."
				}
				f.Status = StatusAttributionMissing
				switch {
				case unfinishedEntry(notices, f.Source):
					f.Reason += " Its entry in the notices isn't finished: fill in what it says to copy there."
				case textOnly != "":
					f.Reason += " Its licence text is kept with the copy, in " + textOnly + ", but its NOTICE text isn't."
				case credited(strings.ToLower(fileText(f.File)), f.Source):
					f.Reason += " The file names its source, but the copyright line and licence text also have to ship with the code."
				}
			}
		}
		if r, ok := allowRule(pol, f); ok {
			f.Allowed = r.Reason
			f.Reason = "Allowed by policy: " + r.Reason + ". Otherwise: " + statusLabel(f.Status) + ", " + f.Reason
			f.Status = StatusAllowed
		}
		kept = append(kept, f)
	}
	res.Findings = kept
	// Credit in the files left unread can't count: say so.
	if res.Skipped != nil {
		delete(res.Skipped, "licence-files")
	}
	if len(near.unread) > 0 {
		if res.Skipped == nil {
			res.Skipped = map[string]int{}
		}
		res.Skipped["licence-files"] = len(near.unread)
	}
	res.recount()
}

func allowRule(pol *Policy, f Finding) (AllowRule, bool) {
	for _, r := range pol.Allow {
		if r.Component != "" && !wildcard(r.Component, f.Source.Component) && !wildcard(r.Component, f.Source.Key) {
			continue
		}
		if r.File != "" && !globMatch(r.File, f.File) {
			continue
		}
		if r.License != "" && !sameLicense(r.License, f.Source.License) {
			continue
		}
		return r, true
	}
	return AllowRule{}, false
}

func wildcard(pattern, s string) bool {
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, s)
	}
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.ToLower(pattern)), `\*`, ".*") + "$"
	ok, _ := regexp.MatchString(re, strings.ToLower(s))
	return ok
}

// Failures returns the findings whose status is in failOn.
func Failures(res *Results, failOn []string) []Finding {
	var out []Finding
	for _, f := range res.Findings {
		if containsString(failOn, f.Status) {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return statusRank(out[i].Status) > statusRank(out[j].Status) })
	return out
}
