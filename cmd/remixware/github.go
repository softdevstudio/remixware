package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/softdevstudio/remixware"
)

// GitHub output for check and deps: workflow-command annotations
// (--format github) and a Markdown step summary (--summary FILE, appended,
// for $GITHUB_STEP_SUMMARY). Text a project, an index or a registry
// supplies is cleaned before it goes out: control characters can't end a
// workflow command or start another, and Markdown and HTML in a summary
// cell are shown as text.

// summaryRows caps the rows of a summary table; the rest are counted.
const summaryRows = 100

// oneLineText cleans text for a single output line.
func oneLineText(s string) string {
	s = remixware.TerminalText(s)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, string([]rune{0xFFFD}), " ")), " ")
}

// ghData escapes a workflow command's message.
func ghData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	return strings.ReplaceAll(s, "\n", "%0A")
}

// ghProp escapes a workflow command's property value.
func ghProp(s string) string {
	s = ghData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	return strings.ReplaceAll(s, ",", "%2C")
}

// ghAnnotation writes one ::error or ::warning line. line and endLine are
// left out when 0.
func ghAnnotation(w io.Writer, level, file string, line, endLine int, title, msg string) {
	props := []string{}
	if file != "" {
		props = append(props, "file="+ghProp(oneLineText(file)))
		if line > 0 {
			props = append(props, fmt.Sprintf("line=%d", line))
			if endLine > line {
				props = append(props, fmt.Sprintf("endLine=%d", endLine))
			}
		}
	}
	props = append(props, "title="+ghProp(oneLineText(title)))
	fmt.Fprintf(w, "::%s %s::%s\n", level, strings.Join(props, ","), ghData(oneLineText(msg)))
}

// workspacePath turns a path relative to the project folder into one
// relative to the working folder, which is the workspace in GitHub Actions
// and the root annotations are placed against.
func workspacePath(project, rel string) string {
	p := filepath.Join(project, filepath.FromSlash(rel))
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if s := shownPath(p); !filepath.IsAbs(s) {
		return s
	}
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, p); err == nil {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(p)
}

// mdCell makes text safe for a Markdown table cell in a GitHub summary,
// which renders HTML as well.
func mdCell(s string) string {
	s = oneLineText(s)
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", `\|`, "`", "&#96;",
		"*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "~", `\~`, "#", `\#`, "!", `\!`)
	return r.Replace(s)
}

// mdCode puts text in a code span for a table cell.
func mdCode(s string) string {
	s = oneLineText(s)
	s = strings.NewReplacer("|", `\|`, "`", "'").Replace(s)
	return "`" + s + "`"
}

func appendSummary(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("--summary: %v", err)
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("--summary: %v", err)
	}
	return f.Close()
}

// --- check -------------------------------------------------------------------

func findingSource(f remixware.Finding) string {
	src := f.Source.Component
	if f.Source.Version != "" {
		src += " " + f.Source.Version
	}
	if src == "" {
		src = "unknown source"
	}
	return src
}

func checkAnnotations(w io.Writer, project string, fails, warns []remixware.Finding) {
	emit := func(level string, f remixware.Finding) {
		lic := f.Source.License
		if lic == "" {
			lic = "no licence found"
		}
		title := "Remixware: " + strings.ReplaceAll(f.Status, "-", " ")
		msg := fmt.Sprintf("Copied from %s (%s). %s", findingSource(f), lic, f.Reason)
		ghAnnotation(w, level, workspacePath(project, f.File), f.Lines.Start, f.Lines.End, title, msg)
	}
	for _, f := range fails {
		emit("error", f)
	}
	for _, f := range warns {
		emit("warning", f)
	}
}

func checkSummary(project string, fails, warns []remixware.Finding, failOn []string, res *remixware.Results) string {
	var b strings.Builder
	verdict := "PASS"
	if len(fails) > 0 {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "### Remixware check: %s\n\n", verdict)
	fmt.Fprintf(&b, "%s. %d %s break the policy (fail on: %s).\n\n", mdCell(strings.TrimSuffix(summary(res), ".")), len(fails),
		plural(len(fails), "finding", "findings"), mdCell(strings.Join(failOn, ", ")))
	rows := append(append([]remixware.Finding(nil), fails...), warns...)
	if len(rows) > 0 {
		b.WriteString("| Status | Copy | Copied from | Licence |\n|---|---|---|---|\n")
		for i, f := range rows {
			if i == summaryRows {
				fmt.Fprintf(&b, "\nAnd %d more; the JSON and HTML reports list them all.\n", len(rows)-summaryRows)
				break
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", mdCell(f.Status),
				mdCode(fmt.Sprintf("%s:%s", workspacePath(project, f.File), f.Lines)), mdCell(findingSource(f)), mdCell(f.Source.License))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// --- deps --------------------------------------------------------------------

func depVersions(c remixware.DepChange) string {
	from, to := strings.Join(c.From, ", "), strings.Join(c.To, ", ")
	switch {
	case from != "" && to != "":
		return from + " -> " + to
	case to != "":
		return to
	}
	return from
}

// depProblem says what's wrong with a change, as the text output does: the
// change's reason, then the reasons of its failing issues that the change's
// reason doesn't already give (install scripts, publisher changes).
func depProblem(c remixware.DepChange, failOn []string) string {
	parts := []string{strings.TrimSuffix(c.Reason, ".") + "."}
	for _, is := range c.Issues {
		if !containsString(failOn, is.Status) || is.Reason == "" ||
			is.Status == remixware.DepNotAllowed || is.Status == remixware.DepNotChecked {
			continue
		}
		r := is.Reason
		if is.Version != "" && len(c.To) > 1 {
			r = is.Version + ": " + r
		}
		parts = append(parts, strings.TrimSuffix(r, ".")+".")
	}
	if len(parts) > 1 && c.Change == "updated" {
		parts = parts[1:] // "At a new version." says nothing the versions don't
	}
	return strings.Join(parts, " ")
}

func depAnnotations(w io.Writer, project string, fails []remixware.DepChange, failOn []string) {
	for _, c := range fails {
		lf := ""
		if len(c.Lockfiles) > 0 {
			lf = c.Lockfiles[0]
		}
		file, line := "", 0
		if lf != "" {
			file = workspacePath(project, lf)
			if c.Name != "" {
				line = lineNaming(filepath.Join(project, filepath.FromSlash(lf)), c.Name)
			}
		}
		what := c.Package
		if what == "" {
			what = lf
		}
		title := "Remixware deps: " + c.Status + " " + what
		msg := depProblem(c, failOn)
		if v := depVersions(c); v != "" {
			msg = c.Change + " " + v + ". " + msg
		}
		ghAnnotation(w, "error", file, line, 0, title, msg)
	}
}

// lineNaming finds the first line of a lockfile that names a package, for
// an annotation's line number, or 0. It reads the change's side only: a
// removed package's line isn't in the file.
func lineNaming(path, name string) int {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 64<<20 {
		return 0
	}
	re, err := regexp.Compile(`(?i)(^|[^A-Za-z0-9._\-@])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9._\-/])`)
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(data), "\n") {
		if re.MatchString(line) {
			return i + 1
		}
	}
	return 0
}

func depsSummaryMarkdown(project string, res *remixware.DepsResult, fails []remixware.DepChange, failOn []string) string {
	var b strings.Builder
	verdict := "PASS"
	if len(fails) > 0 {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "### Remixware deps: %s\n\n", verdict)
	fmt.Fprintf(&b, "%s. %d %s break the policy (fail on: %s).\n\n", mdCell(strings.TrimSuffix(depsSummary(res), ".")), len(fails),
		plural(len(fails), "change", "changes"), mdCell(strings.Join(failOn, ", ")))
	if len(res.Changes) > 0 {
		b.WriteString("| Status | Package | Change | Versions | Lockfile |\n|---|---|---|---|---|\n")
		// Failures first, then the rest in their order.
		rows := append([]remixware.DepChange(nil), fails...)
		failed := map[int]bool{}
		for i, c := range res.Changes {
			for _, f := range fails {
				if f.Package == c.Package && strings.Join(f.Lockfiles, ",") == strings.Join(c.Lockfiles, ",") && f.Change == c.Change {
					failed[i] = true
				}
			}
		}
		for i, c := range res.Changes {
			if !failed[i] {
				rows = append(rows, c)
			}
		}
		for i, c := range rows {
			if i == summaryRows {
				fmt.Fprintf(&b, "\nAnd %d more; the JSON report lists them all.\n", len(rows)-summaryRows)
				break
			}
			lf := ""
			if len(c.Lockfiles) > 0 {
				lf = mdCode(workspacePath(project, c.Lockfiles[0]))
			}
			pkg := c.Package
			if pkg == "" {
				pkg = "(lockfile)"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", mdCell(c.Status), mdCell(pkg), mdCell(c.Change), mdCell(depVersions(c)), lf)
		}
		b.WriteString("\n")
	}
	if len(res.NewAllowRules) > 0 {
		b.WriteString("Allow rules this change adds to the policy (review them: a change can allow its own packages):\n\n")
		for _, r := range res.NewAllowRules {
			fmt.Fprintf(&b, "- %s: %s\n", mdCode(r.Package), mdCell(r.Reason))
		}
		b.WriteString("\n")
	}
	if len(res.PolicyChanges) > 0 {
		b.WriteString("Settings this change alters in the policy:\n\n")
		for _, c := range res.PolicyChanges {
			fmt.Fprintf(&b, "- %s\n", mdCode(c))
		}
		b.WriteString("\n")
	}
	var skipped []string
	for _, l := range res.Lockfiles {
		if l.Skipped != "" {
			skipped = append(skipped, "- "+mdCode(workspacePath(project, l.Path))+": "+mdCell(l.Skipped))
		}
	}
	if len(skipped) > 0 {
		b.WriteString("Lockfiles skipped (the change leaves them as they are, and they can't be read):\n\n" + strings.Join(skipped, "\n") + "\n\n")
	}
	b.WriteString("Checks:\n\n")
	for _, c := range res.Checks {
		fmt.Fprintf(&b, "- %s: %s\n", mdCell(checkLabels[c.Name]), mdCell(checkText(c, res.Online)))
	}
	b.WriteString("\n")
	return b.String()
}

// outputFlags are --format and --summary, shared by check and deps.
type outputFlags struct {
	format  string
	summary string
}

func (o *outputFlags) validate() error {
	switch o.format {
	case "", "text", "github":
		return nil
	}
	return fmt.Errorf("--format: %q isn't a format; use text or github", o.format)
}

func (o *outputFlags) github() bool { return o.format == "github" }
