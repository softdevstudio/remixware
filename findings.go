package remixware

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Range is a span of lines, both ends included. It is written as [start, end].
type Range struct{ Start, End int }

func (r Range) MarshalJSON() ([]byte, error) { return json.Marshal([2]int{r.Start, r.End}) }

func (r *Range) UnmarshalJSON(b []byte) error {
	var a [2]int
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	r.Start, r.End = a[0], a[1]
	return nil
}

func (r Range) String() string {
	if r.Start == 0 && r.End == 0 {
		return "?"
	}
	if r.Start == r.End {
		return fmt.Sprint(r.Start)
	}
	return fmt.Sprintf("%d-%d", r.Start, r.End)
}

func (r Range) overlap(o Range) int {
	lo, hi := max(r.Start, o.Start), min(r.End, o.End)
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

func (r Range) len() int { return r.End - r.Start + 1 }

// Source says where copied code came from.
type Source struct {
	Key           string   `json:"key"`
	Component     string   `json:"component"`
	Version       string   `json:"version,omitempty"`
	OtherVersions []string `json:"other_versions,omitempty"`
	File          string   `json:"file,omitempty"`
	Lines         Range    `json:"lines"`
	URL           string   `json:"url,omitempty"`
	PURL          string   `json:"purl,omitempty"`
	License       string   `json:"license"`
	LicenseFrom   string   `json:"license_from,omitempty"`
	Copyrights    []string `json:"copyrights,omitempty"`
	Via           string   `json:"via"` // "index" or "scanoss"
	Abs           string   `json:"abs,omitempty"`
}

// ComponentTexts holds the licence and NOTICE text of a component, kept once
// per component in a results file so notices can be written from it later.
type ComponentTexts struct {
	LicenseText string `json:"license_text,omitempty"`
	NoticeText  string `json:"notice_text,omitempty"`
}

func (s Source) label() string {
	out := s.Component
	if s.Version != "" {
		out += " " + s.Version
	}
	return out
}

// Match describes how closely the code matches its source.
type Match struct {
	// verbatim: same code, same names. edited: same code with some changes.
	// renamed: same structure, names changed. file: a whole file (SCANOSS).
	// snippet: part of a file (SCANOSS).
	Kind      string  `json:"kind"`
	Coverage  float64 `json:"coverage"`
	Tokens    int     `json:"tokens,omitempty"`
	WholeFile bool    `json:"whole_file,omitempty"`
}

// Finding is one stretch of code that matches third-party code.
type Finding struct {
	ID      string   `json:"id"`
	File    string   `json:"file"`
	Lines   Range    `json:"lines"`
	Source  Source   `json:"source"`
	Match   Match    `json:"match"`
	Also    []Source `json:"also,omitempty"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason"`
	Allowed string   `json:"allowed,omitempty"`

	score float64
}

// Statuses, from fine to failing.
const (
	StatusOK                 = "ok"
	StatusAllowed            = "allowed"
	StatusAttributed         = "attributed"
	StatusAttributionMissing = "attribution-missing"
	StatusReview             = "review"
	StatusNoLicense          = "no-license"
	StatusConflict           = "conflict"
)

// AllStatuses lists every status, from fine to failing.
var AllStatuses = []string{StatusOK, StatusAllowed, StatusAttributed, StatusAttributionMissing, StatusReview,
	StatusNoLicense, StatusConflict}

func statusRank(s string) int {
	for i, x := range AllStatuses {
		if x == s {
			return i
		}
	}
	return len(AllStatuses)
}

func statusLabel(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", " "))
}

// Results is everything one scan found.
type Results struct {
	Tool               string                     `json:"tool"`
	Created            string                     `json:"created"`
	Project            string                     `json:"project"`
	ProjectLicense     string                     `json:"project_license"`
	ProjectLicenseFrom string                     `json:"project_license_from,omitempty"`
	ProjectNote        string                     `json:"project_note,omitempty"`
	Inputs             []Input                    `json:"inputs"`
	FilesScanned       int                        `json:"files_scanned"`
	LinesScanned       int                        `json:"lines_scanned"`
	Skipped            map[string]int             `json:"skipped,omitempty"`
	MinTokens          int                        `json:"min_tokens,omitempty"`
	MinLines           int                        `json:"min_lines,omitempty"`
	NoticeFiles        []string                   `json:"notice_files,omitempty"`
	Findings           []Finding                  `json:"findings"`
	Counts             map[string]int             `json:"counts"`
	Texts              map[string]*ComponentTexts `json:"texts,omitempty"`
}

// Input is one index or result file the project was compared with.
type Input struct {
	Kind       string `json:"kind"` // index, scanoss, remixware
	Path       string `json:"path"`
	Components int    `json:"components,omitempty"`
	Files      int    `json:"files,omitempty"`
	Matches    int    `json:"matches,omitempty"`
}

func findingID(file string, s Source) string {
	h := sha1.Sum([]byte(file + "\x00" + s.Component + "\x00" + s.File))
	return "RW-" + hex.EncodeToString(h[:4])
}

// mergeFindings clusters findings that cover the same lines of the same
// file and keeps the best match of each cluster, listing the others as
// other places the same code was found.
func mergeFindings(in []Finding) []Finding {
	in = joinPieces(in)
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].File != in[j].File {
			return in[i].File < in[j].File
		}
		if in[i].score != in[j].score {
			return in[i].score > in[j].score
		}
		return in[i].Source.Key < in[j].Source.Key
	})
	var out []Finding
	start := 0
	for i := 0; i <= len(in); i++ {
		if i < len(in) && in[i].File == in[start].File {
			continue
		}
		group := in[start:i]
		var kept []Finding
		for _, f := range group {
			merged := false
			for k := range kept {
				p := &kept[k]
				ov := p.Lines.overlap(f.Lines)
				if ov*2 >= min(p.Lines.len(), f.Lines.len()) {
					p.addAlso(f.Source)
					for _, a := range f.Also {
						p.addAlso(a)
					}
					merged = true
					break
				}
			}
			if !merged {
				kept = append(kept, f)
			}
		}
		out = append(out, kept...)
		start = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Lines.Start < out[j].Lines.Start
	})
	return out
}

// joinPieces joins matches of one file against the same source file that
// overlap or nearly touch: an edit in the middle of a copied function
// splits its match in two, but it is still one copy.
func joinPieces(in []Finding) []Finding {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Source.Key != b.Source.Key {
			return a.Source.Key < b.Source.Key
		}
		if a.Source.File != b.Source.File {
			return a.Source.File < b.Source.File
		}
		return a.Lines.Start < b.Lines.Start
	})
	var out []Finding
	for _, f := range in {
		if n := len(out); n > 0 {
			p := &out[n-1]
			scanGap := f.Lines.Start - p.Lines.End
			srcGap := f.Source.Lines.Start - p.Source.Lines.End
			forward := f.Source.Lines.Start >= p.Source.Lines.Start-3
			// Pieces join when they touch on one side and are near on the
			// other: code removed from the copy leaves a gap in the source,
			// code added to it a gap in the copy.
			near := forward && ((scanGap <= 3 && srcGap <= 60) || (srcGap <= 3 && scanGap <= 60))
			if p.File == f.File && p.Source.Key == f.Source.Key && p.Source.File == f.Source.File &&
				p.Source.Via == f.Source.Via && near {
				total := p.Match.Tokens + f.Match.Tokens
				if total > 0 {
					p.Match.Coverage = round2((p.Match.Coverage*float64(p.Match.Tokens) + f.Match.Coverage*float64(f.Match.Tokens)) / float64(total))
				}
				p.Match.Tokens = total
				p.Match.WholeFile = p.Match.WholeFile || f.Match.WholeFile
				if p.Match.Kind != f.Match.Kind {
					p.Match.Kind = joinKind(p.Match.Kind, f.Match.Kind)
				}
				if p.Match.Kind == "verbatim" && (scanGap > 3 || srcGap > 3) {
					p.Match.Kind = "edited" // part of it was removed or added
				}
				p.Lines.End = max(p.Lines.End, f.Lines.End)
				p.Source.Lines.Start = min(p.Source.Lines.Start, f.Source.Lines.Start)
				p.Source.Lines.End = max(p.Source.Lines.End, f.Source.Lines.End)
				p.score += f.score
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

func joinKind(a, b string) string {
	if a == "renamed" || b == "renamed" {
		if a == "verbatim" || b == "verbatim" {
			return "edited"
		}
		return "renamed"
	}
	if a == "verbatim" && b == "verbatim" {
		return "verbatim"
	}
	return "edited"
}

func (f *Finding) addAlso(s Source) {
	if s.Component == f.Source.Component {
		if s.Version != f.Source.Version && s.Version != "" && !containsString(f.Source.OtherVersions, s.Version) {
			f.Source.OtherVersions = append(f.Source.OtherVersions, s.Version)
		}
		return
	}
	for i, a := range f.Also {
		if a.Component == s.Component {
			if s.Version != a.Version && s.Version != "" && !containsString(a.OtherVersions, s.Version) {
				f.Also[i].OtherVersions = append(f.Also[i].OtherVersions, s.Version)
			}
			return
		}
	}
	if len(f.Also) < 10 {
		f.Also = append(f.Also, s)
	}
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (r *Results) recount() {
	r.Counts = map[string]int{}
	for _, f := range r.Findings {
		r.Counts[f.Status]++
	}
}

// NewResults starts empty results for a project that is not scanned
// locally, only compared through imported result files.
func NewResults(project string) (*Results, error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, fmt.Errorf("%s is a file: name the project folder", project)
	}
	return &Results{Tool: "remixware " + Version, Created: time.Now().UTC().Format(time.RFC3339), Project: abs,
		Skipped: map[string]int{}, Texts: map[string]*ComponentTexts{}}, nil
}

// Add brings in findings from an imported results file.
func (r *Results) Add(findings []Finding, in Input, texts map[string]*ComponentTexts) {
	r.Inputs = append(r.Inputs, in)
	for k, v := range texts {
		if r.Texts == nil {
			r.Texts = map[string]*ComponentTexts{}
		}
		if _, ok := r.Texts[k]; !ok {
			r.Texts[k] = v
		}
	}
	for i := range findings {
		if findings[i].score == 0 {
			findings[i].score = float64(findings[i].Match.Tokens) + findings[i].Match.Coverage*float64(findings[i].Lines.len())
		}
	}
	r.Findings = mergeFindings(append(r.Findings, findings...))
}
