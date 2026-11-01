package remixware

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parseYAMLDocs reads the YAML that pnpm and yarn (berry) write into maps
// (map[string]any), slices ([]any) and strings. It reads block mappings and
// sequences, plain, single- and double-quoted scalars, one-line flow
// mappings and sequences, comments and several documents (--- and ...).
// Anything else (anchors and aliases, tags, block scalars, flow collections
// spread over lines, tabs for indentation, a key given twice) is an error:
// a lockfile reader that guessed would pass what it can't see.
func parseYAMLDocs(data []byte) ([]map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("not UTF-8")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	var docs []map[string]any
	var lines []yamlLine
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		p := &yamlParser{lines: lines}
		v, err := p.block(0, 0)
		if err != nil {
			return err
		}
		if p.i < len(p.lines) {
			return p.errf(p.lines[p.i], "unexpected indentation")
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("a document that isn't a mapping")
		}
		docs = append(docs, m)
		lines = nil
		return nil
	}
	for n, raw := range strings.Split(text, "\n") {
		if raw == "---" || strings.HasPrefix(raw, "--- ") || raw == "..." {
			// A document can start on the --- line itself; this reader
			// doesn't read that, so it stops rather than drop the content.
			if rest := strings.TrimSpace(stripComment(strings.TrimPrefix(raw, "---"))); strings.HasPrefix(raw, "--- ") && rest != "" {
				return nil, fmt.Errorf("line %d: content on the --- line, which Remixware doesn't read", n+1)
			}
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		body := strings.TrimRight(raw, " ")
		trimmed := strings.TrimLeft(body, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "\t") || strings.HasPrefix(raw, "\t") {
			return nil, fmt.Errorf("line %d: a tab used for indentation", n+1)
		}
		if strings.HasPrefix(trimmed, "%") && len(body) == len(trimmed) {
			return nil, fmt.Errorf("line %d: a YAML directive", n+1)
		}
		lines = append(lines, yamlLine{n: n + 1, indent: len(body) - len(trimmed), text: trimmed})
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return docs, nil
}

type yamlLine struct {
	n, indent int
	text      string
}

type yamlParser struct {
	lines []yamlLine
	i     int
}

func (p *yamlParser) errf(l yamlLine, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", l.n, fmt.Sprintf(format, args...))
}

const yamlMaxDepth = 64

// block reads the mapping or sequence whose first line is at p.i, at that
// line's indentation (which must be at least min).
func (p *yamlParser) block(min, depth int) (any, error) {
	if depth > yamlMaxDepth {
		return nil, fmt.Errorf("nested too deep")
	}
	if p.i >= len(p.lines) {
		return nil, fmt.Errorf("a value is missing at the end")
	}
	first := p.lines[p.i]
	if first.indent < min {
		return nil, p.errf(first, "a value is missing")
	}
	indent := first.indent
	if isSeqItem(first.text) {
		var out []any
		for p.i < len(p.lines) && p.lines[p.i].indent == indent && isSeqItem(p.lines[p.i].text) {
			l := p.lines[p.i]
			rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
			if rest == "" {
				p.i++
				v, err := p.block(indent+1, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
				continue
			}
			if _, _, isKey, err := splitYAMLKey(rest); err != nil {
				return nil, p.errf(l, "%v", err)
			} else if isKey {
				// "- key: value" starts a mapping indented past the dash.
				p.lines[p.i] = yamlLine{n: l.n, indent: l.indent + len(l.text) - len(rest), text: rest}
				v, err := p.block(indent+1, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
				continue
			}
			v, err := scalarOrFlow(rest)
			if err != nil {
				return nil, p.errf(l, "%v", err)
			}
			out = append(out, v)
			p.i++
		}
		if p.i < len(p.lines) && p.lines[p.i].indent > indent {
			return nil, p.errf(p.lines[p.i], "unexpected indentation")
		}
		return out, nil
	}
	m := map[string]any{}
	for p.i < len(p.lines) && p.lines[p.i].indent == indent {
		l := p.lines[p.i]
		if isSeqItem(l.text) {
			return nil, p.errf(l, "a sequence item inside a mapping")
		}
		key, rest, isKey, err := splitYAMLKey(l.text)
		if err != nil {
			return nil, p.errf(l, "%v", err)
		}
		if !isKey {
			return nil, p.errf(l, "expected key: value, found %q", short(l.text))
		}
		if _, dup := m[key]; dup {
			return nil, p.errf(l, "%q given twice", key)
		}
		p.i++
		if rest != "" {
			v, err := scalarOrFlow(rest)
			if err != nil {
				return nil, p.errf(l, "%v", err)
			}
			m[key] = v
			continue
		}
		// The value is a nested block, a sequence at the same indentation,
		// or empty.
		switch {
		case p.i < len(p.lines) && p.lines[p.i].indent > indent:
			v, err := p.block(indent+1, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		case p.i < len(p.lines) && p.lines[p.i].indent == indent && isSeqItem(p.lines[p.i].text):
			v, err := p.block(indent, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		default:
			m[key] = ""
		}
	}
	if p.i < len(p.lines) && p.lines[p.i].indent > indent {
		return nil, p.errf(p.lines[p.i], "unexpected indentation")
	}
	return m, nil
}

func isSeqItem(s string) bool { return s == "-" || strings.HasPrefix(s, "- ") }

func short(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// splitYAMLKey splits "key: value" (the key plain or quoted). isKey is
// false for a line that is a plain scalar with no ": ".
func splitYAMLKey(s string) (key, rest string, isKey bool, err error) {
	if s[0] == '\'' || s[0] == '"' {
		k, n, err := quoted(s)
		if err != nil {
			return "", "", false, err
		}
		after := s[n:]
		if after == ":" {
			return k, "", true, nil
		}
		if strings.HasPrefix(after, ": ") {
			return k, strings.TrimSpace(stripComment(after[2:])), true, nil
		}
		if strings.TrimSpace(stripComment(after)) == "" {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("text after a quoted key: %q", short(after))
	}
	if s[0] == '?' && (len(s) == 1 || s[1] == ' ') {
		return "", "", false, fmt.Errorf("complex keys (?) aren't read")
	}
	if s[0] == '{' || s[0] == '[' {
		return "", "", false, nil
	}
	if strings.HasSuffix(s, ":") && !strings.Contains(s[:len(s)-1], ": ") {
		// "key :" is the key "key", as YAML reads a plain scalar.
		k := strings.TrimRight(s[:len(s)-1], " \t")
		if k == "" {
			return "", "", false, fmt.Errorf("an empty key")
		}
		return k, "", true, nil
	}
	if i := strings.Index(s, ": "); i > 0 {
		k := strings.TrimRight(s[:i], " \t")
		if k == "" {
			return "", "", false, fmt.Errorf("an empty key")
		}
		if strings.Contains(k, " #") {
			return "", "", false, nil
		}
		return k, strings.TrimSpace(stripComment(s[i+2:])), true, nil
	}
	return "", "", false, nil
}

// stripComment drops " # ..." from a plain value (quoted text keeps its #).
func stripComment(s string) string {
	inQ := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ != 0:
			if c == '\\' && inQ == '"' {
				i++
			} else if c == inQ {
				inQ = 0
			}
		case c == '\'' || c == '"':
			if i == 0 || s[i-1] == ' ' || s[i-1] == '[' || s[i-1] == '{' || s[i-1] == ',' || s[i-1] == ':' {
				inQ = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return strings.TrimRight(s[:i], " ")
		}
	}
	return s
}

// scalarOrFlow reads a one-line value.
func scalarOrFlow(s string) (any, error) {
	switch s[0] {
	case '&', '*', '!':
		return nil, fmt.Errorf("anchors, aliases and tags aren't read")
	case '|', '>':
		return nil, fmt.Errorf("block scalars aren't read")
	case '{', '[':
		f := &flowParser{s: s}
		v, err := f.value(0)
		if err != nil {
			return nil, err
		}
		f.space()
		if f.i != len(f.s) {
			return nil, fmt.Errorf("text after a flow collection: %q", short(f.s[f.i:]))
		}
		return v, nil
	case '\'', '"':
		v, n, err := quoted(s)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s[n:]) != "" {
			return nil, fmt.Errorf("text after a quoted value: %q", short(s[n:]))
		}
		return v, nil
	}
	return s, nil
}

// quoted reads a quoted scalar at the start of s and returns it and the
// bytes it used.
func quoted(s string) (string, int, error) {
	q := s[0]
	if q == '\'' {
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				return b.String(), i + 1, nil
			}
			b.WriteByte(s[i])
		}
		return "", 0, fmt.Errorf("a quoted value that isn't closed (flow scalars over lines aren't read)")
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			v, err := strconv.Unquote(yamlToGoQuote(s[:i+1]))
			if err != nil {
				return "", 0, fmt.Errorf("a double-quoted value Remixware can't read: %q", short(s[:i+1]))
			}
			return v, i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("a quoted value that isn't closed (flow scalars over lines aren't read)")
}

// yamlToGoQuote maps YAML's double-quote escapes that Go's differ on.
func yamlToGoQuote(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(`\/`, `/`, `\ `, ` `, `\e`, `\x1b`, `\0`, `\x00`, `\N`, "\u0085", `\_`, " ", `\L`, " ", `\P`, " ")
	return r.Replace(s)
}

type flowParser struct {
	s string
	i int
}

func (f *flowParser) space() {
	for f.i < len(f.s) && f.s[f.i] == ' ' {
		f.i++
	}
}

func (f *flowParser) value(depth int) (any, error) {
	if depth > yamlMaxDepth {
		return nil, fmt.Errorf("nested too deep")
	}
	f.space()
	if f.i >= len(f.s) {
		return nil, fmt.Errorf("a flow collection is cut off (flow collections over lines aren't read)")
	}
	switch c := f.s[f.i]; c {
	case '{':
		f.i++
		m := map[string]any{}
		for {
			f.space()
			if f.i < len(f.s) && f.s[f.i] == '}' {
				f.i++
				return m, nil
			}
			k, err := f.scalar(true)
			if err != nil {
				return nil, err
			}
			f.space()
			if f.i >= len(f.s) || f.s[f.i] != ':' {
				return nil, fmt.Errorf("expected : after %q in a flow mapping", k)
			}
			f.i++
			v, err := f.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if _, dup := m[k]; dup {
				return nil, fmt.Errorf("%q given twice", k)
			}
			m[k] = v
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ',' {
				f.i++
				continue
			}
			if f.i < len(f.s) && f.s[f.i] == '}' {
				continue
			}
			return nil, fmt.Errorf("expected , or } in a flow mapping")
		}
	case '[':
		f.i++
		out := []any{}
		for {
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ']' {
				f.i++
				return out, nil
			}
			v, err := f.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			f.space()
			if f.i < len(f.s) && f.s[f.i] == ',' {
				f.i++
				continue
			}
			if f.i < len(f.s) && f.s[f.i] == ']' {
				continue
			}
			return nil, fmt.Errorf("expected , or ] in a flow sequence")
		}
	case '&', '*', '!':
		return nil, fmt.Errorf("anchors, aliases and tags aren't read")
	}
	return f.scalar(false)
}

// scalar reads a quoted or plain scalar inside a flow collection. A key's
// plain scalar ends at ':' followed by a space or the end; a value's at ',',
// ']' or '}'.
func (f *flowParser) scalar(key bool) (string, error) {
	if f.i < len(f.s) && (f.s[f.i] == '\'' || f.s[f.i] == '"') {
		v, n, err := quoted(f.s[f.i:])
		if err != nil {
			return "", err
		}
		f.i += n
		return v, nil
	}
	start := f.i
	for f.i < len(f.s) {
		c := f.s[f.i]
		if c == ',' || c == ']' || c == '}' || c == '[' || c == '{' {
			break
		}
		if c == ':' && (f.i+1 == len(f.s) || f.s[f.i+1] == ' ' || f.s[f.i+1] == ',' || f.s[f.i+1] == '}' || f.s[f.i+1] == ']') {
			if key {
				break
			}
			return "", fmt.Errorf("a ':' inside a flow value")
		}
		if c == '#' && f.i > start && f.s[f.i-1] == ' ' {
			return "", fmt.Errorf("a comment inside a flow collection")
		}
		f.i++
	}
	v := strings.TrimSpace(f.s[start:f.i])
	if v == "" {
		return "", fmt.Errorf("an empty item in a flow collection")
	}
	return v, nil
}
