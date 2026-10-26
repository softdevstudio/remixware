package remixware

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// token is one lexical unit of source code once comments and layout are gone.
//
// norm is the hash of the normalised form: keywords, operators and numbers
// hash as written, every string literal hashes the same, and names get 0
// because they are numbered later, per k-gram, by order of first appearance.
// exact is the hash of the token as written, used to tell a verbatim copy
// from a renamed one.
type token struct {
	norm    uint64
	exact   uint64
	line    int32
	keyword bool
}

func (t token) isName() bool { return t.norm == 0 }

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func fnvHash(prefix byte, b []byte) uint64 {
	h := uint64(fnvOffset)
	h ^= uint64(prefix)
	h *= fnvPrime
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime
	}
	if h == 0 {
		h = 1
	}
	return h
}

var (
	hashString = fnvHash('s', nil)
	hashRegex  = fnvHash('r', nil)
)

// regexAfterKeyword lists the JavaScript keywords after which a slash starts
// a regular expression rather than a division.
var regexAfterKeyword = set(`return typeof instanceof in of new delete void throw case do else yield await`)

var ops3 = map[string]bool{">>=": true, "<<=": true, "...": true, "===": true, "!==": true, "**=": true, "//=": true,
	"->*": true, "<=>": true, "&&=": true, "||=": true, "??=": true, ">>>": true, "::=": true, "&^=": true}

var ops2 = map[string]bool{"==": true, "!=": true, "<=": true, ">=": true, "&&": true, "||": true, "++": true,
	"--": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true, "&=": true, "|=": true, "^=": true,
	"<<": true, ">>": true, "->": true, "=>": true, "::": true, ":=": true, "<-": true, "**": true, "//": true,
	"??": true, "?.": true, "..": true, "&^": true, "|>": true}

// isTriple reports whether a triple quote starts at i.
func isTriple(src []byte, i int) bool {
	c := byte(0)
	if i < len(src) {
		c = src[i]
	}
	return i+2 < len(src) && (c == '"' || c == '\'') && src[i+1] == c && src[i+2] == c
}

// scanTriple returns the end of a triple-quoted string that starts at i,
// counting the lines it spans.
func scanTriple(src []byte, i int, line *int32) int {
	c, n := src[i], len(src)
	i += 3
	for i < n {
		if src[i] == '\\' && i+1 < n {
			if src[i+1] == '\n' {
				*line++
			}
			i += 2
			continue
		}
		if src[i] == '\n' {
			*line++
		}
		if src[i] == c && i+2 < n && src[i+1] == c && src[i+2] == c {
			return i + 3
		}
		i++
	}
	return n
}

// leadingComments returns the comments at the top of a source file, before
// its first line of code, markers included: where licence notices, SPDX
// tags and copyright lines live. A byte order mark, a shebang line and
// PHP's opening tag are passed over, and so are C preprocessor lines; a
// Python module docstring counts as a comment. Licence words in the code
// itself, in a string or a variable name, never count.
func leadingComments(src []byte, sx *syntax, limit int) string {
	if sx == nil {
		return ""
	}
	if len(src) > limit {
		src = src[:limit]
	}
	if bytes.Contains(src, []byte("\r\n")) {
		src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	}
	n, i := len(src), 0
	if n >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3
	}
	if n-i >= 2 && src[i] == '#' && src[i+1] == '!' {
		for i < n && src[i] != '\n' {
			i++
		}
	}
	var b strings.Builder
	keep := func(from, to int) {
		b.Write(src[from:to])
		b.WriteByte('\n')
	}
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
			continue
		case hasPrefixAt(src, i, "<?php"):
			i += len("<?php")
			continue
		}
		if end, ok := blockComment(src, i, sx); ok {
			keep(i, end)
			i = end
			continue
		}
		if lineComment(src, i, sx) || (c == '#' && sx == synC) {
			j := i
			for j < n && src[j] != '\n' {
				j++
			}
			if c == '#' && sx == synC {
				i = j // #include, #ifndef: not a comment, but no code yet either
				continue
			}
			keep(i, j)
			i = j
			continue
		}
		if sx.tripleQuotes {
			j := i
			if sx.stringPrefix && isASCIILetter(c) {
				j = pyStringStart(src, i)
			}
			if isTriple(src, j) {
				var line int32
				end := scanTriple(src, j, &line)
				keep(i, end)
				i = end
				continue
			}
		}
		break
	}
	return b.String()
}

// scanRegex returns the end of a JavaScript regular expression literal that
// starts with the slash at i, flags included. A literal can't span lines, so
// a slash with no closing slash on its line is a division after all.
func scanRegex(src []byte, i int) (int, bool) {
	inClass := false
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\n':
			return 0, false
		case '\\':
			j++
			if j < len(src) && src[j] == '\n' {
				return 0, false
			}
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if inClass {
				continue
			}
			for j++; j < len(src) && isASCIILetter(src[j]); j++ {
			}
			return j, true
		}
	}
	return 0, false
}

// valueEnd reports whether an operator ends a value, so that a slash after
// it is a division: x) / 2, a[i] / 2, n++ / 2.
func valueEnd(op []byte) bool {
	switch string(op) {
	case ")", "]", "}", "++", "--":
		return true
	}
	return false
}

// tokenize splits source code into tokens, dropping comments and layout.
// It is deliberately forgiving: it never fails, and code it can't make
// sense of still turns into a stable stream of tokens, so two copies of the
// same text always tokenize the same way.
func tokenize(src []byte, sx *syntax) []token {
	// Windows line endings must not change what a string literal hashes to.
	if bytes.Contains(src, []byte("\r\n")) {
		src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	}
	toks := make([]token, 0, len(src)/4)
	n := len(src)
	line := int32(1)
	i := 0
	if n >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3
	}
	if n-i >= 2 && src[i] == '#' && src[i+1] == '!' {
		for i < n && src[i] != '\n' {
			i++
		}
	}
	// afterValue is true when the last token ends a value. It tells a
	// JavaScript regular expression from a division.
	afterValue := false
	emit := func(norm, exact uint64) {
		toks = append(toks, token{norm: norm, exact: exact, line: line})
	}
	for i < n {
		c := src[i]

		// Block comments come first: Ruby's start with a newline.
		if end, ok := blockComment(src, i, sx); ok {
			for j := i; j < end; j++ {
				if src[j] == '\n' {
					line++
				}
			}
			i = end
			continue
		}
		if c == '\n' {
			line++
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v' {
			i++
			continue
		}
		if lineComment(src, i, sx) {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		// Strings.
		if sx.tripleQuotes && isTriple(src, i) {
			start, startLine := i, line
			i = scanTriple(src, i, &line)
			toks = append(toks, token{norm: hashString, exact: fnvHash('s', src[start:i]), line: startLine})
			afterValue = true
			continue
		}
		if sx.stringPrefix && isASCIILetter(c) {
			if j := pyStringStart(src, i); j > i {
				start, startLine := i, line
				if sx.tripleQuotes && isTriple(src, j) {
					i = scanTriple(src, j, &line) // r"""...""", f'''...'''
				} else {
					i = scanQuoted(src, j, src[j], true, &line)
				}
				toks = append(toks, token{norm: hashString, exact: fnvHash('s', src[start:i]), line: startLine})
				afterValue = true
				continue
			}
		}
		if c == '"' || (c == '\'' && sx.singleQuote && !(sx.rustLifetimes && isLifetime(src, i))) {
			start, startLine := i, line
			i = scanQuoted(src, i, c, true, &line)
			toks = append(toks, token{norm: hashString, exact: fnvHash('s', src[start:i]), line: startLine})
			afterValue = true
			continue
		}
		if c == '`' && sx.backtick {
			start, startLine := i, line
			i = scanQuoted(src, i, c, sx != synGo, &line)
			toks = append(toks, token{norm: hashString, exact: fnvHash('s', src[start:i]), line: startLine})
			afterValue = true
			continue
		}

		// Names and keywords.
		if r, size := decodeRune(src, i); isNameStart(r, sx) {
			start := i
			i += size
			for i < n {
				r, size = decodeRune(src, i)
				if !isNamePart(r, sx) {
					break
				}
				i += size
			}
			word := src[start:i]
			afterValue = true
			if len(word) == 1 && word[0] == '$' {
				emit(fnvHash('p', word), fnvHash('p', word))
				continue
			}
			key := word
			if sx.foldCase {
				key = asciiLower(word)
			}
			if sx.keywords[string(key)] {
				h := fnvHash('k', key)
				emit(h, h)
				toks[len(toks)-1].keyword = true
				afterValue = !sx.regexLiterals || !regexAfterKeyword[string(key)]
			} else {
				emit(0, fnvHash('i', key))
			}
			continue
		}

		// Numbers.
		if isDigit(c) || (c == '.' && i+1 < n && isDigit(src[i+1])) {
			start := i
			hex := c == '0' && i+1 < n && (src[i+1] == 'x' || src[i+1] == 'X')
			i++
			for i < n {
				d := src[i]
				if isDigit(d) || isASCIILetter(d) || d == '_' || d == '.' {
					i++
					continue
				}
				if (d == '+' || d == '-') && i > start {
					p := src[i-1]
					if p == 'p' || p == 'P' || (!hex && (p == 'e' || p == 'E')) {
						i++
						continue
					}
				}
				break
			}
			h := fnvHash('n', asciiLower(src[start:i]))
			emit(h, h)
			afterValue = true
			continue
		}

		// Operators and punctuation.
		if c >= 0x80 {
			r, size := decodeRune(src, i)
			if unicode.IsSpace(r) {
				i += size
				continue
			}
			h := fnvHash('p', src[i:i+size])
			emit(h, h)
			afterValue = false
			i += size
			continue
		}
		if c == '/' && sx.regexLiterals && !afterValue {
			if end, ok := scanRegex(src, i); ok {
				emit(hashRegex, fnvHash('r', src[i:end]))
				afterValue = true
				i = end
				continue
			}
		}
		width := 1
		if i+3 <= n && ops3[string(src[i:i+3])] {
			width = 3
		} else if i+2 <= n && ops2[string(src[i:i+2])] {
			width = 2
		}
		h := fnvHash('p', src[i:i+width])
		emit(h, h)
		afterValue = valueEnd(src[i : i+width])
		i += width
	}
	return toks
}

// blockComment reports whether a block comment starts at i, and where it ends.
func blockComment(src []byte, i int, sx *syntax) (int, bool) {
	for _, bc := range sx.blockComments {
		open, close := bc[0], bc[1]
		j := i + len(open)
		if !hasPrefixAt(src, i, open) {
			// Ruby's =begin must start a line; at the top of the file there
			// is no newline before it.
			if !(open == "\n=begin" && i == 0 && hasPrefixAt(src, 0, "=begin")) {
				continue
			}
			j = len("=begin")
		}
		depth := 1
		for j < len(src) {
			if sx.nestedBlocks && hasPrefixAt(src, j, open) {
				depth++
				j += len(open)
				continue
			}
			if hasPrefixAt(src, j, close) {
				depth--
				j += len(close)
				if depth == 0 {
					return j, true
				}
				continue
			}
			j++
		}
		return len(src), true
	}
	return 0, false
}

func lineComment(src []byte, i int, sx *syntax) bool {
	for _, lc := range sx.lineComments {
		if !hasPrefixAt(src, i, lc) {
			continue
		}
		if lc == "#" && (sx == synShell || sx == synPerl) && i > 0 {
			// In shell and Perl, # starts a comment only at the start of a
			// word: $# and ${#x} are not comments.
			p := src[i-1]
			if p != ' ' && p != '\t' && p != '\n' && p != ';' && p != '(' && p != '\r' {
				return false
			}
		}
		return true
	}
	return false
}

// scanQuoted returns the index just past the string that starts with the
// quote at i. Strings in single or double quotes end at a newline when they
// aren't closed, so one stray quote can't swallow the rest of the file.
func scanQuoted(src []byte, i int, quote byte, escapes bool, line *int32) int {
	n := len(src)
	i++
	for i < n {
		c := src[i]
		if escapes && c == '\\' && i+1 < n {
			if src[i+1] == '\n' {
				*line++
			}
			i += 2
			continue
		}
		if c == quote {
			return i + 1
		}
		if c == '\n' {
			if quote != '`' {
				return i
			}
			*line++
		}
		i++
	}
	return n
}

// pyStringStart returns the index of the opening quote when a Python string
// prefix such as r, b, f, rb or fr starts at i, or i when it doesn't.
func pyStringStart(src []byte, i int) int {
	j := i
	for j < len(src) && j-i < 2 && isPyPrefix(src[j]) {
		j++
	}
	if j > i && j < len(src) && (src[j] == '"' || src[j] == '\'') {
		if i > 0 && isNamePart(rune(src[i-1]), synPython) {
			return i
		}
		return j
	}
	return i
}

func isPyPrefix(c byte) bool {
	switch c {
	case 'r', 'R', 'b', 'B', 'u', 'U', 'f', 'F':
		return true
	}
	return false
}

// isLifetime tells a Rust lifetime ('a, 'static) from a char literal ('a').
func isLifetime(src []byte, i int) bool {
	j := i + 1
	if j >= len(src) {
		return false
	}
	r, size := decodeRune(src, j)
	if !(r == '_' || unicode.IsLetter(r)) {
		return false
	}
	j += size
	for j < len(src) {
		r, size = decodeRune(src, j)
		if !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			break
		}
		j += size
	}
	return j >= len(src) || src[j] != '\''
}

func decodeRune(src []byte, i int) (rune, int) {
	c := src[i]
	if c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRune(src[i:])
}

func isNameStart(r rune, sx *syntax) bool {
	if r < utf8.RuneSelf {
		c := byte(r)
		return isASCIILetter(c) || c == '_' || (c == '$' && sx.dollarIdent)
	}
	return r != utf8.RuneError && unicode.IsLetter(r)
}

func isNamePart(r rune, sx *syntax) bool {
	if r < utf8.RuneSelf {
		c := byte(r)
		return isASCIILetter(c) || isDigit(c) || c == '_' || (c == '$' && sx.dollarIdent)
	}
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r))
}

func isASCIILetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }
func isDigit(c byte) bool       { return c >= '0' && c <= '9' }

func hasPrefixAt(src []byte, i int, p string) bool {
	if i+len(p) > len(src) {
		return false
	}
	for k := 0; k < len(p); k++ {
		if src[i+k] != p[k] {
			return false
		}
	}
	return true
}

func asciiLower(b []byte) []byte {
	for _, c := range b {
		if c >= 'A' && c <= 'Z' {
			out := make([]byte, len(b))
			for k, d := range b {
				if d >= 'A' && d <= 'Z' {
					d += 'a' - 'A'
				}
				out[k] = d
			}
			return out
		}
	}
	return b
}
