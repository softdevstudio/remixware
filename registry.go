package remixware

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The dependency gate's online step: remixware deps --online asks the npm
// registry and PyPI about the package versions a change adds. It's the
// only network code besides --scanoss, and it runs only when asked for.
// What a registry says about a published version doesn't change, so the
// answers are cached in a file and never asked for again.

// PyPIURL is PyPI's JSON API.
const PyPIURL = "https://pypi.org/pypi"

// VersionFacts is what a registry says about one package version.
type VersionFacts struct {
	InstallScripts bool      `json:"install_scripts"`
	Publisher      string    `json:"publisher,omitempty"`
	Maintainers    []string  `json:"maintainers,omitempty"`
	Published      time.Time `json:"published,omitempty"`
	Previous       string    `json:"previous,omitempty"` // the release published just before this one

	fromCache bool
}

// Registry looks package versions up online and keeps a cache.
type Registry struct {
	NPMURL    string // default NPMRegistry
	PyPIURL   string // default PyPIURL
	CachePath string // "" keeps no cache file
	Client    *http.Client

	mu       sync.Mutex
	loaded   bool
	cache    map[string]VersionFacts
	dirty    bool
	npmDocs  map[string]*npmDoc
	pypiRels map[string]map[string]time.Time
	Fetched  int // requests made
	Warnings []string
}

// DefaultRegistryCache is where the cache lives unless --cache says
// otherwise: remixware/registry.json in the user's cache folder.
func DefaultRegistryCache() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "remixware", "registry.json")
}

type registryCacheFile struct {
	Format  int                     `json:"format"`
	Entries map[string]VersionFacts `json:"entries"`
}

const registryCacheFormat = 1

func (r *Registry) load() {
	if r.loaded {
		return
	}
	r.loaded = true
	r.cache = map[string]VersionFacts{}
	r.npmDocs = map[string]*npmDoc{}
	r.pypiRels = map[string]map[string]time.Time{}
	if r.CachePath == "" {
		return
	}
	data, err := os.ReadFile(r.CachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("registry cache %s not read: %v", r.CachePath, err))
		}
		return
	}
	var f registryCacheFile
	if err := json.Unmarshal(data, &f); err != nil || f.Format != registryCacheFormat {
		r.Warnings = append(r.Warnings, fmt.Sprintf("registry cache %s isn't one Remixware wrote; starting a new one", r.CachePath))
		return
	}
	for k, v := range f.Entries {
		r.cache[k] = v
	}
}

// Save writes the cache file, if anything was added to it.
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.dirty || r.CachePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.CachePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registryCacheFile{Format: registryCacheFormat, Entries: r.cache}, "", " ")
	if err != nil {
		return err
	}
	tmp := r.CachePath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, r.CachePath); err != nil {
		os.Remove(tmp)
		return err
	}
	r.dirty = false
	return nil
}

// Facts returns what the registry says about eco/name@version, and
// whether it came from the cache.
func (r *Registry) Facts(eco, name, version string) (VersionFacts, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	key := eco + "/" + name + "@" + version
	if f, ok := r.cache[key]; ok {
		return f, true, nil
	}
	var f VersionFacts
	var err error
	switch eco {
	case EcoNPM:
		f, err = r.npmFacts(name, version)
	case EcoPyPI:
		f, err = r.pypiFacts(name, version)
	default:
		return f, false, fmt.Errorf("no registry lookups for %s packages", eco)
	}
	if err != nil {
		return f, false, err
	}
	r.cache[key] = f
	r.dirty = true
	return f, false, nil
}

// maxRegistryAnswer bounds a registry answer: the npm documents of the
// largest packages run to tens of megabytes.
const maxRegistryAnswer = 128 << 20

var errNotFound = errors.New("not found")

func (r *Registry) get(u string, into any) error {
	c := r.Client
	if c == nil {
		c = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "remixware/"+Version)
	r.Fetched++
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("asking %s: %v", shownURL(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", shownURL(u), errNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", shownURL(u), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryAnswer+1))
	if err != nil {
		return fmt.Errorf("reading %s: %v", shownURL(u), err)
	}
	if len(data) > maxRegistryAnswer {
		return fmt.Errorf("%s answered with more than %d MB", shownURL(u), maxRegistryAnswer>>20)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s answered with something that isn't the JSON expected: %v", shownURL(u), err)
	}
	return nil
}

// shownURL drops user name, password and query from a URL for messages.
func shownURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "the registry"
	}
	p.User, p.RawQuery, p.Fragment = nil, "", ""
	return p.String()
}

type npmDoc struct {
	Versions map[string]npmDocVersion `json:"versions"`
	Time     map[string]string        `json:"time"`
}

type npmDocVersion struct {
	Scripts          map[string]any    `json:"scripts"`
	Gypfile          bool              `json:"gypfile"`
	HasInstallScript bool              `json:"hasInstallScript"`
	NpmUser          json.RawMessage   `json:"_npmUser"`
	Maintainers      []json.RawMessage `json:"maintainers"`
}

// personName reads a person from an npm document: {"name": ...} or the
// older "name <email>" string.
func personName(raw json.RawMessage) string {
	var o struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Name != "" {
		return strings.ToLower(o.Name)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if i := strings.IndexAny(s, "<("); i >= 0 {
			s = s[:i]
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
	return ""
}

func (r *Registry) npmFacts(name, version string) (VersionFacts, error) {
	doc := r.npmDocs[name]
	if doc == nil {
		base := r.NPMURL
		if base == "" {
			base = NPMRegistry
		}
		u := strings.TrimSuffix(base, "/") + "/" + strings.Replace(url.PathEscape(name), "%40", "@", 1)
		doc = &npmDoc{}
		if err := r.get(u, doc); err != nil {
			if errors.Is(err, errNotFound) {
				return VersionFacts{}, fmt.Errorf("npm/%s isn't on the npm registry (%v)", name, err)
			}
			return VersionFacts{}, err
		}
		r.npmDocs[name] = doc
	}
	v, ok := doc.Versions[version]
	if !ok {
		return VersionFacts{}, fmt.Errorf("npm/%s %s isn't on the npm registry", name, version)
	}
	f := VersionFacts{Publisher: personName(v.NpmUser)}
	for _, m := range v.Maintainers {
		if n := personName(m); n != "" {
			f.Maintainers = append(f.Maintainers, n)
		}
	}
	sort.Strings(f.Maintainers)
	for _, s := range []string{"preinstall", "install", "postinstall"} {
		if _, ok := v.Scripts[s]; ok {
			f.InstallScripts = true
		}
	}
	f.InstallScripts = f.InstallScripts || v.Gypfile || v.HasInstallScript
	times := map[string]time.Time{}
	for k, s := range doc.Time {
		if _, ok := doc.Versions[k]; !ok {
			continue // "created", "modified" and unpublished versions
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			times[k] = t
		}
	}
	f.Published = times[version]
	f.Previous = previousRelease(times, version)
	return f, nil
}

// previousRelease is the version published last before version.
func previousRelease(times map[string]time.Time, version string) string {
	at, ok := times[version]
	if !ok {
		return ""
	}
	best := ""
	var bestT time.Time
	for v, t := range times {
		if v == version || !t.Before(at) {
			continue
		}
		if best == "" || t.After(bestT) || t.Equal(bestT) && v > best {
			best, bestT = v, t
		}
	}
	return best
}

type pypiFile struct {
	PackageType string `json:"packagetype"`
	UploadTime  string `json:"upload_time_iso_8601"`
}

type pypiDoc struct {
	Info struct {
		Author          string `json:"author"`
		AuthorEmail     string `json:"author_email"`
		Maintainer      string `json:"maintainer"`
		MaintainerEmail string `json:"maintainer_email"`
	} `json:"info"`
	URLs     []pypiFile            `json:"urls"`
	Releases map[string][]pypiFile `json:"releases"`
}

func firstUpload(files []pypiFile) time.Time {
	var first time.Time
	for _, f := range files {
		if t, err := time.Parse(time.RFC3339, f.UploadTime); err == nil && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}

// pypiFacts reads PyPI's JSON for a release. PyPI doesn't say who
// uploaded a release, so the publisher is the release's own maintainer or
// author metadata, which is weaker evidence than npm's _npmUser.
func (r *Registry) pypiFacts(name, version string) (VersionFacts, error) {
	base := strings.TrimSuffix(r.PyPIURL, "/")
	if base == "" {
		base = PyPIURL
	}
	var doc pypiDoc
	if err := r.get(base+"/"+url.PathEscape(name)+"/"+url.PathEscape(version)+"/json", &doc); err != nil {
		if errors.Is(err, errNotFound) {
			return VersionFacts{}, fmt.Errorf("pypi/%s %s isn't on PyPI (%v)", name, version, err)
		}
		return VersionFacts{}, err
	}
	f := VersionFacts{InstallScripts: true}
	for _, u := range doc.URLs {
		if u.PackageType == "bdist_wheel" {
			f.InstallScripts = false
		}
	}
	for _, s := range []string{doc.Info.MaintainerEmail, doc.Info.AuthorEmail, doc.Info.Maintainer, doc.Info.Author} {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || s == "unknown" || s == "none" {
			continue
		}
		if f.Publisher == "" {
			f.Publisher = s
		}
		if !containsString(f.Maintainers, s) {
			f.Maintainers = append(f.Maintainers, s)
		}
	}
	sort.Strings(f.Maintainers)
	f.Published = firstUpload(doc.URLs)
	rels := r.pypiRels[name]
	if rels == nil {
		var all pypiDoc
		if err := r.get(base+"/"+url.PathEscape(name)+"/json", &all); err != nil {
			return VersionFacts{}, err
		}
		rels = map[string]time.Time{}
		for v, files := range all.Releases {
			if t := firstUpload(files); !t.IsZero() {
				rels[v] = t
			}
		}
		r.pypiRels[name] = rels
	}
	if _, ok := rels[version]; !ok && !f.Published.IsZero() {
		rels[version] = f.Published
	}
	f.Previous = previousRelease(rels, version)
	return f, nil
}
