package remixware

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// globMatch matches a slash-separated relative path against a pattern where
// * matches within one folder, ** matches across folders, and ? matches one
// character. A pattern without a slash matches the file or folder name at
// any depth, the way .gitignore does.
func globMatch(pattern, rel string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pattern)), "./")
	rel = filepath.ToSlash(rel)
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if !strings.Contains(strings.TrimSuffix(pattern, "/**"), "/") {
		pattern = "**/" + pattern
	}
	return globRegexp(pattern).MatchString(rel)
}

var globCache sync.Map

func globRegexp(pattern string) *regexp.Regexp {
	if re, ok := globCache.Load(pattern); ok {
		return re.(*regexp.Regexp)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// A pattern naming a folder also matches everything inside it.
	b.WriteString("(?:/.*)?$")
	re := regexp.MustCompile(b.String())
	globCache.Store(pattern, re)
	return re
}

func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if globMatch(p, rel) {
			return true
		}
	}
	return false
}

func relSlash(base, path string) string {
	r, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(r)
}

// within reports whether path is inside dir (or is dir).
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// gitRemote returns the URL of the "origin" remote in a git checkout.
func gitRemote(dir string) string {
	text := readSmall(filepath.Join(dir, ".git", "config"), 64<<10)
	in := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			in = line == `[remote "origin"]`
			continue
		}
		if in && strings.HasPrefix(line, "url") {
			if _, v, ok := strings.Cut(line, "="); ok {
				return cleanRepoURL(strings.TrimSpace(v))
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}
