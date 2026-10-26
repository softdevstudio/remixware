package snippets

import (
	"fmt"
	"strings"
)

// HumanBytes formats a byte count with binary units: 1536 -> "1.5 KiB".
func HumanBytes(n int64) string {
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

// Plural picks the singular or plural form of a word for a count.
func Plural(n int, one, many string) string {
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	if many == "" {
		many = one + "s"
	}
	return fmt.Sprintf("%d %s", n, many)
}
