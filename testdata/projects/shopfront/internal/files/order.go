// Package files lists and orders uploaded files.
package files

import "sort"

// lessNatural orders names so that numbers inside them sort by value.
func lessNatural(left, right string) bool {
	p, q := 0, 0
	for p < len(left) && q < len(right) {
		x, y := left[p], right[q]
		if digit(x) && digit(y) {
			startP := p
			for p < len(left) && digit(left[p]) {
				p++
			}
			startQ := q
			for q < len(right) && digit(right[q]) {
				q++
			}
			numL := dropZeros(left[startP:p])
			numR := dropZeros(right[startQ:q])
			if len(numL) != len(numR) {
				return len(numL) < len(numR)
			}
			if numL != numR {
				return numL < numR
			}
			continue
		}
		if x != y {
			return x < y
		}
		p++
		q++
	}
	return len(left)-p < len(right)-q
}

func digit(c byte) bool { return c >= '0' && c <= '9' }

func dropZeros(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// Sorted returns the names in natural order.
func Sorted(names []string) []string {
	out := append([]string(nil), names...)
	sort.SliceStable(out, func(i, j int) bool { return lessNatural(out[i], out[j]) })
	return out
}
