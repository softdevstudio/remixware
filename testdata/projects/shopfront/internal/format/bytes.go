// Package format formats numbers for the storefront.
package format

import (
	"fmt"
	"strings"
)

// Size formats a file size with binary units.
func Size(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	text := fmt.Sprintf("%.1f", value)
	text = strings.TrimSuffix(text, ".0")
	return text + " " + string("KMGTPE"[exp]) + "iB"
}

// Price formats cents as a price with two decimals.
func Price(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, currency, cents/100, cents%100)
}
