package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/softdevstudio/remixware"
)

type depsOpts struct {
	base, baseRef, policy, failOn, jsonOut string
	cache, npmURL, pypiURL                 string
	online, noCache, quiet, basePolicy     bool
}

func depsFlags(stderr io.Writer) (*flag.FlagSet, *depsOpts) {
	o := &depsOpts{}
	fs := flag.NewFlagSet("deps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.base, "base", "", "folder holding the version to compare with, such as a checkout of main")
	fs.StringVar(&o.baseRef, "base-ref", "", "git revision to compare with, such as origin/main (read from the local repository)")
	fs.StringVar(&o.policy, "policy", "", "policy file (default: .remixware.json in the project, if there is one)")
	fs.BoolVar(&o.basePolicy, "base-policy", false, "use the base's .remixware.json, so a change can't allow its own packages")
	fs.StringVar(&o.failOn, "fail-on", "", "statuses that fail, comma separated (default: the policy's deps.fail_on, or "+strings.Join(remixware.DefaultDepFailOn, ",")+")")
	fs.StringVar(&o.jsonOut, "json", "", "write the changes as JSON to this file")
	fs.BoolVar(&o.online, "online", false, "also ask the npm registry and PyPI about added and updated packages (install scripts, publisher changes)")
	fs.StringVar(&o.cache, "cache", "", "registry answers cache for --online (default: remixware/registry.json in the user cache folder)")
	fs.BoolVar(&o.noCache, "no-cache", false, "with --online, keep no cache file")
	fs.StringVar(&o.npmURL, "npm-registry", "", "npm registry URL for --online (default "+remixware.NPMRegistry+")")
	fs.StringVar(&o.pypiURL, "pypi-url", "", "PyPI JSON API URL for --online (default "+remixware.PyPIURL+")")
	fs.BoolVar(&o.quiet, "quiet", false, "print only the problems and the result")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: remixware deps [project] (--base FOLDER | --base-ref REV) [flags]

Compares the packages the project's lockfiles name (go.mod and go.sum,
package-lock.json, requirements*.txt, poetry.lock) with those of the base, and
exits with status 1 when the change adds a package that isn't on the policy's
deps allow list, a package that runs install scripts, or a release by a new
publisher. Without --online it uses the lockfiles only.

Flags:
`)
		fs.PrintDefaults()
	}
	return fs, o
}

func cmdDeps(args []string, stdout, stderr io.Writer) (int, error) {
	fs, o := depsFlags(stderr)
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage, err
	}
	if len(pos) > 1 {
		return exitUsage, errors.New("deps: name one project folder")
	}
	project := "."
	if len(pos) == 1 {
		project = pos[0]
	}
	if (o.base == "") == (o.baseRef == "") {
		fs.Usage()
		return exitUsage, errors.New("deps: give the version to compare with, either --base FOLDER or --base-ref REV")
	}
	var failOn []string
	if o.failOn != "" {
		for _, s := range strings.Split(o.failOn, ",") {
			s = strings.TrimSpace(s)
			if !containsString(remixware.DepStatuses, s) {
				return exitUsage, fmt.Errorf("--fail-on: unknown status %q; statuses are %s", s, strings.Join(remixware.DepStatuses, ", "))
			}
			failOn = append(failOn, s)
		}
	}
	if !o.online && (o.cache != "" || o.noCache || o.npmURL != "" || o.pypiURL != "") {
		return exitUsage, errors.New("deps: --cache, --no-cache, --npm-registry and --pypi-url go with --online")
	}
	head, err := remixware.DepsDir(project)
	if err != nil {
		return exitUsage, err
	}
	var base remixware.DepsTree
	if o.base != "" {
		base, err = remixware.DepsDir(o.base)
	} else {
		base, err = remixware.DepsGit(project, o.baseRef)
	}
	if err != nil {
		return exitUsage, err
	}
	if o.basePolicy && o.policy != "" {
		return exitUsage, errors.New("deps: --base-policy and --policy don't go together")
	}
	// The base's policy, to use with --base-policy or to show which allow
	// rules the change adds.
	basePol := &remixware.Policy{}
	if data, ok, err := base.ReadFile(remixware.PolicyFile); err != nil {
		return exitUsage, err
	} else if ok {
		if basePol, err = remixware.ParsePolicy(data, remixware.PolicyFile+" at "+base.Label()); err != nil {
			return exitUsage, err
		}
	}
	var pol *remixware.Policy
	switch {
	case o.basePolicy:
		pol = basePol
	case o.policy != "":
		pol, err = remixware.LoadPolicy(o.policy)
	default:
		pol, err = remixware.FindPolicy(project)
	}
	if err != nil {
		return exitUsage, err
	}
	if len(failOn) == 0 {
		failOn = remixware.DefaultDepFailOn
		if pol.Deps != nil && len(pol.Deps.FailOn) > 0 {
			failOn = pol.Deps.FailOn
		}
	}
	var reg *remixware.Registry
	if o.online {
		reg = &remixware.Registry{NPMURL: o.npmURL, PyPIURL: o.pypiURL, CachePath: o.cache}
		if o.cache == "" && !o.noCache {
			reg.CachePath = remixware.DefaultRegistryCache()
		}
		if o.noCache {
			reg.CachePath = ""
		}
	}
	res, err := remixware.CompareDeps(head, base, remixware.DepsOptions{Policy: pol, Registry: reg, BasePolicy: basePol})
	if reg != nil {
		for _, w := range reg.Warnings {
			fmt.Fprintln(stderr, "remixware: warning:", w)
		}
	}
	if err != nil {
		if res == nil {
			return exitUsage, err
		}
		// Only saving the cache failed: the checks themselves ran.
		fmt.Fprintln(stderr, "remixware: warning:", err)
	}
	if o.base != "" {
		res.Base = shownPath(res.Base)
	}
	res.Project = shownPath(res.Project)
	if !o.quiet {
		depsHeader(stdout, res)
	}
	fails := remixware.DepFailures(res, failOn)
	shown := fails
	if !o.quiet {
		shown = res.Changes
	}
	for _, c := range shown {
		printDepChange(stdout, c)
	}
	fmt.Fprintln(stdout)
	if len(res.NewAllowRules) > 0 {
		fmt.Fprintln(stdout, "Allow rules this change adds to the policy (review them: a change can allow its own packages):")
		for _, r := range res.NewAllowRules {
			extra := ""
			if r.InstallScripts {
				extra += ", install scripts too"
			}
			if r.PublisherChange {
				extra += ", publisher changes too"
			}
			fmt.Fprintf(stdout, "  %s%s: %s\n", r.Package, extra, r.Reason)
		}
		fmt.Fprintln(stdout)
	}
	printChecks(stdout, res)
	if o.jsonOut != "" {
		if err := writeDepsJSON(o.jsonOut, res); err != nil {
			return exitUsage, err
		}
	}
	fmt.Fprintf(stdout, "\n%s\n", depsSummary(res))
	if len(fails) > 0 {
		noun := "changes break"
		if len(fails) == 1 {
			noun = "change breaks"
		}
		fmt.Fprintf(stdout, "FAIL: %d %s the policy (fail on: %s).\n", len(fails), noun, strings.Join(failOn, ", "))
		return exitFail, nil
	}
	fmt.Fprintf(stdout, "PASS: no changes break the policy (fail on: %s).\n", strings.Join(failOn, ", "))
	return exitOK, nil
}

func writeDepsJSON(path string, res *remixware.DepsResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func depsHeader(w io.Writer, res *remixware.DepsResult) {
	fmt.Fprintf(w, "Compared the dependencies of %s with %s\n", res.Project, res.Base)
	if len(res.Lockfiles) == 0 {
		fmt.Fprintln(w, "Lockfiles: none found")
		return
	}
	var parts []string
	for _, l := range res.Lockfiles {
		switch {
		case l.Kind == "unsupported":
			parts = append(parts, l.Path+" (not read)")
		case !l.InHead:
			parts = append(parts, fmt.Sprintf("%s (removed; %s before)", l.Path, plural2(l.BasePackages, "package")))
		case !l.InBase:
			parts = append(parts, fmt.Sprintf("%s (new; %s)", l.Path, plural2(l.HeadPackages, "package")))
		default:
			parts = append(parts, fmt.Sprintf("%s (%s)", l.Path, plural2(l.HeadPackages, "package")))
		}
	}
	fmt.Fprintf(w, "Lockfiles: %s\n", strings.Join(parts, ", "))
}

func plural2(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%s %ss", comma(n), noun)
}

func printDepChange(w io.Writer, c remixware.DepChange) {
	label := strings.ToUpper(strings.ReplaceAll(c.Status, "-", " "))
	what := c.Package
	switch c.Change {
	case "added":
		what += " " + strings.Join(c.To, ", ") + ", added"
	case "updated":
		what += " " + strings.Join(c.From, ", ") + " -> " + strings.Join(c.To, ", ")
	case "removed":
		what += " " + strings.Join(c.From, ", ") + ", removed"
	case "unread":
		what = strings.Join(c.Lockfiles, ", ") + " changed"
	}
	if c.Change == "added" || c.Change == "updated" {
		if c.Direct {
			what += " (direct)"
		} else {
			what += " (transitive)"
		}
	}
	fmt.Fprintf(w, "\n%-20s %s\n", label, what)
	if c.Change != "unread" {
		fmt.Fprintf(w, "%-20s in %s\n", "", strings.Join(c.Lockfiles, ", "))
	}
	if len(c.Issues) == 0 || c.Status == remixware.DepNotChecked {
		fmt.Fprintf(w, "%-20s %s\n", "", wrap(c.Reason, 78, strings.Repeat(" ", 21)))
	}
	for _, i := range c.Issues {
		if i.Status == remixware.DepNotChecked {
			continue
		}
		reason := i.Reason
		if i.Status == remixware.DepNotAllowed {
			reason = c.Reason
		}
		prefix := ""
		if i.Status != c.Status || len(c.To) > 1 {
			prefix = strings.ToUpper(strings.ReplaceAll(i.Status, "-", " "))
			if i.Version != "" {
				prefix += " (" + i.Version + ")"
			}
			prefix += ": "
		}
		fmt.Fprintf(w, "%-20s %s\n", "", wrap(prefix+reason, 78, strings.Repeat(" ", 21)))
	}
}

var checkLabels = map[string]string{"allow-list": "allow list", "install-scripts": "install scripts", "publisher": "publisher changes"}

// printChecks says what each check did, so a pass can be read for what it
// covers. Nothing here claims an online check that didn't happen.
func printChecks(w io.Writer, res *remixware.DepsResult) {
	fmt.Fprintln(w, "Checks:")
	for _, c := range res.Checks {
		fmt.Fprintf(w, "  %-18s %s\n", checkLabels[c.Name], wrap(checkText(c, res.Online), 66, strings.Repeat(" ", 21)))
	}
}

func checkText(c remixware.DepCheck, online bool) string {
	noun := "added package"
	if c.Name != "allow-list" {
		noun = "added or updated version"
	}
	if c.Packages == 0 {
		return "nothing to check (no " + noun + "s)"
	}
	var parts []string
	if c.Offline > 0 {
		parts = append(parts, fmt.Sprintf("%d checked from the lockfiles", c.Offline))
	}
	if c.Online > 0 {
		s := fmt.Sprintf("%d checked online", c.Online)
		if c.Cached > 0 {
			s += fmt.Sprintf(" (%d of them from the cache)", c.Cached)
		}
		parts = append(parts, s)
	}
	reasons := make([]string, 0, len(c.NotRun))
	for r := range c.NotRun {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%d not checked (%s)", c.NotRun[r], r))
	}
	if c.NotApplicable > 0 {
		why := "Go modules run no install scripts"
		if c.Name == "publisher" {
			why = "the Go module proxy records no publisher"
		}
		parts = append(parts, fmt.Sprintf("%d not applicable (%s)", c.NotApplicable, why))
	}
	if c.Packages != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%s, %d %s: %s", strings.ReplaceAll(c.Status, "-", " "), c.Packages, noun, strings.Join(parts, ", "))
}

func depsSummary(res *remixware.DepsResult) string {
	if len(res.Changes) == 0 {
		return "No dependency changes."
	}
	var parts []string
	for i := len(remixware.DepStatuses) - 1; i >= 0; i-- {
		s := remixware.DepStatuses[i]
		if n := res.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	noun := "changes"
	if len(res.Changes) == 1 {
		noun = "change"
	}
	return fmt.Sprintf("%d %s: %s.", len(res.Changes), noun, strings.Join(parts, ", "))
}
