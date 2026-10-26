// Package fastcsv splits and joins comma-separated records.
package fastcsv

import (
	"errors"
	"strings"
)

// ErrQuote is returned for a field with an unterminated quote.
var ErrQuote = errors.New("fastcsv: unterminated quote")

// SplitRecord splits one line into fields. Fields may be quoted with double
// quotes; a doubled quote inside a quoted field stands for one quote.
func SplitRecord(line string, sep rune) ([]string, error) {
	var fields []string
	var field strings.Builder
	inQuotes := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case inQuotes && r == '"':
			if i+1 < len(runes) && runes[i+1] == '"' {
				field.WriteRune('"')
				i++
			} else {
				inQuotes = false
			}
		case inQuotes:
			field.WriteRune(r)
		case r == '"' && field.Len() == 0:
			inQuotes = true
		case r == sep:
			fields = append(fields, field.String())
			field.Reset()
		default:
			field.WriteRune(r)
		}
	}
	if inQuotes {
		return nil, ErrQuote
	}
	fields = append(fields, field.String())
	return fields, nil
}

// JoinRecord joins fields into one line, quoting the fields that need it.
func JoinRecord(fields []string, sep rune) string {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteRune(sep)
		}
		if strings.ContainsRune(f, sep) || strings.ContainsAny(f, "\"\r\n") {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(f, `"`, `""`))
			b.WriteByte('"')
			continue
		}
		b.WriteString(f)
	}
	return b.String()
}

// TrimFields trims spaces around every field and drops empty trailing fields.
func TrimFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.TrimSpace(f))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
