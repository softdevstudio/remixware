package remixware

// Text from elsewhere (SCANOSS answers, results and findings files, indexes
// made on another machine) ends up in terminals, reports and notice files.
// These helpers keep it from acting on them, and change nothing else, so
// copyright lines and names still match where they came from.

import (
	"strings"
	"unicode/utf8"
)

// terminalRune reports characters a terminal or a text viewer acts on
// instead of showing: control characters other than tab, DEL, the C1
// controls, line and paragraph separators, and the marks that change the
// direction of text.
func terminalRune(r rune) bool {
	switch {
	case r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// unsafeText reports whether s holds a character a terminal acts on, or
// bytes that aren't UTF-8 (a lone 0x9b is a control sequence to some
// terminals).
func unsafeText(s string) bool {
	return !utf8.ValidString(s) || strings.IndexFunc(s, terminalRune) >= 0
}

// oneLine keeps text from elsewhere to one line that shows as it is:
// characters a terminal acts on, line breaks included, become spaces, and
// bytes that aren't UTF-8 become U+FFFD. Everything else stays, spacing
// and invisible joiners included.
func oneLine(s string) string {
	if !unsafeText(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if terminalRune(r) {
			return ' '
		}
		return r
	}, s)
}

// textBlock is oneLine for a text of several lines, such as a licence text
// or a file a copyright line is looked for in: line breaks and tabs stay,
// and other characters a terminal acts on become spaces, as in oneLine, so
// a line cleaned by oneLine is still found in a text cleaned by textBlock.
func textBlock(s string) string {
	if !unsafeText(s) {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || !terminalRune(r) {
			return r
		}
		return ' '
	}, s)
}

// printable is oneLine for a message: runs of spaces become one, and the
// text is cut at max characters.
func printable(s string, max int) string {
	s = strings.Join(strings.Fields(oneLine(s)), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "..."
	}
	return s
}

// mdLine makes one line of text safe to start a line of a Markdown file
// with: it can't become a heading, a quote, a list, a table, a fence or an
// HTML block, which could hide the rest of the file. Text that doesn't
// start like one of those is left alone.
func mdLine(s string) string {
	s = oneLine(s)
	t := strings.TrimLeft(s, " \t")
	if t == "" {
		return s
	}
	if strings.ContainsRune(`#>-+*=|~<[`+"`", rune(t[0])) {
		return `\` + t
	}
	// An ordered list: digits followed by "." or ")".
	i := 0
	for i < len(t) && i < 10 && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i < len(t) && (t[i] == '.' || t[i] == ')') {
		return t[:i] + `\` + t[i:]
	}
	return t
}

// cutText cuts s to at most max bytes, at a character boundary.
func cutText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// TerminalText is what a command line prints: line breaks and tabs stay,
// and each character a terminal acts on shows as U+FFFD. The command line
// passes everything it prints through it, so text from indexes and results
// made elsewhere, and file names, can't move the cursor, clear the screen
// or change the colours.
func TerminalText(s string) string {
	if !unsafeText(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || !terminalRune(r) {
			return r
		}
		return utf8.RuneError
	}, s)
}
