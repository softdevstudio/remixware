// Package csvio reads the product catalog.
package csvio

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

// Header maps column names to their positions.
func Header(fields []string) map[string]int {
	m := make(map[string]int, len(fields))
	for i, f := range fields {
		m[strings.ToLower(strings.TrimSpace(f))] = i
	}
	return m
}
