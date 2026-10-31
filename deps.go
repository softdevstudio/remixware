package remixware

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The dependency gate: remixware deps compares the packages a project's
// lockfiles name with those on its main branch, and fails a change that
// brings in a package nobody allowed, a package that runs install scripts,
// or a release by a new publisher.

// Dependency change statuses, a public interface like the licence ones.
const (
	DepNotAllowed       = "not-allowed"       // a new package that isn't on the allow list
	DepInstallScript    = "install-script"    // an added or updated package runs code when installed
	DepPublisherChanged = "publisher-changed" // a recent release by someone who didn't publish or maintain the one before
	DepNotChecked       = "not-checked"       // a changed lockfile Remixware can't read
	DepAllowed          = "allowed"           // a new package on the allow list
	DepUpdated          = "updated"           // a package at a new version
	DepRemoved          = "removed"           // a package no lockfile names any more
)

// DepStatuses lists every dependency status, from fine to failing.
var DepStatuses = []string{DepRemoved, DepUpdated, DepAllowed, DepNotChecked, DepNotAllowed, DepInstallScript, DepPublisherChanged}

// DefaultDepFailOn is what remixware deps fails on without a policy.
var DefaultDepFailOn = []string{DepNotAllowed, DepInstallScript, DepPublisherChanged, DepNotChecked}

// DefaultPublisherDays is how recent a publisher change has to be to count.
const DefaultPublisherDays = 90

func depStatusRank(s string) int {
	for i, x := range DepStatuses {
		if x == s {
			return i
		}
	}
	return -1
}

// DepsPolicy is the "deps" object of the policy file.
type DepsPolicy struct {
	Allow         []DepAllowRule `json:"allow,omitempty"`
	FailOn        []string       `json:"fail_on,omitempty"`
	Lockfiles     []string       `json:"lockfiles,omitempty"`
	PublisherDays int            `json:"publisher_days,omitempty"`
}

// DepAllowRule accepts packages: a new package it names is allowed, and
// with InstallScripts or PublisherChange, so are those findings.
type DepAllowRule struct {
	Package         string `json:"package"` // ecosystem/name, * as a wildcard: npm/@acme/*, golang/golang.org/x/*
	Reason          string `json:"reason"`
	InstallScripts  bool   `json:"install_scripts,omitempty"`
	PublisherChange bool   `json:"publisher_change,omitempty"`
}

func (d *DepsPolicy) validate(path string) error {
	if d.FailOn != nil && len(d.FailOn) == 0 {
		return fmt.Errorf("%s: deps.fail_on is empty; list the statuses that should fail, or leave it out for the defaults (%s)",
			path, strings.Join(DefaultDepFailOn, ", "))
	}
	for _, s := range d.FailOn {
		if depStatusRank(s) < 0 {
			return fmt.Errorf("%s: deps.fail_on has %q; statuses are %s", path, s, strings.Join(DepStatuses, ", "))
		}
	}
	for i, r := range d.Allow {
		if _, _, err := splitDepPattern(r.Package); err != nil {
			return fmt.Errorf("%s: deps allow rule %d: %v", path, i+1, err)
		}
		if strings.TrimSpace(r.Reason) == "" {
			return fmt.Errorf("%s: deps allow rule %d has no reason", path, i+1)
		}
	}
	for _, l := range d.Lockfiles {
		if !safeRel(strings.TrimPrefix(l, "./")) || lockfileKind(l) == "" {
			return fmt.Errorf("%s: deps.lockfiles has %q, which isn't a lockfile path inside the project", path, l)
		}
	}
	if d.PublisherDays < 0 {
		return fmt.Errorf("%s: deps.publisher_days is negative", path)
	}
	return nil
}

func splitDepPattern(p string) (eco, name string, err error) {
	eco, name, ok := strings.Cut(p, "/")
	if !ok || name == "" {
		return "", "", fmt.Errorf("package %q should look like npm/name, pypi/name or golang/module/path", p)
	}
	switch eco {
	case EcoGo, EcoNPM, EcoPyPI:
	default:
		return "", "", fmt.Errorf("package %q: ecosystem %q isn't one of golang, npm, pypi", p, eco)
	}
	return eco, name, nil
}

// match reports whether the rule names the package. Go and npm names
// compare exactly (case included: github.com/Sirupsen and
// github.com/sirupsen are different modules); PyPI names are normalised.
func (r DepAllowRule) match(eco, name string) bool {
	re, pn, err := splitDepPattern(r.Package)
	if err != nil || re != eco {
		return false
	}
	if eco == EcoPyPI {
		pn = strings.ToLower(pyNameSep.ReplaceAllString(pn, "-"))
	}
	if !strings.Contains(pn, "*") {
		return pn == name
	}
	ok, _ := regexp.MatchString("^"+strings.ReplaceAll(regexp.QuoteMeta(pn), `\*`, ".*")+"$", name)
	return ok
}

// UnpinnedVersion stands for a requirement that names no version (pip
// takes whatever is newest), in From, To and an issue's Version.
const UnpinnedVersion = "unpinned"

// DepsResult is what remixware deps found.
type DepsResult struct {
	Project   string         `json:"project"`
	Base      string         `json:"base"`
	Online    bool           `json:"online"`
	Lockfiles []DepLockfile  `json:"lockfiles"`
	Changes   []DepChange    `json:"changes"`
	Checks    []DepCheck     `json:"checks"`
	Counts    map[string]int `json:"counts"`
	// Allow rules in the policy used that the base's policy doesn't have:
	// a change can allow its own packages, so these deserve a look.
	NewAllowRules []DepAllowRule `json:"new_allow_rules,omitempty"`
	// Settings of the policy used that differ from the base's (fail_on,
	// lockfiles, publisher_days, ignore): a change can loosen them too.
	PolicyChanges []string `json:"policy_changes,omitempty"`
}

// DepLockfile is one lockfile and how many packages it names on each side.
type DepLockfile struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"` // go.mod, package-lock.json, requirements.txt, poetry.lock, unsupported
	HeadPackages int    `json:"head_packages"`
	BasePackages int    `json:"base_packages"`
	InHead       bool   `json:"in_head"`
	InBase       bool   `json:"in_base"`
}

// DepChange is one package that was added, updated or removed, or a
// lockfile that changed and couldn't be read.
type DepChange struct {
	Package   string     `json:"package"` // ecosystem/name; empty for an unread lockfile
	Ecosystem string     `json:"ecosystem,omitempty"`
	Name      string     `json:"name,omitempty"`
	Change    string     `json:"change"`         // added, updated, removed, unread
	From      []string   `json:"from,omitempty"` // versions on the base side
	To        []string   `json:"to,omitempty"`   // versions new on the head side
	Direct    bool       `json:"direct"`
	Lockfiles []string   `json:"lockfiles"`
	Status    string     `json:"status"` // the most serious of the issues, or allowed, updated, removed
	Reason    string     `json:"reason"`
	AllowedBy string     `json:"allowed_by,omitempty"` // the reason of the allow rule that accepted it
	Issues    []DepIssue `json:"issues,omitempty"`
}

// DepIssue is one problem with a change.
type DepIssue struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason"`
}

// DepCheck says whether a check ran, and on what.
type DepCheck struct {
	Name          string         `json:"name"`     // allow-list, install-scripts, publisher
	Status        string         `json:"status"`   // ran, partly, not-run, not-applicable, nothing-to-check
	Packages      int            `json:"packages"` // package versions the check applies to
	Offline       int            `json:"offline"`  // checked from the lockfiles
	Online        int            `json:"online"`   // checked with the registry
	Cached        int            `json:"cached"`   // of those online, answered from the cache
	NotApplicable int            `json:"not_applicable"`
	NotRun        map[string]int `json:"not_run,omitempty"` // why the rest weren't checked
}

func (c *DepCheck) skip(why string) {
	if c.NotRun == nil {
		c.NotRun = map[string]int{}
	}
	c.NotRun[why]++
}

func (c *DepCheck) finish() {
	notRun := 0
	for _, n := range c.NotRun {
		notRun += n
	}
	done := c.Offline + c.Online
	switch {
	case c.Packages == 0:
		c.Status = "nothing-to-check"
	case done == 0 && notRun == 0:
		c.Status = "not-applicable"
	case done == 0:
		c.Status = "not-run"
	case notRun == 0:
		c.Status = "ran"
	default:
		c.Status = "partly"
	}
}

// Statuses lists the change's status and its issues' statuses.
func (c DepChange) Statuses() []string {
	out := []string{c.Status}
	for _, i := range c.Issues {
		if i.Status != c.Status {
			out = append(out, i.Status)
		}
	}
	return out
}

// DepFailures returns the changes with a status in failOn.
func DepFailures(res *DepsResult, failOn []string) []DepChange {
	var out []DepChange
	for _, c := range res.Changes {
		for _, s := range c.Statuses() {
			if containsString(failOn, s) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// DepsOptions configures CompareDeps.
type DepsOptions struct {
	Policy   *Policy
	Registry *Registry // nil: no online checks
	// BasePolicy is the base's policy file, when it has one; allow rules
	// the policy used adds to it are listed in NewAllowRules.
	BasePolicy *Policy
	Now        func() time.Time
}

type pkgKey struct{ eco, name string }

type pkgEntry struct {
	versions  map[string]lockPackage
	direct    bool
	lockfiles map[string]bool
}

// readsTree records every file a lockfile reader reads (go.sum,
// package.json, requirements files taken in with -r or -c).
type readsTree struct {
	depTree
	reads map[string]bool
}

func (r readsTree) read(rel string) ([]byte, bool, error) {
	r.reads[rel] = true
	return r.depTree.read(rel)
}

type depSide struct {
	reads   map[string]bool // every file the readers read, includes too
	pkgs    map[pkgKey]*pkgEntry
	count   map[string]int // packages per lockfile
	raw     map[string][]byte
	has     map[string]bool
	sources map[string][]string // where a lockfile tells the installer to look
}

func readSide(t DepsTree, lockfiles []string) (*depSide, error) {
	s := &depSide{reads: map[string]bool{}, pkgs: map[pkgKey]*pkgEntry{}, count: map[string]int{}, raw: map[string][]byte{},
		has: map[string]bool{}, sources: map[string][]string{}}
	rt := readsTree{depTree: t.t, reads: s.reads}
	for _, rel := range lockfiles {
		data, ok, err := t.t.read(rel)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		s.has[rel] = true
		if lockfileKind(rel) == kindUnsupported {
			s.raw[rel] = data
			continue
		}
		pkgs, sources, err := readLockfile(rt, rel, data)
		if err != nil {
			return nil, err
		}
		s.sources[rel] = sources
		s.count[rel] = len(pkgs)
		for _, p := range pkgs {
			k := pkgKey{p.eco, p.name}
			e := s.pkgs[k]
			if e == nil {
				e = &pkgEntry{versions: map[string]lockPackage{}, lockfiles: map[string]bool{}}
				s.pkgs[k] = e
			}
			if old, ok := e.versions[p.version]; ok {
				if p.scripts > old.scripts {
					old.scripts = p.scripts
				}
				old.registry = old.registry && p.registry
				e.versions[p.version] = old
			} else {
				e.versions[p.version] = p
			}
			e.direct = e.direct || p.direct
			e.lockfiles[p.lockfile] = true
		}
	}
	return s, nil
}

// CompareDeps compares the lockfiles of head (the change) with those of
// base (main).
func CompareDeps(head, base DepsTree, opt DepsOptions) (*DepsResult, error) {
	pol := opt.Policy
	if pol == nil {
		pol = &Policy{}
	}
	dp := pol.Deps
	if dp == nil {
		dp = &DepsPolicy{}
	}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	days := dp.PublisherDays
	if days == 0 {
		days = DefaultPublisherDays
	}
	var lockfiles []string
	if len(dp.Lockfiles) > 0 {
		for _, l := range dp.Lockfiles {
			lockfiles = append(lockfiles, strings.TrimPrefix(l, "./"))
		}
	} else {
		seen := map[string]bool{}
		for _, t := range []DepsTree{head, base} {
			found, err := findLockfiles(t.t, pol.Ignore)
			if err != nil {
				return nil, err
			}
			for _, f := range found {
				if !seen[f] {
					seen[f] = true
					lockfiles = append(lockfiles, f)
				}
			}
		}
	}
	sort.Strings(lockfiles)
	hs, err := readSide(head, lockfiles)
	if err != nil {
		return nil, err
	}
	bs, err := readSide(base, lockfiles)
	if err != nil {
		return nil, err
	}
	res := &DepsResult{Project: head.Label(), Base: base.Label(), Online: opt.Registry != nil, Counts: map[string]int{}}
	for _, l := range lockfiles {
		if !hs.has[l] && !bs.has[l] {
			if len(dp.Lockfiles) > 0 {
				return nil, fmt.Errorf("deps.lockfiles names %s, which is in neither %s nor %s", l, head.Label(), base.Label())
			}
			continue
		}
		kind := lockfileKind(l)
		res.Lockfiles = append(res.Lockfiles, DepLockfile{Path: l, Kind: kind, HeadPackages: hs.count[l],
			BasePackages: bs.count[l], InHead: hs.has[l], InBase: bs.has[l]})
		if kind == kindUnsupported && hs.has[l] && (!bs.has[l] || !bytes.Equal(hs.raw[l], bs.raw[l])) {
			res.Changes = append(res.Changes, DepChange{Change: "unread", Lockfiles: []string{l}, Status: DepNotChecked,
				Reason: "This lockfile changed, and Remixware doesn't read " + pathBase(l) +
					" files, so its packages weren't checked. Check them by hand, or take not-checked out of deps.fail_on.",
				Issues: []DepIssue{{Status: DepNotChecked, Reason: "lockfile changed and wasn't read"}}})
		}
		if bs.has[l] && !hs.has[l] {
			if manifest := lockManifest(l); manifest != "" && !hasSiblingLockfile(hs, l) {
				m := path.Join(path.Dir(l), manifest)
				if _, ok, err := head.ReadFile(m); err != nil {
					return nil, err
				} else if ok {
					res.Changes = append(res.Changes, DepChange{Change: "unread", Lockfiles: []string{l}, Status: DepNotChecked,
						Reason: "This change deletes " + l + " but keeps " + m + ", so the next install picks versions, and new packages, that no lockfile names. Keep the lockfile, or take not-checked out of deps.fail_on.",
						Issues: []DepIssue{{Status: DepNotChecked, Reason: "lockfile deleted, manifest kept"}}})
				}
			}
		}
		var newSources []string
		for _, src := range hs.sources[l] {
			if !containsString(bs.sources[l], src) && !containsString(newSources, src) {
				newSources = append(newSources, src)
			}
		}
		if len(newSources) > 0 {
			res.Changes = append(res.Changes, DepChange{Change: "unread", Lockfiles: []string{l}, Status: DepNotChecked,
				Reason: "This change tells pip to look for packages somewhere new (" + strings.Join(newSources, "; ") +
					"), and a package of the same name there can take the place of the one you expect. Check the index by hand, or take not-checked out of deps.fail_on.",
				Issues: []DepIssue{{Status: DepNotChecked, Reason: "package index changed"}}})
		}
	}

	if len(dp.Lockfiles) > 0 {
		// The list replaces the search, but a lockfile outside it that the
		// change adds or edits mustn't pass unread: search anyway, and make
		// each such file not-checked unless a listed file reads it (-r, -c).
		listed := map[string]bool{}
		for _, l := range lockfiles {
			listed[l] = true
		}
		found, err := findLockfiles(head.t, pol.Ignore)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			if listed[f] || hs.reads[f] {
				continue
			}
			hd, _, err := head.t.read(f)
			if err != nil {
				return nil, err
			}
			bd, inBase, err := base.t.read(f)
			if err != nil {
				return nil, err
			}
			what := "is new"
			if inBase {
				if bytes.Equal(hd, bd) {
					continue
				}
				what = "changed"
			}
			res.Changes = append(res.Changes, DepChange{Change: "unread", Lockfiles: []string{f}, Status: DepNotChecked,
				Reason: f + " " + what + ", and it isn't in the policy's deps.lockfiles, so Remixware didn't read it. Add it to deps.lockfiles to check its packages, or add it to the policy's ignore to leave it out on purpose.",
				Issues: []DepIssue{{Status: DepNotChecked, Reason: "lockfile outside deps.lockfiles"}}})
		}
	}

	allow := &DepCheck{Name: "allow-list"}
	scripts := &DepCheck{Name: "install-scripts"}
	pub := &DepCheck{Name: "publisher"}
	ev := &depEval{dp: dp, reg: opt.Registry, now: now(), days: days, scripts: scripts, pub: pub}

	keys := make([]pkgKey, 0, len(hs.pkgs)+len(bs.pkgs))
	for k := range hs.pkgs {
		keys = append(keys, k)
	}
	for k := range bs.pkgs {
		if hs.pkgs[k] == nil {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].eco != keys[j].eco {
			return keys[i].eco < keys[j].eco
		}
		return keys[i].name < keys[j].name
	})
	for _, k := range keys {
		h, b := hs.pkgs[k], bs.pkgs[k]
		c := DepChange{Package: k.eco + "/" + k.name, Ecosystem: k.eco, Name: k.name}
		switch {
		case h == nil:
			c.Change, c.Status, c.From = "removed", DepRemoved, sortedVersions(b.versions, nil)
			c.Direct, c.Lockfiles = b.direct, sortedKeys(b.lockfiles)
			c.Reason = "No lockfile names it any more."
		case b == nil:
			c.Change, c.To = "added", sortedVersions(h.versions, nil)
			c.Direct, c.Lockfiles = h.direct, sortedKeys(h.lockfiles)
			allow.Packages++
			allow.Offline++
			if r, ok := ev.rule(k, func(DepAllowRule) bool { return true }); ok {
				c.Status, c.AllowedBy = DepAllowed, r.Reason
				c.Reason = "New, and on the allow list: " + r.Reason
			} else {
				c.Status = DepNotAllowed
				c.Reason = "New, and not on the allow list. Add a deps allow rule with a reason to the policy file, or drop it."
				c.Issues = append(c.Issues, DepIssue{Status: DepNotAllowed, Reason: "new package not on the allow list"})
			}
		default:
			newVers := sortedVersions(h.versions, b.versions)
			if len(newVers) == 0 {
				continue
			}
			c.Change, c.Status, c.To, c.From = "updated", DepUpdated, newVers, sortedVersions(b.versions, nil)
			c.Direct, c.Lockfiles = h.direct, sortedKeys(h.lockfiles)
			c.Reason = "At a new version."
		}
		if c.Change == "updated" {
			fromRegistry := true
			for _, bp := range b.versions {
				fromRegistry = fromRegistry && bp.registry
			}
			for _, v := range c.To {
				if np := h.versions[v]; fromRegistry && !np.registry {
					c.Issues = append(c.Issues, DepIssue{Status: DepNotChecked, Version: v,
						Reason: "Main takes it from the public registry; this change takes it from somewhere else (" + v +
							"), which Remixware can't check. Check the source by hand, or take not-checked out of deps.fail_on."})
				}
			}
		}
		if c.Change != "removed" {
			for _, v := range c.To {
				if err := ev.check(&c, k, h.versions[v], b); err != nil {
					return nil, err
				}
			}
		}
		for _, i := range c.Issues {
			if depStatusRank(i.Status) > depStatusRank(c.Status) {
				c.Status = i.Status
			}
		}
		res.Changes = append(res.Changes, c)
	}
	// A requirement with no version is read as version "": show it as
	// unpinned, never as an empty string.
	unpinned := func(vs []string) {
		for i, v := range vs {
			if v == "" {
				vs[i] = UnpinnedVersion
			}
		}
	}
	for i := range res.Changes {
		c := &res.Changes[i]
		unpinned(c.From)
		unpinned(c.To)
		for j := range c.Issues {
			// Version-level issues name the version; package-level ones don't.
			if st := c.Issues[j].Status; c.Issues[j].Version == "" && (st == DepInstallScript || st == DepPublisherChanged) {
				c.Issues[j].Version = UnpinnedVersion
			}
		}
	}
	sort.SliceStable(res.Changes, func(i, j int) bool {
		return depStatusRank(res.Changes[i].Status) > depStatusRank(res.Changes[j].Status)
	})
	for _, c := range []*DepCheck{allow, scripts, pub} {
		c.finish()
		res.Checks = append(res.Checks, *c)
	}
	for _, c := range res.Changes {
		res.Counts[c.Status]++
	}
	if opt.Registry != nil {
		if err := opt.Registry.Save(); err != nil {
			return res, fmt.Errorf("saving the registry cache: %v", err)
		}
	}
	if opt.BasePolicy != nil {
		var old []DepAllowRule
		if opt.BasePolicy.Deps != nil {
			old = opt.BasePolicy.Deps.Allow
		}
		for _, r := range dp.Allow {
			found := false
			for _, o := range old {
				if o == r {
					found = true
					break
				}
			}
			if !found {
				res.NewAllowRules = append(res.NewAllowRules, r)
			}
		}
		res.PolicyChanges = policyChanges(pol, opt.BasePolicy)
	}
	if res.Lockfiles == nil {
		res.Lockfiles = []DepLockfile{}
	}
	if res.Changes == nil {
		res.Changes = []DepChange{}
	}
	return res, nil
}

// policyChanges lists the gate's settings that differ between the policy
// used and the base's: a change that narrows fail_on, the lockfiles read or
// the paths ignored passes its own packages as surely as an allow rule.
func policyChanges(pol, base *Policy) []string {
	eff := func(p *Policy) (failOn, lockfiles []string, days int) {
		failOn, days = DefaultDepFailOn, DefaultPublisherDays
		if p.Deps != nil {
			if len(p.Deps.FailOn) > 0 {
				failOn = p.Deps.FailOn
			}
			lockfiles = p.Deps.Lockfiles
			if p.Deps.PublisherDays > 0 {
				days = p.Deps.PublisherDays
			}
		}
		return
	}
	show := func(l []string) string {
		if len(l) == 0 {
			return "(none)"
		}
		return strings.Join(l, ", ")
	}
	same := func(a, b []string) bool {
		a, b = append([]string(nil), a...), append([]string(nil), b...)
		sort.Strings(a)
		sort.Strings(b)
		return strings.Join(a, "\x00") == strings.Join(b, "\x00")
	}
	hf, hl, hd := eff(pol)
	bf, bl, bd := eff(base)
	var out []string
	if !same(hf, bf) {
		out = append(out, "deps.fail_on: "+show(bf)+" -> "+show(hf))
	}
	if !same(hl, bl) {
		out = append(out, "deps.lockfiles: "+show(bl)+" -> "+show(hl))
	}
	if hd != bd {
		out = append(out, fmt.Sprintf("deps.publisher_days: %d -> %d", bd, hd))
	}
	if !same(pol.Ignore, base.Ignore) {
		out = append(out, "ignore: "+show(base.Ignore)+" -> "+show(pol.Ignore))
	}
	return out
}

// lockManifest is the file a lockfile pins the packages of, when an
// installer would go on without the lockfile: package.json for npm,
// pyproject.toml for Poetry.
func lockManifest(l string) string {
	switch lockfileKind(l) {
	case kindNPM:
		return "package.json"
	case kindPoetry:
		return "pyproject.toml"
	}
	return ""
}

// hasSiblingLockfile says whether the side has another lockfile of the
// same kind in l's folder (npm-shrinkwrap.json for package-lock.json).
func hasSiblingLockfile(s *depSide, l string) bool {
	for o := range s.has {
		if o != l && path.Dir(o) == path.Dir(l) && lockfileKind(o) == lockfileKind(l) {
			return true
		}
	}
	return false
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedVersions lists the versions in m that aren't in minus.
func sortedVersions(m, minus map[string]lockPackage) []string {
	var out []string
	for v := range m {
		if _, ok := minus[v]; !ok {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

type depEval struct {
	dp           *DepsPolicy
	reg          *Registry
	now          time.Time
	days         int
	scripts, pub *DepCheck
}

func (e *depEval) rule(k pkgKey, want func(DepAllowRule) bool) (DepAllowRule, bool) {
	for _, r := range e.dp.Allow {
		if r.match(k.eco, k.name) && want(r) {
			return r, true
		}
	}
	return DepAllowRule{}, false
}

const (
	whyNeedsOnline = "needs --online"
	whyNotRegistry = "not from the public registry"
	whyNotPinned   = "version not pinned"
	whyNoPrevious  = "no earlier release to compare with"
)

// check runs the install-script and publisher checks on one added or
// updated version.
func (e *depEval) check(c *DepChange, k pkgKey, p lockPackage, base *pkgEntry) error {
	e.scripts.Packages++
	e.pub.Packages++
	if k.eco == EcoGo {
		// Go builds run no code from modules when they download them, and the
		// module proxy keeps no record of who published a version.
		e.scripts.NotApplicable++
		e.pub.NotApplicable++
		return nil
	}
	lookup := func(chk *DepCheck) bool {
		switch {
		case e.reg == nil:
			chk.skip(whyNeedsOnline)
		case !p.registry:
			chk.skip(whyNotRegistry)
		case !p.pinned:
			chk.skip(whyNotPinned)
		default:
			return true
		}
		return false
	}
	var facts *VersionFacts
	fetch := func() error {
		if facts != nil {
			return nil
		}
		f, cached, err := e.reg.Facts(k.eco, k.name, p.version)
		if err != nil {
			return err
		}
		f.fromCache = cached
		facts = &f
		return nil
	}
	// Install scripts.
	runs := false
	switch {
	case p.scripts != scriptsUnknown:
		e.scripts.Offline++
		runs = p.scripts == scriptsYes
	case lookup(e.scripts):
		if err := fetch(); err != nil {
			return err
		}
		e.scripts.Online++
		if facts.fromCache {
			e.scripts.Cached++
		}
		runs = facts.InstallScripts
	}
	if runs {
		if _, ok := e.rule(k, func(r DepAllowRule) bool { return r.InstallScripts }); !ok {
			why := "Runs code when it's installed (an install script, or a build from source)."
			if k.eco == EcoNPM {
				why = "Runs an install script (preinstall, install or postinstall, or a node-gyp build) when npm installs it."
			} else if k.eco == EcoPyPI {
				why = "Has no wheel, so pip builds it from source, which runs its build code."
			}
			c.Issues = append(c.Issues, DepIssue{Status: DepInstallScript, Version: p.version,
				Reason: why + " Accept it with a deps allow rule that has install_scripts: true and a reason."})
		}
	}
	// Publisher.
	if !lookup(e.pub) {
		return nil
	}
	if err := fetch(); err != nil {
		return err
	}
	e.pub.Online++
	if facts.fromCache {
		e.pub.Cached++
	}
	var compare []string
	if base != nil {
		for v, bp := range base.versions {
			if bp.pinned && bp.registry {
				compare = append(compare, v)
			}
		}
		sort.Strings(compare)
	}
	if len(compare) == 0 && facts.Previous != "" {
		// A new package, or one main took from elsewhere: compare with the
		// release before this one.
		compare = []string{facts.Previous}
	}
	if len(compare) == 0 {
		e.pub.Online--
		if facts.fromCache {
			e.pub.Cached--
		}
		e.pub.skip(whyNoPrevious)
		return nil
	}
	if facts.Publisher == "" {
		e.pub.Online--
		if facts.fromCache {
			e.pub.Cached--
		}
		e.pub.skip("no publisher recorded")
		return nil
	}
	recent := facts.Published.IsZero() || e.now.Sub(facts.Published) < time.Duration(e.days)*24*time.Hour
	if !recent {
		return nil
	}
	var known []string
	for _, v := range compare {
		cf, _, err := e.reg.Facts(k.eco, k.name, v)
		if err != nil {
			return err
		}
		if cf.Publisher == "" && len(cf.Maintainers) == 0 {
			return nil // nothing recorded to compare with
		}
		if cf.Publisher == facts.Publisher || containsString(cf.Maintainers, facts.Publisher) {
			return nil
		}
		known = append(known, v)
	}
	if _, ok := e.rule(k, func(r DepAllowRule) bool { return r.PublisherChange }); ok {
		return nil
	}
	when := "recently"
	if !facts.Published.IsZero() {
		when = facts.Published.UTC().Format("2006-01-02")
	}
	c.Issues = append(c.Issues, DepIssue{Status: DepPublisherChanged, Version: p.version,
		Reason: fmt.Sprintf("Published %s by %s, who neither published nor maintained %s %s. Check the release before taking it, then accept it with a deps allow rule that has publisher_change: true.",
			when, facts.Publisher, k.name, strings.Join(known, ", "))})
	return nil
}
