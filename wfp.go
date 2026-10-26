package remixware

// Winnowing fingerprints in the format the SCANOSS service reads (WFP).
// The algorithm and its settings follow scanoss.py's winnowing.py
// (Copyright (c) 2021, SCANOSS, MIT License; see THIRD_PARTY_LICENSES.md),
// so that the service matches what Remixware sends exactly as it matches
// what its own scanner sends. It is separate from Remixware's own
// fingerprints in fingerprint.go, which work on tokens.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Settings of SCANOSS's winnowing. The service expects exactly these.
const (
	wfpGram         = 30
	wfpWindow       = 64
	wfpMinFileSize  = 256  // files of this many characters or fewer get no snippet fingerprints
	wfpMaxFirstLine = 1000 // nor do files whose first line is longer
)

var crc32c = crc32.MakeTable(crc32.Castagnoli)

// File endings SCANOSS takes no snippet fingerprints of.
var wfpSkipSnippetExt = []string{".exe", ".zip", ".tar", ".tgz", ".gz", ".7z", ".rar", ".jar", ".war", ".ear",
	".class", ".pyc", ".o", ".a", ".so", ".obj", ".dll", ".lib", ".out", ".app", ".bin", ".lst", ".dat", ".json",
	".htm", ".html", ".xml", ".md", ".txt", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods",
	".odp", ".pages", ".key", ".numbers", ".pdf", ".min.js", ".mf", ".sum", ".woff", ".woff2", ".xsd", ".pom", ".whl"}

// WFP returns the SCANOSS fingerprint of a file: a "file=" line with its
// MD5, its size and name, a "fh2=" line with the MD5 of the same text with
// the other line endings, and lines of snippet hashes, each as
// "<line>=<hash>,<hash>". name is what the "file=" line calls the file;
// path is its real name, whose ending decides whether snippets are taken.
func WFP(name, path string, data []byte) string {
	var b strings.Builder
	sum := md5.Sum(data)
	fmt.Fprintf(&b, "file=%s,%d,%s\n", hex.EncodeToString(sum[:]), len(data), name)
	binaryFile := wfpBinary(data)
	if !binaryFile {
		if h := oppositeLineEndingsMD5(data); h != "" {
			fmt.Fprintf(&b, "fh2=%s\n", h)
		}
	}
	if binaryFile || wfpSkipSnippets(path, data) {
		return b.String()
	}
	var (
		gram     [wfpGram]byte
		gramLen  int
		window   = make([]uint32, 0, wfpWindow)
		line     = 1
		lastHash = uint64(1) << 32 // no hash yet
		lastLine = 0
		out      strings.Builder
	)
	var four [4]byte
	for _, c := range data {
		var n byte
		if c == '\n' {
			line++
		} else {
			n = wfpNormalize(c)
		}
		if n == 0 {
			continue
		}
		if gramLen < wfpGram {
			gram[gramLen] = n
			gramLen++
		} else {
			copy(gram[:], gram[1:])
			gram[wfpGram-1] = n
		}
		if gramLen < wfpGram {
			continue
		}
		window = append(window, crc32.Checksum(gram[:], crc32c))
		if len(window) < wfpWindow {
			continue
		}
		minHash := window[0]
		for _, h := range window[1:] {
			if h < minHash {
				minHash = h
			}
		}
		if uint64(minHash) != lastHash {
			// Hashing the selected hash again spreads the values evenly.
			binary.LittleEndian.PutUint32(four[:], minHash)
			hash := fmt.Sprintf("%08x", crc32.Checksum(four[:], crc32c))
			if lastLine != line {
				if out.Len() > 0 {
					b.WriteString(out.String())
					b.WriteByte('\n')
				}
				out.Reset()
				fmt.Fprintf(&out, "%d=%s", line, hash)
			} else {
				out.WriteByte(',')
				out.WriteString(hash)
			}
			lastLine = line
			lastHash = uint64(minHash)
		}
		window = window[1:]
		if cap(window) == len(window) {
			// Keep the window's backing array from growing without end.
			window = append(make([]uint32, 0, wfpWindow), window...)
		}
	}
	if out.Len() > 0 {
		b.WriteString(out.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// wfpNormalize keeps digits and letters, lower-cased, and drops the rest.
func wfpNormalize(c byte) byte {
	switch {
	case c >= '0' && c <= '9', c >= 'a' && c <= 'z':
		return c
	case c >= 'A' && c <= 'Z':
		return c + 32
	}
	return 0
}

// wfpBinary reports whether data looks like a binary file.
func wfpBinary(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// oppositeLineEndingsMD5 is the MD5 of the text with Windows line endings
// if it has Unix ones (or a mix), and with Unix ones if it has only Windows
// ones. It is empty for text with no line endings at all.
func oppositeLineEndingsMD5(data []byte) string {
	hasCRLF := bytes.Contains(data, []byte("\r\n"))
	rest := bytes.ReplaceAll(data, []byte("\r\n"), nil)
	hasLF := bytes.IndexByte(rest, '\n') >= 0
	hasCR := bytes.IndexByte(rest, '\r') >= 0
	if !hasCRLF && !hasLF && !hasCR {
		return ""
	}
	norm := bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
	if !(hasCRLF && !hasLF && !hasCR) {
		norm = bytes.ReplaceAll(norm, []byte("\n"), []byte("\r\n"))
	}
	sum := md5.Sum(norm)
	return hex.EncodeToString(sum[:])
}

// wfpSkipSnippets reports whether SCANOSS takes no snippet fingerprints of
// a file: some endings, small files, and files that start like JSON, XML
// or HTML or with a very long line. Sizes count characters of the text
// read as UTF-8, invalid bytes left out, as scanoss.py counts them.
func wfpSkipSnippets(path string, data []byte) bool {
	low := strings.ToLower(path)
	for _, e := range wfpSkipSnippetExt {
		if strings.HasSuffix(low, e) {
			return true
		}
	}
	var runes []rune
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size > 1 {
			runes = append(runes, r)
		}
		i += size
	}
	if len(runes) <= wfpMinFileSize {
		return true
	}
	prefix := strings.TrimFunc(strings.ToLower(string(runes[:wfpMinFileSize-1])), unicode.IsSpace)
	if strings.HasPrefix(prefix, "{") || strings.HasPrefix(prefix, "[") {
		return true
	}
	for _, p := range []string{"<?xml", "<html", "<ac3d", "<!doc"} {
		if strings.HasPrefix(prefix, p) {
			return true
		}
	}
	first := len(runes) - 1
	for i, r := range runes {
		if r == '\n' {
			first = i
			break
		}
	}
	return first > wfpMaxFirstLine
}
