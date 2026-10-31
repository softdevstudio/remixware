package remixware

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 0.3.2: with deps.lockfiles set, a lockfile outside the list that appears
// or changes was skipped without a word, so a new requirements file with
// an extra index passed (found in the deps case studies, the monorepo
// recipe). It's not-checked now, and ignore is the way to skip a path.
func TestDepsUnlistedLockfileNotChecked(t *testing.T) {
	common := map[string]string{
		"api/go.mod":                  "module example.com/api\n\ngo 1.22\n\nrequire github.com/acme/a v1.0.0\n",
		"api/go.sum":                  "github.com/acme/a v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		"web/package-lock.json":       npmLock("a@1.0.0"),
		"ml/requirements.txt":         "-r requirements/common.txt\nflask==3.0.0\n",
		"docs/requirements.txt":       "sphinx==7.0.0\n", // outside the list, unchanged
		"old/requirements.txt":        "gone==1.0\n",     // outside the list, deleted
		"node_modules/x/yarn.lock":    "x@1:\n  version 1\n",
		"testdata/requirements-x.txt": "fixture==1.0\n",
		"tools/package-lock.json":     npmLock("b@1.0.0"),
	}
	base := map[string]string{"ml/requirements/common.txt": "numpy==1.26.0\n"}
	head := map[string]string{
		// Read through the listed ml/requirements.txt: not outside the list.
		"ml/requirements/common.txt": "numpy==1.26.4\n",
		// New, outside the list: a requirements file with an extra index.
		"ml/requirements-neuron.txt": "--extra-index-url https://pip.repos.neuron.amazonaws.com\nneuronx-cc\n",
		// Changed, outside the list.
		"tools/package-lock.json": npmLock("b@1.0.0", "evil@6.0.0"),
		// New, outside the list, a kind the gate doesn't read.
		"services/yarn.lock": "y@1:\n  version 1\n",
		// Changed, but ignored on purpose, and under node_modules.
		"testdata/requirements-x.txt": "fixture==2.0\n",
		"node_modules/x/yarn.lock":    "x@2:\n  version 2\n",
	}
	for k, v := range common {
		if _, ok := base[k]; !ok {
			base[k] = v
		}
		if _, ok := head[k]; !ok && k != "old/requirements.txt" {
			head[k] = v
		}
	}
	pol := &Policy{Ignore: []string{"testdata/**"}, Deps: &DepsPolicy{
		Lockfiles: []string{"api/go.mod", "web/package-lock.json", "ml/requirements.txt"}}}
	res := compare(t, head, base, pol, nil)
	var flagged []string
	for _, c := range res.Changes {
		if c.Change != "unread" {
			continue
		}
		if c.Status != DepNotChecked || !strings.Contains(c.Reason, "deps.lockfiles") || !strings.Contains(c.Reason, "ignore") {
			t.Errorf("%v: status %s, reason %q", c.Lockfiles, c.Status, c.Reason)
		}
		flagged = append(flagged, c.Lockfiles...)
	}
	sort.Strings(flagged)
	want := "ml/requirements-neuron.txt,services/yarn.lock,tools/package-lock.json"
	if strings.Join(flagged, ",") != want {
		t.Errorf("flagged %v, want %s", flagged, want)
	}
	if c := change(res, "pypi/numpy"); c == nil || c.Change != "updated" {
		t.Errorf("the included requirements file wasn't read: %+v", c)
	}
	if got := len(DepFailures(res, DefaultDepFailOn)); got != 3 {
		t.Errorf("%d failures, want 3: %+v", got, res.Changes)
	}
	// Ignoring the paths is the explicit way to skip them, and then it passes.
	pol.Ignore = append(pol.Ignore, "ml/requirements-neuron.txt", "services/**", "tools/**")
	res = compare(t, head, base, pol, nil)
	if f := DepFailures(res, DefaultDepFailOn); len(f) != 0 {
		t.Errorf("ignored paths still fail: %+v", f)
	}
	// Without deps.lockfiles the search finds them all, as before.
	pol = &Policy{Ignore: []string{"testdata/**"}}
	res = compare(t, head, base, pol, nil)
	if c := change(res, "pypi/neuronx-cc"); c == nil || c.Status != DepNotAllowed {
		t.Errorf("search without deps.lockfiles: %+v", c)
	}
}

// The same on a git revision, the way a pull request runs it.
func TestDepsUnlistedLockfileGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL="+noGitConfig(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0.0\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "base")
	os.MkdirAll(filepath.Join(dir, "extra"), 0o755)
	os.WriteFile(filepath.Join(dir, "extra", "requirements.txt"), []byte("--extra-index-url https://pkgs.example.net/simple\nevil==1.0\n"), 0o644)
	head, err := DepsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := DepsGit(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	pol := &Policy{Deps: &DepsPolicy{Lockfiles: []string{"requirements.txt"}}}
	res, err := CompareDeps(head, base, DepsOptions{Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 || res.Changes[0].Status != DepNotChecked || res.Changes[0].Lockfiles[0] != "extra/requirements.txt" {
		t.Errorf("new unlisted requirements file in git: %+v", res.Changes)
	}
}

// 0.3.2: a requirement with no version showed an empty version
// ("pypi/neuronx-cc , added", "to": [""]).
func TestDepsUnpinnedVersionShown(t *testing.T) {
	base := map[string]string{"requirements.txt": "numpy\nflask==3.0.0\ntorch==2.1.0\n"}
	head := map[string]string{"requirements.txt": "numpy==1.26.0\nneuronx-cc\nflask==3.0.0\ntorch\n"}
	res := compare(t, head, base, nil, nil)
	for pkg, want := range map[string][2]string{
		"pypi/neuronx-cc": {"", UnpinnedVersion},
		"pypi/numpy":      {UnpinnedVersion, "1.26.0"},
		"pypi/torch":      {"2.1.0", UnpinnedVersion},
	} {
		c := change(res, pkg)
		if c == nil {
			t.Errorf("%s: no change", pkg)
			continue
		}
		if strings.Join(c.From, ",") != want[0] || strings.Join(c.To, ",") != want[1] {
			t.Errorf("%s: from %q to %q, want %q to %q", pkg, c.From, c.To, want[0], want[1])
		}
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `""`) {
		t.Errorf("empty string in the JSON: %s", data)
	}
	// Unpinned to unpinned is no change, and an unpinned range keeps its spec.
	if res := compare(t, base, base, nil, nil); len(res.Changes) != 0 {
		t.Errorf("unchanged unpinned: %+v", res.Changes)
	}
	res = compare(t, map[string]string{"requirements.txt": "numpy>=1.26\n"}, map[string]string{"requirements.txt": "numpy\n"}, nil, nil)
	if c := change(res, "pypi/numpy"); c == nil || strings.Join(c.To, ",") != ">=1.26" || strings.Join(c.From, ",") != UnpinnedVersion {
		t.Errorf("range: %+v", c)
	}
}
