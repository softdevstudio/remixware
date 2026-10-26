// Copyright (C) 2019 Tomas Lindqvist
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package gplsort sorts names the way people expect.
package gplsort

// NaturalLess compares two strings the way people expect file names to sort:
// runs of digits compare by their numeric value, so "file9" comes before
// "file10".
func NaturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if isDigit(ca) && isDigit(cb) {
			si := i
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			sj := j
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := trimZeros(a[si:i])
			nb := trimZeros(b[sj:j])
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func trimZeros(s string) string {
	for len(s) > 1 && s[0] == '0' {
		s = s[1:]
	}
	return s
}

// MergeRuns merges two sorted runs into dst using less.
func MergeRuns(dst, left, right []string, less func(a, b string) bool) []string {
	dst = dst[:0]
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		if less(right[j], left[i]) {
			dst = append(dst, right[j])
			j++
		} else {
			dst = append(dst, left[i])
			i++
		}
	}
	dst = append(dst, left[i:]...)
	dst = append(dst, right[j:]...)
	return dst
}

// SortNatural sorts names in natural order with a bottom-up merge sort.
func SortNatural(names []string) {
	buf := make([]string, len(names))
	for width := 1; width < len(names); width *= 2 {
		for lo := 0; lo < len(names); lo += 2 * width {
			mid := min(lo+width, len(names))
			hi := min(lo+2*width, len(names))
			merged := MergeRuns(buf[lo:lo], names[lo:mid], names[mid:hi], NaturalLess)
			copy(names[lo:hi], merged)
		}
	}
}
