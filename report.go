package remixware

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed report.html.tmpl
var reportTemplate string

// WriteJSON writes the results as the findings file.
func WriteJSON(w io.Writer, res *Results) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	out := *res
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	if out.Inputs == nil {
		out.Inputs = []Input{}
	}
	return enc.Encode(&out)
}

type excerpt struct {
	Lines []excerptLine
	Note  string
}

type excerptLine struct {
	N    int
	Text string
	Gap  bool
}

type reportFinding struct {
	Finding
	StatusLabel string
	Yours       *excerpt
	Theirs      *excerpt
	Coverage    string
	Kind        string
}

type reportData struct {
	*Results
	Name         string
	ProjectShown string
	Statuses     []statusCount
	Items        []reportFinding
	InputsText   string
	SkippedText  string
}

type statusCount struct {
	Status, Label string
	Count         int
}

// WriteHTML writes a self-contained HTML report.
func WriteHTML(w io.Writer, res *Results) error {
	t, err := template.New("report").Funcs(template.FuncMap{
		"comma": comma,
	}).Parse(reportTemplate)
	if err != nil {
		return err
	}
	d := reportData{Results: res, Name: filepath.Base(res.Project), ProjectShown: res.Project}
	// Show a project inside the current folder by its relative path.
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, res.Project); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			d.ProjectShown = filepath.ToSlash(rel)
		}
	}
	for i := len(AllStatuses) - 1; i >= 0; i-- {
		s := AllStatuses[i]
		if n := res.Counts[s]; n > 0 {
			d.Statuses = append(d.Statuses, statusCount{s, statusLabel(s), n})
		}
	}
	items := append([]Finding(nil), res.Findings...)
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := statusRank(items[i].Status), statusRank(items[j].Status); a != b {
			return a > b
		}
		if items[i].File != items[j].File {
			return items[i].File < items[j].File
		}
		return items[i].Lines.Start < items[j].Lines.Start
	})
	for _, f := range items {
		rf := reportFinding{Finding: f, StatusLabel: statusLabel(f.Status), Kind: capitalize(kindText(f.Match))}
		if f.Match.Coverage > 0 {
			rf.Coverage = fmt.Sprintf("%.0f%%", f.Match.Coverage*100)
		}
		rf.Yours = readExcerpt(filepath.Join(res.Project, filepath.FromSlash(f.File)), f.Lines)
		if f.Source.Abs != "" {
			rf.Theirs = readExcerpt(f.Source.Abs, f.Source.Lines)
		}
		d.Items = append(d.Items, rf)
	}
	var inputs []string
	for _, in := range res.Inputs {
		switch in.Kind {
		case "index":
			inputs = append(inputs, fmt.Sprintf("%s (index: %s components, %s files)", filepath.Base(in.Path), comma(in.Components), comma(in.Files)))
		case "scanoss":
			inputs = append(inputs, fmt.Sprintf("%s (SCANOSS results: %s matches)", filepath.Base(in.Path), comma(in.Matches)))
		default:
			inputs = append(inputs, fmt.Sprintf("%s (earlier Remixware findings)", filepath.Base(in.Path)))
		}
	}
	d.InputsText = strings.Join(inputs, "; ")
	var skipped []string
	keys := make([]string, 0, len(res.Skipped))
	for k := range res.Skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		skipped = append(skipped, fmt.Sprintf("%s %s", comma(res.Skipped[k]), skippedText(k)))
	}
	d.SkippedText = strings.Join(skipped, ", ")
	return t.Execute(w, d)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func kindText(m Match) string {
	switch m.Kind {
	case "verbatim":
		if m.WholeFile {
			return "whole file, copied as is"
		}
		return "copied as is"
	case "edited":
		return "copied with some edits"
	case "renamed":
		return "copied with names changed"
	case "file":
		return "whole file (SCANOSS)"
	case "snippet":
		return "snippet (SCANOSS)"
	}
	return m.Kind
}

func skippedText(k string) string {
	switch k {
	case "declared":
		return "in folders the index already covers"
	case "generated":
		return "generated"
	case "minified":
		return "minified"
	case "binary":
		return "binary"
	case "too-large":
		return "over 2 MB"
	case "unreadable":
		return "unreadable"
	}
	return k
}

func readExcerpt(path string, r Range) *excerpt {
	if r.Start <= 0 || r.End < r.Start {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if r.Start > len(lines) {
		return nil
	}
	end := min(r.End, len(lines))
	ex := &excerpt{}
	add := func(i int) {
		t := strings.ReplaceAll(lines[i-1], "\t", "    ")
		if rs := []rune(t); len(rs) > 200 {
			t = string(rs[:200]) + "…"
		}
		ex.Lines = append(ex.Lines, excerptLine{N: i, Text: t})
	}
	if end-r.Start+1 <= 60 {
		for i := r.Start; i <= end; i++ {
			add(i)
		}
		return ex
	}
	for i := r.Start; i < r.Start+40; i++ {
		add(i)
	}
	ex.Lines = append(ex.Lines, excerptLine{Gap: true, Text: fmt.Sprintf("… %d lines not shown …", end-r.Start+1-55)})
	for i := end - 14; i <= end; i++ {
		add(i)
	}
	return ex
}

func comma(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
