// Command sortnames sorts firmware image names by version.
package main

import (
	"bufio"
	"fmt"
	"os"
)

func mergeRuns(dst, left, right []string, less func(a, b string) bool) []string {
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

func sortNames(names []string, less func(a, b string) bool) {
	buf := make([]string, len(names))
	for width := 1; width < len(names); width *= 2 {
		for lo := 0; lo < len(names); lo += 2 * width {
			mid := min(lo+width, len(names))
			hi := min(lo+2*width, len(names))
			merged := mergeRuns(buf[lo:lo], names[lo:mid], names[mid:hi], less)
			copy(names[lo:hi], merged)
		}
	}
}

func main() {
	var names []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		names = append(names, sc.Text())
	}
	sortNames(names, func(a, b string) bool { return a < b })
	for _, n := range names {
		fmt.Println(n)
	}
}
