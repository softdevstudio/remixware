package remixware

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tomlDoc reads a TOML document into maps (map[string]any), slices ([]any)
// and scalars: strings as string, everything else (numbers, booleans,
// dates) as tomlRaw holding the text. It's written for lockfiles (uv.lock),
// and it is strict: a key given twice, a table defined twice, a value it
// can't read or text after a value are errors, never skipped. Nesting is
// limited, and so is the input size (maxLockfile).
type tomlRaw string

// tomlBare matches the unquoted values TOML allows: booleans, integers
// (decimal, hex, octal, binary, with underscores), floats (inf and nan
// too) and dates and times. Anything else is an error, so a stray line
// in an array can't pass as a value.
var tomlBare = regexp.MustCompile(`^(?:true|false|` +
	`[+-]?(?:0|[1-9](?:_?[0-9])*)|0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*|0o[0-7](?:_?[0-7])*|0b[01](?:_?[01])*|` +
	`[+-]?(?:0|[1-9](?:_?[0-9])*)(?:\.[0-9](?:_?[0-9])*)?(?:[eE][+-]?[0-9](?:_?[0-9])*)?|[+-]?(?:inf|nan)|` +
	`[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[Tt ][0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?(?:[Zz]|[+-][0-9]{2}:[0-9]{2})?)?|` +
	`[0-9]{2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]+)?)?)$`)

const tomlMaxDepth = 32

type tomlReader struct {
	s    string
	i    int
	line int
	// defined tables, by dotted path with array positions, so [a] twice is
	// an error but [[a]] twice is two tables.
	defined map[string]bool
}

func parseTOML(data []byte) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("not UTF-8")
	}
	r := &tomlReader{s: strings.ReplaceAll(string(data), "\r\n", "\n"), line: 1, defined: map[string]bool{}}
	root := map[string]any{}
	cur := root
	for {
		r.skipBlank()
		if r.i >= len(r.s) {
			return root, nil
		}
		if r.s[r.i] == '[' {
			t, err := r.header(root)
			if err != nil {
				return nil, r.errf("%v", err)
			}
			cur = t
			continue
		}
		if err := r.keyValue(cur, 0); err != nil {
			return nil, err
		}
		if err := r.endOfLine(); err != nil {
			return nil, err
		}
	}
}

func (r *tomlReader) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", r.line, fmt.Sprintf(format, args...))
}

// skipBlank skips spaces, tabs, comments and newlines.
func (r *tomlReader) skipBlank() {
	for r.i < len(r.s) {
		switch c := r.s[r.i]; {
		case c == ' ' || c == '\t':
			r.i++
		case c == '\n':
			r.line++
			r.i++
		case c == '#':
			for r.i < len(r.s) && r.s[r.i] != '\n' {
				r.i++
			}
		default:
			return
		}
	}
}

func (r *tomlReader) skipSpace() {
	for r.i < len(r.s) && (r.s[r.i] == ' ' || r.s[r.i] == '\t') {
		r.i++
	}
}

// endOfLine accepts spaces and a comment, then a newline or the end.
func (r *tomlReader) endOfLine() error {
	r.skipSpace()
	if r.i < len(r.s) && r.s[r.i] == '#' {
		for r.i < len(r.s) && r.s[r.i] != '\n' {
			r.i++
		}
	}
	if r.i < len(r.s) && r.s[r.i] != '\n' {
		return r.errf("unexpected %q after a value", r.rest(20))
	}
	return nil
}

func (r *tomlReader) rest(n int) string {
	end := r.i + n
	if end > len(r.s) {
		end = len(r.s)
	}
	if nl := strings.IndexByte(r.s[r.i:end], '\n'); nl >= 0 {
		end = r.i + nl
	}
	return r.s[r.i:end]
}

// key reads a dotted key: bare parts or quoted strings.
func (r *tomlReader) key() ([]string, error) {
	var parts []string
	for {
		r.skipSpace()
		if r.i >= len(r.s) {
			return nil, r.errf("a key is cut off")
		}
		var part string
		switch c := r.s[r.i]; {
		case c == '"' || c == '\'':
			v, err := r.str()
			if err != nil {
				return nil, err
			}
			part = v
		default:
			start := r.i
			for r.i < len(r.s) {
				c := r.s[r.i]
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
					r.i++
					continue
				}
				break
			}
			if r.i == start {
				return nil, r.errf("expected a key, found %q", r.rest(20))
			}
			part = r.s[start:r.i]
		}
		parts = append(parts, part)
		r.skipSpace()
		if r.i < len(r.s) && r.s[r.i] == '.' {
			r.i++
			continue
		}
		return parts, nil
	}
}

// header reads [a.b] or [[a.b]] and returns the table it names.
func (r *tomlReader) header(root map[string]any) (map[string]any, error) {
	array := strings.HasPrefix(r.s[r.i:], "[[")
	if array {
		r.i += 2
	} else {
		r.i++
	}
	parts, err := r.key()
	if err != nil {
		return nil, err
	}
	closing := "]"
	if array {
		closing = "]]"
	}
	if !strings.HasPrefix(r.s[r.i:], closing) {
		return nil, fmt.Errorf("a table header that isn't closed")
	}
	r.i += len(closing)
	if err := r.endOfLine(); err != nil {
		return nil, err
	}
	// Walk to the parent, creating tables; through arrays of tables, the
	// last element.
	t := root
	path := ""
	for _, p := range parts[:len(parts)-1] {
		next, err := r.descend(t, p)
		if err != nil {
			return nil, err
		}
		t = next
		path += "." + p + r.arrayPos(t)
	}
	last := parts[len(parts)-1]
	if array {
		v, ok := t[last]
		if !ok {
			v = []any{}
		}
		arr, isArr := v.([]any)
		if !isArr || r.defined["inline:"+path+"."+last] {
			return nil, fmt.Errorf("[[%s]] after %s was given a value", strings.Join(parts, "."), last)
		}
		nt := map[string]any{}
		arr = append(arr, nt)
		t[last] = arr
		r.defined[path+"."+last+fmt.Sprintf("#%d", len(arr))] = true
		return nt, nil
	}
	full := path + "." + last
	if r.defined[full] {
		return nil, fmt.Errorf("[%s] defined twice", strings.Join(parts, "."))
	}
	r.defined[full] = true
	v, ok := t[last]
	if !ok {
		nt := map[string]any{}
		t[last] = nt
		return nt, nil
	}
	nt, isTable := v.(map[string]any)
	if !isTable || r.defined["inline:"+full] {
		return nil, fmt.Errorf("[%s] names a key that already has a value", strings.Join(parts, "."))
	}
	return nt, nil
}

// arrayPos tells tables in different elements of an array of tables apart.
func (r *tomlReader) arrayPos(t map[string]any) string { return fmt.Sprintf("@%p", t) }

func (r *tomlReader) descend(t map[string]any, p string) (map[string]any, error) {
	v, ok := t[p]
	if !ok {
		nt := map[string]any{}
		t[p] = nt
		return nt, nil
	}
	switch x := v.(type) {
	case map[string]any:
		return x, nil
	case []any:
		if len(x) == 0 {
			return nil, fmt.Errorf("%s is an empty array", p)
		}
		last, ok := x[len(x)-1].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is an array of values, not of tables", p)
		}
		return last, nil
	}
	return nil, fmt.Errorf("%s already has a value", p)
}

// keyValue reads key = value into t.
func (r *tomlReader) keyValue(t map[string]any, depth int) error {
	parts, err := r.key()
	if err != nil {
		return err
	}
	if r.i >= len(r.s) || r.s[r.i] != '=' {
		return r.errf("expected = after %s", strings.Join(parts, "."))
	}
	r.i++
	r.skipSpace()
	v, err := r.value(depth)
	if err != nil {
		return err
	}
	for _, p := range parts[:len(parts)-1] {
		next, ok := t[p]
		if !ok {
			nt := map[string]any{}
			t[p] = nt
			t = nt
			continue
		}
		nt, isTable := next.(map[string]any)
		if !isTable {
			return r.errf("%s already has a value", p)
		}
		t = nt
	}
	last := parts[len(parts)-1]
	if _, dup := t[last]; dup {
		return r.errf("%s given twice", strings.Join(parts, "."))
	}
	t[last] = v
	return nil
}

func (r *tomlReader) value(depth int) (any, error) {
	if depth > tomlMaxDepth {
		return nil, r.errf("values nested too deep")
	}
	if r.i >= len(r.s) {
		return nil, r.errf("a value is missing")
	}
	switch c := r.s[r.i]; c {
	case '"', '\'':
		return r.str()
	case '[':
		r.i++
		var out []any
		for {
			r.skipBlank()
			if r.i >= len(r.s) {
				return nil, r.errf("an array is never closed")
			}
			if r.s[r.i] == ']' {
				r.i++
				if out == nil {
					out = []any{}
				}
				return out, nil
			}
			v, err := r.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			r.skipBlank()
			if r.i < len(r.s) && r.s[r.i] == ',' {
				r.i++
				continue
			}
			r.skipBlank()
			if r.i >= len(r.s) || r.s[r.i] != ']' {
				return nil, r.errf("expected , or ] in an array, found %q", r.rest(20))
			}
		}
	case '{':
		r.i++
		t := map[string]any{}
		r.skipSpace()
		if r.i < len(r.s) && r.s[r.i] == '}' {
			r.i++
			return t, nil
		}
		for {
			if err := r.keyValue(t, depth+1); err != nil {
				return nil, err
			}
			r.skipSpace()
			if r.i >= len(r.s) {
				return nil, r.errf("an inline table is never closed")
			}
			switch r.s[r.i] {
			case ',':
				r.i++
				r.skipSpace()
				continue
			case '}':
				r.i++
				return t, nil
			}
			return nil, r.errf("expected , or } in an inline table, found %q", r.rest(20))
		}
	}
	// Numbers, booleans, dates: kept as text, up to a separator.
	start := r.i
	for r.i < len(r.s) {
		c := r.s[r.i]
		if c == ',' || c == ']' || c == '}' || c == '\n' || c == '#' {
			break
		}
		r.i++
	}
	raw := strings.TrimSpace(r.s[start:r.i])
	if !tomlBare.MatchString(raw) {
		return nil, r.errf("can't read the value %q", raw)
	}
	return tomlRaw(raw), nil
}

// str reads a basic, literal or multi-line string.
func (r *tomlReader) str() (string, error) {
	q := r.s[r.i]
	if strings.HasPrefix(r.s[r.i:], strings.Repeat(string(q), 3)) {
		r.i += 3
		if r.i < len(r.s) && r.s[r.i] == '\n' {
			r.i++
			r.line++
		}
		end := strings.Index(r.s[r.i:], strings.Repeat(string(q), 3))
		if end < 0 {
			return "", r.errf("a multi-line string is never closed")
		}
		body := r.s[r.i : r.i+end]
		r.line += strings.Count(body, "\n")
		r.i += end + 3
		// Up to two more quotes belong to the string.
		for k := 0; k < 2 && r.i < len(r.s) && r.s[r.i] == q; k++ {
			body += string(q)
			r.i++
		}
		if q == '\'' {
			return body, nil
		}
		return unescapeTOML(body, true)
	}
	r.i++
	start := r.i
	for r.i < len(r.s) {
		c := r.s[r.i]
		if c == '\n' {
			return "", r.errf("a string is cut by a line break")
		}
		if c == q {
			body := r.s[start:r.i]
			r.i++
			if q == '\'' {
				return body, nil
			}
			v, err := unescapeTOML(body, false)
			if err != nil {
				return "", r.errf("%v", err)
			}
			return v, nil
		}
		if c == '\\' && q == '"' {
			r.i++
		}
		r.i++
	}
	return "", r.errf("a string is never closed")
}

func unescapeTOML(s string, multiline bool) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("a string ends in a backslash")
		}
		switch e := s[i]; e {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '"', '\\':
			b.WriteByte(e)
		case 'u', 'U':
			n := 4
			if e == 'U' {
				n = 8
			}
			if i+1+n > len(s) {
				return "", fmt.Errorf("a cut-off \\%c escape", e)
			}
			v, err := strconv.ParseUint(s[i+1:i+1+n], 16, 32)
			if err != nil || !utf8.ValidRune(rune(v)) {
				return "", fmt.Errorf("a bad \\%c escape", e)
			}
			b.WriteRune(rune(v))
			i += n
		case '\n', ' ', '\t':
			if !multiline {
				return "", fmt.Errorf("a line-ending backslash outside a multi-line string")
			}
			// Line-ending backslash: drop it and the whitespace that follows.
			for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
				i++
			}
			i--
		default:
			return "", fmt.Errorf("an unknown escape \\%c", e)
		}
	}
	return b.String(), nil
}
