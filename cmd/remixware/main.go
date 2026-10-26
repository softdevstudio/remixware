// Command remixware finds code a project copied from other projects, names
// the licence it came with, and says what that licence asks.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/softdevstudio/remixware"
)

const usage = `Remixware finds code your project copied from other projects, names the
licence it came with, and tells you what that licence asks of you.

Usage:
  remixware index <folder>... [--out FILE]     fingerprint third-party code into an index
  remixware scan [project] --index FILE        compare a project with indexes, list the matches
  remixware check [project] --index FILE       the same, as a pass/fail gate for CI
  remixware notice [project] --index FILE      write third-party notices for what you copied
  remixware licenses                           list the licences Remixware knows
  remixware version

scan, check and notice compare the project with --index files made by
remixware index, with --from results files written by the SCANOSS scanner
(scanoss-py scan -o results.json), or with both.

Run "remixware help <command>" for a command's flags.
Remixware flags licensing questions. It is not legal advice.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes.
const (
	exitOK    = 0
	exitFail  = 1 // check found findings that break the policy
	exitUsage = 2 // bad arguments, unreadable files and other errors
)

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	var err error
	code := exitOK
	switch cmd {
	case "index":
		err = cmdIndex(rest, stdout, stderr)
	case "scan":
		err = cmdScan(rest, stdout, stderr)
	case "check":
		code, err = cmdCheck(rest, stdout, stderr)
	case "notice", "notices":
		err = cmdNotice(rest, stdout, stderr)
	case "licenses", "licences":
		cmdLicenses(stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "remixware "+remixware.Version)
	case "help", "-h", "--help":
		if len(rest) > 0 {
			return helpFor(rest[0], stdout, stderr)
		}
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "remixware: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintln(stderr, "remixware:", err)
		return exitUsage
	}
	return code
}

func helpFor(cmd string, stdout, stderr io.Writer) int {
	var fs *flag.FlagSet
	switch cmd {
	case "index":
		fs, _ = indexFlags(stdout)
	case "scan", "check", "notice", "notices":
		fs, _ = compareFlags(cmd, stdout)
	default:
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	fs.SetOutput(stdout)
	fs.Usage()
	return exitOK
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// parse reads flags that may come before or after the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

type indexOpts struct {
	out, name, version, license string
	exclude                     multi
	maxDF                       int
	quiet                       bool
}

func indexFlags(stderr io.Writer) (*flag.FlagSet, *indexOpts) {
	o := &indexOpts{}
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.out, "out", "remixware.rmx", "index file to write")
	fs.StringVar(&o.name, "name", "", "component name for the folder (one folder only)")
	fs.StringVar(&o.version, "version", "", "component version for the folder (one folder only)")
	fs.StringVar(&o.license, "license", "", "licence of all the code, as an SPDX expression, instead of what Remixware finds")
	fs.Var(&o.exclude, "exclude", "leave out paths matching this pattern (repeatable)")
	fs.IntVar(&o.maxDF, "max-common", 0, "drop fingerprints found in more than this many components (0 picks a value, -1 keeps all)")
	fs.BoolVar(&o.quiet, "quiet", false, "print nothing but errors")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: remixware index <folder>... [flags]\n\nFingerprints the source files in third-party code (a vendor folder, node_modules,\nthe Go module cache, a cloned repository) and records each file's component and\nlicence.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, o
}

func cmdIndex(args []string, stdout, stderr io.Writer) error {
	fs, o := indexFlags(stderr)
	roots, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		fs.Usage()
		return errors.New("index: name at least one folder")
	}
	start := time.Now()
	opt := remixware.IndexOptions{Name: o.name, Version: o.version, License: o.license, MaxDF: o.maxDF, Exclude: o.exclude}
	if !o.quiet {
		opt.Progress = func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) }
	}
	ix, err := remixware.BuildIndex(roots, opt)
	if err != nil {
		return err
	}
	if err := ix.Save(o.out); err != nil {
		return err
	}
	if o.quiet {
		return nil
	}
	st, _ := os.Stat(o.out)
	size := int64(0)
	if st != nil {
		size = st.Size()
	}
	fmt.Fprintf(stdout, "Indexed %s %s (%s lines) in %s %s into %s (%s) in %s.\n",
		comma(len(ix.Files)), plural(len(ix.Files), "file", "files"), comma(ix.Lines), comma(len(ix.Components)),
		plural(len(ix.Components), "component", "components"), o.out, humanSize(size), time.Since(start).Round(time.Millisecond))
	if ix.Duplicates > 0 {
		fmt.Fprintf(stdout, "%s %s identical to a file in another version and recorded once.\n", comma(ix.Duplicates),
			plural(ix.Duplicates, "file was", "files were"))
	}
	if n := ix.Skipped["unreadable"]; n > 0 {
		fmt.Fprintf(stdout, "%s %s couldn't be read and %s not in the index.\n", comma(n), plural(n, "file", "files"), plural(n, "is", "are"))
	}
	counts := ix.LicenseCounts()
	var lics []string
	for l := range counts {
		lics = append(lics, l)
	}
	sort.Slice(lics, func(i, j int) bool {
		if counts[lics[i]] != counts[lics[j]] {
			return counts[lics[i]] > counts[lics[j]]
		}
		return lics[i] < lics[j]
	})
	var parts []string
	for _, l := range lics {
		parts = append(parts, fmt.Sprintf("%s %d", l, counts[l]))
	}
	fmt.Fprintf(stdout, "Licences: %s\n", strings.Join(parts, ", "))
	if n := counts[remixware.LicenseNone]; n > 0 {
		fmt.Fprintf(stdout, "%d %s no licence: copies of %s code will be reported as no-license.\n", n,
			plural(n, "component has", "components have"), plural(n, "its", "their"))
	}
	return nil
}

type compareOpts struct {
	indexes, from, exclude    multi
	jsonOut, htmlOut, spdxOut string
	license, policy, failOn   string
	minTokens, minLines       int
	includeDeps, quiet, all   bool
	out                       string
}

func compareFlags(cmd string, stderr io.Writer) (*flag.FlagSet, *compareOpts) {
	o := &compareOpts{}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&o.indexes, "index", "index file made by remixware index (repeatable)")
	fs.Var(&o.from, "from", "results file from the SCANOSS scanner or from remixware scan --json (repeatable)")
	fs.StringVar(&o.license, "license", "", "the project's licence, as an SPDX expression, instead of its licence file")
	fs.StringVar(&o.policy, "policy", "", "policy file (default: .remixware.json in the project, if there is one)")
	fs.IntVar(&o.minTokens, "min-tokens", 0, fmt.Sprintf("smallest match to report, in tokens (default %d)", remixware.DefaultMinTokens))
	fs.IntVar(&o.minLines, "min-lines", 0, fmt.Sprintf("smallest match to report, in lines (default %d)", remixware.DefaultMinLines))
	fs.Var(&o.exclude, "exclude", "leave out project paths matching this pattern (repeatable)")
	fs.BoolVar(&o.includeDeps, "include-deps", false, "also scan vendor/, node_modules/ and similar folders")
	fs.BoolVar(&o.quiet, "quiet", false, "print only the findings and errors")
	switch cmd {
	case "scan":
		fs.StringVar(&o.jsonOut, "json", "", "write the findings as JSON to this file")
		fs.StringVar(&o.htmlOut, "html", "", "write an HTML report to this file")
		fs.StringVar(&o.spdxOut, "spdx", "", "write the findings as an SPDX 2.3 JSON document to this file")
	case "check":
		fs.StringVar(&o.failOn, "fail-on", "", "statuses that fail the check, comma separated (default: the policy's fail_on, or "+strings.Join(remixware.DefaultFailOn, ",")+")")
		fs.StringVar(&o.jsonOut, "json", "", "write the findings as JSON to this file")
		fs.StringVar(&o.htmlOut, "html", "", "write an HTML report to this file")
	case "notice", "notices":
		fs.StringVar(&o.out, "out", "", "notices file to add entries to (default: print to standard output)")
		fs.BoolVar(&o.all, "all", false, "also write entries for copies that are already credited")
	}
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: remixware %s [project] [flags]\n\n", cmd)
		switch cmd {
		case "scan":
			fmt.Fprint(fs.Output(), "Compares the project (default: the current folder) with indexes and results\nfiles, and lists code that matches third-party code with its licence and status.\n")
		case "check":
			fmt.Fprint(fs.Output(), "Like scan, but exits with status 1 when findings break the policy, for CI.\n")
		default:
			fmt.Fprint(fs.Output(), "Writes third-party notice entries (copyright lines and licence texts) for\ncopied code whose licence asks for attribution.\n")
		}
		fmt.Fprint(fs.Output(), "\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs, o
}

// compare runs a scan and imports results files, then evaluates the policy.
// check, when set, looks at the flags before any work starts.
func compare(cmd string, args []string, stderr io.Writer, check func(*compareOpts) error) (*remixware.Results, *remixware.Policy, *compareOpts, error) {
	fs, o := compareFlags(cmd, stderr)
	pos, err := parse(fs, args)
	if err != nil {
		return nil, nil, nil, err
	}
	if check != nil {
		if err := check(o); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(pos) > 1 {
		return nil, nil, nil, fmt.Errorf("%s takes one project folder, got %d: %s", cmd, len(pos), strings.Join(pos, " "))
	}
	project := "."
	if len(pos) == 1 {
		project = pos[0]
	}
	if len(o.indexes) == 0 && len(o.from) == 0 {
		return nil, nil, nil, errors.New("nothing to compare with: pass --index (made by remixware index) or --from (a SCANOSS results file)")
	}
	var pol *remixware.Policy
	if o.policy != "" {
		pol, err = remixware.LoadPolicy(o.policy)
	} else {
		pol, err = remixware.FindPolicy(project)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	progress := func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) }
	if o.quiet {
		progress = nil
	}
	var res *remixware.Results
	if len(o.indexes) > 0 {
		var ixs []*remixware.Index
		for _, p := range o.indexes {
			ix, err := remixware.LoadIndex(p)
			if err != nil {
				return nil, nil, nil, err
			}
			ixs = append(ixs, ix)
		}
		opt := remixware.ScanOptions{Indexes: ixs, MinTokens: pick(o.minTokens, pol.MinTokens),
			MinLines: pick(o.minLines, pol.MinLines), Exclude: append(append([]string{}, o.exclude...), pol.Ignore...),
			IncludeDeps: o.includeDeps, Progress: progress}
		res, err = remixware.Scan(project, opt)
	} else {
		res, err = remixware.NewResults(project)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range o.from {
		found, in, texts, err := remixware.ImportResults(p, res.Project)
		if err != nil {
			return nil, nil, nil, err
		}
		res.Add(found, in, texts)
	}
	remixware.Evaluate(res, pol, o.license)
	return res, pol, o, nil
}

func pick(flagValue, policyValue int) int {
	if flagValue > 0 {
		return flagValue
	}
	return policyValue
}

func writeReports(res *remixware.Results, o *compareOpts, stderr io.Writer) error {
	write := func(path string, fn func(io.Writer, *remixware.Results) error) error {
		if path == "" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		if err := fn(f, res); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if !o.quiet {
			fmt.Fprintf(stderr, "wrote %s\n", path)
		}
		return nil
	}
	if err := write(o.jsonOut, remixware.WriteJSON); err != nil {
		return err
	}
	if err := write(o.htmlOut, remixware.WriteHTML); err != nil {
		return err
	}
	return write(o.spdxOut, remixware.WriteSPDX)
}

func header(w io.Writer, res *remixware.Results) {
	if res.FilesScanned > 0 {
		fmt.Fprintf(w, "Scanned %s %s (%s lines) in %s%s\n", comma(res.FilesScanned), plural(res.FilesScanned, "file", "files"),
			comma(res.LinesScanned), shownPath(res.Project), skippedNote(res))
	} else {
		fmt.Fprintf(w, "Project %s\n", shownPath(res.Project))
	}
	lic := res.ProjectLicense
	if res.ProjectLicenseFrom != "" {
		lic += " (from " + res.ProjectLicenseFrom + ")"
	}
	if res.ProjectNote != "" {
		lic += ": " + res.ProjectNote
	}
	fmt.Fprintf(w, "Project licence: %s\n", lic)
	var ins []string
	for _, in := range res.Inputs {
		switch in.Kind {
		case "index":
			ins = append(ins, fmt.Sprintf("%s (%s %s, %s %s)", in.Path, comma(in.Components), plural(in.Components, "component", "components"),
				comma(in.Files), plural(in.Files, "file", "files")))
		case "scanoss":
			ins = append(ins, fmt.Sprintf("%s (SCANOSS, %s %s)", in.Path, comma(in.Matches), plural(in.Matches, "match", "matches")))
		default:
			ins = append(ins, in.Path)
		}
	}
	fmt.Fprintf(w, "Compared with: %s\n", strings.Join(ins, "; "))
	if len(res.NoticeFiles) > 0 {
		fmt.Fprintf(w, "Notice files: %s\n", strings.Join(res.NoticeFiles, ", "))
	}
}

func printFinding(w io.Writer, f remixware.Finding) {
	label := strings.ToUpper(strings.ReplaceAll(f.Status, "-", " "))
	fmt.Fprintf(w, "\n%-20s %s:%s\n", label, f.File, f.Lines)
	src := f.Source.Component
	if f.Source.Version != "" {
		src += " " + f.Source.Version
	}
	if f.Source.File != "" {
		src += ", " + f.Source.File
		if f.Source.Lines.Start > 0 {
			src += ":" + f.Source.Lines.String()
		}
	}
	kind := ""
	switch f.Match.Kind {
	case "verbatim":
		kind = "copied as is"
	case "edited":
		kind = "copied with edits"
	case "renamed":
		kind = "copied with names changed"
	case "file":
		kind = "whole file (SCANOSS)"
	case "snippet":
		kind = "snippet (SCANOSS)"
	}
	if f.Match.Coverage > 0 {
		kind += fmt.Sprintf(", %.0f%% match", f.Match.Coverage*100)
	}
	fmt.Fprintf(w, "%-20s %s (%s), %s\n", "", src, f.Source.License, kind)
	fmt.Fprintf(w, "%-20s %s\n", "", wrap(f.Reason, 78, strings.Repeat(" ", 21)))
}

func wrap(s string, width int, indent string) string {
	var lines []string
	var cur strings.Builder
	for _, w := range strings.Fields(s) {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n"+indent)
}

func summary(res *remixware.Results) string {
	if len(res.Findings) == 0 {
		return "No copied code found."
	}
	var parts []string
	for i := len(remixware.AllStatuses) - 1; i >= 0; i-- {
		s := remixware.AllStatuses[i]
		if n := res.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(s, "-", " ")))
		}
	}
	noun := "findings"
	if len(res.Findings) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("%d %s: %s.", len(res.Findings), noun, strings.Join(parts, ", "))
}

func cmdScan(args []string, stdout, stderr io.Writer) error {
	res, _, o, err := compare("scan", args, stderr, nil)
	if err != nil {
		return err
	}
	if !o.quiet {
		header(stdout, res)
	}
	sorted := append([]remixware.Finding(nil), res.Findings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return rank(sorted[i].Status) > rank(sorted[j].Status)
	})
	for _, f := range sorted {
		printFinding(stdout, f)
	}
	fmt.Fprintf(stdout, "\n%s\n", summary(res))
	return writeReports(res, o, stderr)
}

func rank(s string) int {
	for i, x := range remixware.AllStatuses {
		if x == s {
			return i
		}
	}
	return -1
}

func cmdCheck(args []string, stdout, stderr io.Writer) (int, error) {
	var flagFailOn []string
	// A typo in --fail-on is found before the scan, not after it.
	checkFlags := func(o *compareOpts) error {
		if o.failOn == "" {
			return nil
		}
		for _, s := range strings.Split(o.failOn, ",") {
			s = strings.TrimSpace(s)
			if rank(s) < 0 {
				return fmt.Errorf("--fail-on: unknown status %q; statuses are %s", s, strings.Join(remixware.AllStatuses, ", "))
			}
			flagFailOn = append(flagFailOn, s)
		}
		return nil
	}
	res, pol, o, err := compare("check", args, stderr, checkFlags)
	if err != nil {
		return exitUsage, err
	}
	failOn := remixware.DefaultFailOn
	switch {
	case len(flagFailOn) > 0:
		failOn = flagFailOn
	case len(pol.FailOn) > 0:
		failOn = pol.FailOn
	}
	// A check that scanned nothing has checked nothing: a wrong path or an
	// --exclude that covers everything must not pass.
	if len(o.indexes) > 0 && res.FilesScanned == 0 {
		return exitUsage, fmt.Errorf("no source files to check in %s%s; check the folder, --exclude and the policy's ignore list",
			shownPath(res.Project), skippedNote(res))
	}
	if !o.quiet {
		header(stdout, res)
	}
	fails := remixware.Failures(res, failOn)
	for _, f := range fails {
		printFinding(stdout, f)
	}
	var warns []remixware.Finding
	for _, f := range res.Findings {
		if f.Status == remixware.StatusReview && !containsString(failOn, f.Status) {
			warns = append(warns, f)
		}
	}
	for _, f := range warns {
		printFinding(stdout, f)
	}
	if err := writeReports(res, o, stderr); err != nil {
		return exitUsage, err
	}
	fmt.Fprintf(stdout, "\n%s\n", summary(res))
	if len(fails) > 0 {
		noun := "findings break"
		if len(fails) == 1 {
			noun = "finding breaks"
		}
		fmt.Fprintf(stdout, "FAIL: %d %s the policy (fail on: %s).\n", len(fails), noun, strings.Join(failOn, ", "))
		if hasStatus(fails, remixware.StatusAttributionMissing) {
			fmt.Fprintln(stdout, "Attribution can be fixed with: remixware notice --out THIRD_PARTY_NOTICES.md (same --index/--from flags)")
		}
		return exitFail, nil
	}
	fmt.Fprintf(stdout, "PASS: no findings break the policy (fail on: %s).\n", strings.Join(failOn, ", "))
	return exitOK, nil
}

// skippedNote says what a scan skipped, as in " (12 files skipped: 8
// declared, 4 generated)", or "".
func skippedNote(res *remixware.Results) string {
	var keys []string
	total := 0
	for k, n := range res.Skipped {
		if n > 0 {
			keys = append(keys, k)
			total += n
		}
	}
	if total == 0 {
		return ""
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		n := res.Skipped[k]
		switch k {
		case "symlink":
			k = plural(n, "symbolic link", "symbolic links") + ", not followed"
		case "too-large":
			k = "over 2 MB"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, k))
	}
	return fmt.Sprintf(" (%d %s skipped: %s)", total, plural(total, "file", "files"), strings.Join(parts, ", "))
}

func hasStatus(fs []remixware.Finding, s string) bool {
	for _, f := range fs {
		if f.Status == s {
			return true
		}
	}
	return false
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func cmdNotice(args []string, stdout, stderr io.Writer) error {
	res, _, o, err := compare("notice", args, stderr, nil)
	if err != nil {
		return err
	}
	existing := ""
	if o.out != "" {
		if data, err := os.ReadFile(o.out); err == nil {
			existing = string(data)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	nr := remixware.Notices(res, existing, o.all)
	for _, s := range nr.Skipped {
		fmt.Fprintf(stderr, "not written: %s\n", s)
	}
	for _, w := range nr.Warnings {
		fmt.Fprintf(stderr, "finish by hand: %s\n", w)
	}
	if len(nr.Present) > 0 && !o.quiet {
		fmt.Fprintf(stderr, "already in %s: %s\n", o.out, strings.Join(nr.Present, ", "))
	}
	if len(nr.Written) == 0 {
		if !o.quiet {
			fmt.Fprintln(stderr, "nothing to add: no copied code is missing its notice")
		}
		return nil
	}
	if o.out == "" {
		fmt.Fprint(stdout, remixware.NoticeHeader+nr.Text)
		return nil
	}
	text := nr.Text
	if strings.TrimSpace(existing) == "" {
		text = remixware.NoticeHeader + text
	} else if !strings.HasSuffix(existing, "\n") {
		text = "\n" + text
	}
	f, err := os.OpenFile(o.out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !o.quiet {
		fmt.Fprintf(stderr, "added %d %s to %s: %s\n", len(nr.Written), plural(len(nr.Written), "entry", "entries"), o.out, strings.Join(nr.Written, ", "))
	}
	return nil
}

// shownPath shows a folder inside the current one by its relative path.
func shownPath(p string) string {
	if wd, err := os.Getwd(); err == nil {
		// The scan names the project with links resolved; so might the
		// working folder need to be.
		dirs := []string{wd}
		if real, err := filepath.EvalSymlinks(wd); err == nil && real != wd {
			dirs = append(dirs, real)
		}
		for _, d := range dirs {
			if rel, err := filepath.Rel(d, p); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
	}
	return p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func cmdLicenses(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LICENCE\tKIND\tCOPYING IT INTO AN MIT PROJECT")
	for _, l := range remixware.KnownLicenses() {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", l.ID, l.Cat, remixware.Verdict(l.ID, "MIT"))
	}
	tw.Flush()
	fmt.Fprintln(w, "\nAny SPDX expression works too, such as \"MIT OR Apache-2.0\". Remixware flags licensing questions. It is not legal advice.")
}

func comma(n int) string {
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
