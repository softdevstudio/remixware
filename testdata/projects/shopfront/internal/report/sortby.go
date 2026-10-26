// Package report builds the daily sales report.
package report

import (
	"sort"
	"strconv"
	"strings"
)

// Line is one row of the report.
type Line struct {
	SKU   string
	Name  string
	Units int
	Cents int64
}

// byRevenue sorts lines with the highest revenue first.
type byRevenue []Line

func (s byRevenue) Len() int      { return len(s) }
func (s byRevenue) Swap(a, b int) { s[a], s[b] = s[b], s[a] }
func (s byRevenue) Less(a, b int) bool {
	return s[a].Cents > s[b].Cents
}

// SortSKUs orders SKUs such as "A-9" and "A-10" by their number part.
// Written for this project: it splits at the last dash and compares the
// numbers as integers.
func SortSKUs(skus []string) {
	key := func(s string) (string, int) {
		cut := strings.LastIndex(s, "-")
		if cut < 0 {
			return s, -1
		}
		n, err := strconv.Atoi(s[cut+1:])
		if err != nil {
			return s, -1
		}
		return s[:cut], n
	}
	sort.Slice(skus, func(x, y int) bool {
		px, nx := key(skus[x])
		py, ny := key(skus[y])
		if px != py {
			return px < py
		}
		return nx < ny
	})
}

// Top returns the n best-selling lines.
func Top(lines []Line, n int) []Line {
	out := append([]Line(nil), lines...)
	sort.Sort(byRevenue(out))
	if len(out) > n {
		out = out[:n]
	}
	return out
}
