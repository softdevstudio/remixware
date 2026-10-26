package remixware

import (
	"regexp"
	"sort"
	"strings"
)

// Category groups licences by what copying code under them asks of you.
type Category int

const (
	CatPublicDomain    Category = iota // no conditions
	CatPermissive                      // keep the notice
	CatWeakCopyleft                    // the copied file or library stays under its licence
	CatCopyleft                        // the whole program must use a compatible licence
	CatNetworkCopyleft                 // copyleft that also covers use over a network
	CatSourceAvailable                 // readable, but not open source
	CatUnknown                         // a licence Remixware doesn't recognise
	CatNone                            // no licence at all
)

func (c Category) String() string {
	return [...]string{"public domain", "permissive", "weak copyleft", "copyleft", "network copyleft",
		"source available", "unknown", "no licence"}[c]
}

// License is one known licence.
type License struct {
	ID   string
	Name string
	Cat  Category
}

// The special values used where an SPDX expression is expected.
const (
	LicenseNone         = "NONE"        // no licence found
	LicenseNoAssertion  = "NOASSERTION" // a licence is there but wasn't recognised
	LicenseProprietary  = "LicenseRef-Proprietary"
	LicenseUnrecognised = "LicenseRef-Unrecognised" // a licence file whose text wasn't recognised
)

var knownLicenses = []License{
	{"Unlicense", "The Unlicense", CatPublicDomain},
	{"CC0-1.0", "Creative Commons Zero 1.0", CatPublicDomain},
	{"0BSD", "Zero-Clause BSD", CatPublicDomain},
	{"MIT-0", "MIT No Attribution", CatPublicDomain},
	{"WTFPL", "WTFPL", CatPublicDomain},
	{"LicenseRef-Public-Domain", "Public domain dedication", CatPublicDomain},

	{"MIT", "MIT License", CatPermissive},
	{"BSD-2-Clause", "BSD 2-Clause License", CatPermissive},
	{"BSD-3-Clause", "BSD 3-Clause License", CatPermissive},
	{"BSD-4-Clause", "BSD 4-Clause License", CatPermissive},
	{"ISC", "ISC License", CatPermissive},
	{"Zlib", "zlib License", CatPermissive},
	{"BSL-1.0", "Boost Software License 1.0", CatPermissive},
	{"Apache-2.0", "Apache License 2.0", CatPermissive},
	{"Apache-1.1", "Apache License 1.1", CatPermissive},
	{"PSF-2.0", "Python Software Foundation License 2.0", CatPermissive},
	{"Python-2.0", "Python License 2.0", CatPermissive},
	{"Artistic-2.0", "Artistic License 2.0", CatPermissive},
	{"CC-BY-4.0", "Creative Commons Attribution 4.0", CatPermissive},
	{"BlueOak-1.0.0", "Blue Oak Model License 1.0.0", CatPermissive},
	{"PostgreSQL", "PostgreSQL License", CatPermissive},
	{"NCSA", "University of Illinois/NCSA License", CatPermissive},
	{"Unicode-3.0", "Unicode License v3", CatPermissive},
	{"X11", "X11 License", CatPermissive},

	{"LGPL-2.0-only", "GNU Library General Public License v2 only", CatWeakCopyleft},
	{"LGPL-2.0-or-later", "GNU Library General Public License v2 or later", CatWeakCopyleft},
	{"LGPL-2.1-only", "GNU Lesser General Public License v2.1 only", CatWeakCopyleft},
	{"LGPL-2.1-or-later", "GNU Lesser General Public License v2.1 or later", CatWeakCopyleft},
	{"LGPL-3.0-only", "GNU Lesser General Public License v3.0 only", CatWeakCopyleft},
	{"LGPL-3.0-or-later", "GNU Lesser General Public License v3.0 or later", CatWeakCopyleft},
	{"MPL-1.1", "Mozilla Public License 1.1", CatWeakCopyleft},
	{"MPL-2.0", "Mozilla Public License 2.0", CatWeakCopyleft},
	{"EPL-1.0", "Eclipse Public License 1.0", CatWeakCopyleft},
	{"EPL-2.0", "Eclipse Public License 2.0", CatWeakCopyleft},
	{"CDDL-1.0", "Common Development and Distribution License 1.0", CatWeakCopyleft},
	{"CDDL-1.1", "Common Development and Distribution License 1.1", CatWeakCopyleft},
	{"MS-RL", "Microsoft Reciprocal License", CatWeakCopyleft},

	{"GPL-1.0-only", "GNU General Public License v1.0 only", CatCopyleft},
	{"GPL-1.0-or-later", "GNU General Public License v1.0 or later", CatCopyleft},
	{"GPL-2.0-only", "GNU General Public License v2.0 only", CatCopyleft},
	{"GPL-2.0-or-later", "GNU General Public License v2.0 or later", CatCopyleft},
	{"GPL-3.0-only", "GNU General Public License v3.0 only", CatCopyleft},
	{"GPL-3.0-or-later", "GNU General Public License v3.0 or later", CatCopyleft},
	{"EUPL-1.1", "European Union Public Licence 1.1", CatCopyleft},
	{"EUPL-1.2", "European Union Public Licence 1.2", CatCopyleft},
	{"OSL-3.0", "Open Software License 3.0", CatCopyleft},
	{"CC-BY-SA-4.0", "Creative Commons Attribution Share Alike 4.0", CatCopyleft},

	{"AGPL-3.0-only", "GNU Affero General Public License v3.0 only", CatNetworkCopyleft},
	{"AGPL-3.0-or-later", "GNU Affero General Public License v3.0 or later", CatNetworkCopyleft},

	{"SSPL-1.0", "Server Side Public License v1", CatSourceAvailable},
	{"BUSL-1.1", "Business Source License 1.1", CatSourceAvailable},
	{"Elastic-2.0", "Elastic License 2.0", CatSourceAvailable},
	{"PolyForm-Noncommercial-1.0.0", "PolyForm Noncommercial License 1.0.0", CatSourceAvailable},
	{"PolyForm-Shield-1.0.0", "PolyForm Shield License 1.0.0", CatSourceAvailable},
	{"CC-BY-NC-4.0", "Creative Commons Attribution Non Commercial 4.0", CatSourceAvailable},
	{"CC-BY-NC-SA-4.0", "Creative Commons Attribution Non Commercial Share Alike 4.0", CatSourceAvailable},
	{"CC-BY-ND-4.0", "Creative Commons Attribution No Derivatives 4.0", CatSourceAvailable},
	{"LicenseRef-Commons-Clause", "Commons Clause", CatSourceAvailable},
	{LicenseProprietary, "Proprietary", CatSourceAvailable},
}

var licenseByID = func() map[string]License {
	m := make(map[string]License)
	for _, l := range knownLicenses {
		m[strings.ToLower(l.ID)] = l
	}
	return m
}()

// Older SPDX ids and common spellings, mapped to current ids. Where a GPL
// version is named without "only" or "or later", it is read as "only":
// the stricter of the two.
var licenseAliases = map[string]string{
	"gpl-1.0": "GPL-1.0-only", "gpl-1.0+": "GPL-1.0-or-later",
	"gpl-2.0": "GPL-2.0-only", "gpl-2.0+": "GPL-2.0-or-later", "gplv2": "GPL-2.0-only", "gpl-2": "GPL-2.0-only",
	"gpl-3.0": "GPL-3.0-only", "gpl-3.0+": "GPL-3.0-or-later", "gplv3": "GPL-3.0-only", "gpl-3": "GPL-3.0-only",
	"lgpl-2.0": "LGPL-2.0-only", "lgpl-2.0+": "LGPL-2.0-or-later",
	"lgpl-2.1": "LGPL-2.1-only", "lgpl-2.1+": "LGPL-2.1-or-later", "lgplv2.1": "LGPL-2.1-only",
	"lgpl-3.0": "LGPL-3.0-only", "lgpl-3.0+": "LGPL-3.0-or-later", "lgplv3": "LGPL-3.0-only",
	"agpl-3.0": "AGPL-3.0-only", "agpl-3.0+": "AGPL-3.0-or-later", "agplv3": "AGPL-3.0-only",
	"apache 2.0": "Apache-2.0", "apache2": "Apache-2.0", "apache-2": "Apache-2.0", "apache license 2.0": "Apache-2.0",
	"bsd": "BSD-3-Clause", "new bsd": "BSD-3-Clause", "simplified bsd": "BSD-2-Clause", "bsd-2": "BSD-2-Clause",
	"bsd-3": "BSD-3-Clause", "mit license": "MIT", "expat": "MIT", "isc license": "ISC", "boost": "BSL-1.0",
	"mpl 2.0": "MPL-2.0", "mpl2": "MPL-2.0", "public domain": "LicenseRef-Public-Domain", "public-domain": "LicenseRef-Public-Domain", "cc0": "CC0-1.0",
	"proprietary": LicenseProprietary, "commercial": LicenseProprietary, "unlicensed": LicenseProprietary,
	"licenseref-proprietary": LicenseProprietary, "commons-clause": "LicenseRef-Commons-Clause",
	"psf": "PSF-2.0", "python-2.0.1": "Python-2.0", "zlib": "Zlib",
	"none": LicenseNone, "noassertion": LicenseNoAssertion,
}

// lookupLicense returns the licence for an id, or a License of category
// CatUnknown (or CatNone for NONE) that carries the id as given.
func lookupLicense(id string) License {
	low := strings.ToLower(strings.TrimSpace(id))
	if a, ok := licenseAliases[low]; ok {
		low = strings.ToLower(a)
		id = a
	}
	if l, ok := licenseByID[low]; ok {
		return l
	}
	switch strings.ToUpper(id) {
	case LicenseNone:
		return License{ID: LicenseNone, Name: "no licence found", Cat: CatNone}
	case LicenseNoAssertion, "":
		return License{ID: LicenseNoAssertion, Name: "unrecognised licence", Cat: CatUnknown}
	}
	return License{ID: id, Name: id, Cat: CatUnknown}
}

// ---------------------------------------------------------------------------
// SPDX licence expressions: "MIT", "MIT OR Apache-2.0",
// "GPL-2.0-only WITH Classpath-exception-2.0", "(A OR B) AND C".

type licExpr struct {
	op   string // "" for a single licence, else "AND" or "OR"
	id   string
	with string
	kids []*licExpr
}

func parseLicenseExpr(s string) *licExpr {
	s = strings.TrimSpace(s)
	if s == "" {
		return &licExpr{id: LicenseNoAssertion}
	}
	toks := splitExpr(s)
	p := &exprParser{toks: toks}
	e := p.or()
	if e == nil || p.pos < len(p.toks) {
		// Not a valid expression: keep the whole text as one unknown id.
		return &licExpr{id: s}
	}
	return e
}

func splitExpr(s string) []string {
	var out []string
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '/':
			// Old Cargo manifests write "MIT/Apache-2.0" for "MIT OR Apache-2.0".
			flush()
			out = append(out, string(r))
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

type exprParser struct {
	toks []string
	pos  int
}

func (p *exprParser) peekOp(op string) bool {
	return p.pos < len(p.toks) && strings.EqualFold(p.toks[p.pos], op)
}

func (p *exprParser) or() *licExpr {
	left := p.and()
	if left == nil {
		return nil
	}
	if !p.peekOp("OR") && !p.peekOp("/") {
		return left
	}
	e := &licExpr{op: "OR", kids: []*licExpr{left}}
	for p.peekOp("OR") || p.peekOp("/") {
		p.pos++
		k := p.and()
		if k == nil {
			return nil
		}
		e.kids = append(e.kids, k)
	}
	return e
}

func (p *exprParser) and() *licExpr {
	left := p.atom()
	if left == nil {
		return nil
	}
	if !p.peekOp("AND") {
		return left
	}
	e := &licExpr{op: "AND", kids: []*licExpr{left}}
	for p.peekOp("AND") {
		p.pos++
		k := p.atom()
		if k == nil {
			return nil
		}
		e.kids = append(e.kids, k)
	}
	return e
}

func (p *exprParser) atom() *licExpr {
	if p.pos >= len(p.toks) {
		return nil
	}
	t := p.toks[p.pos]
	if t == "(" {
		p.pos++
		e := p.or()
		if e == nil || p.pos >= len(p.toks) || p.toks[p.pos] != ")" {
			return nil
		}
		p.pos++
		return e
	}
	if t == ")" || t == "/" || strings.EqualFold(t, "AND") || strings.EqualFold(t, "OR") || strings.EqualFold(t, "WITH") {
		return nil
	}
	p.pos++
	e := &licExpr{id: canonicalID(t)}
	if p.peekOp("WITH") {
		p.pos++
		if p.pos >= len(p.toks) {
			return nil
		}
		e.with = p.toks[p.pos]
		p.pos++
	}
	return e
}

// canonicalID turns "gpl-2.0+" into "GPL-2.0-or-later", "mit" into "MIT".
func canonicalID(id string) string {
	l := lookupLicense(id)
	if l.Cat == CatUnknown && l.ID != LicenseNoAssertion {
		return id
	}
	return l.ID
}

func (e *licExpr) String() string {
	if e.op == "" {
		if e.with != "" {
			return e.id + " WITH " + e.with
		}
		return e.id
	}
	parts := make([]string, len(e.kids))
	for i, k := range e.kids {
		s := k.String()
		if k.op != "" && k.op != e.op {
			s = "(" + s + ")"
		}
		parts[i] = s
	}
	return strings.Join(parts, " "+e.op+" ")
}

func (e *licExpr) leaves() []*licExpr {
	if e.op == "" {
		return []*licExpr{e}
	}
	var out []*licExpr
	for _, k := range e.kids {
		out = append(out, k.leaves()...)
	}
	return out
}

// NormalizeLicense returns the canonical form of an SPDX expression.
func NormalizeLicense(s string) string { return parseLicenseExpr(s).String() }

// joinLicenses combines several licences found for one thing. All of them
// apply, so they are joined with AND.
func joinLicenses(ids []string) string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		n := NormalizeLicense(id)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	switch len(out) {
	case 0:
		return LicenseNone
	case 1:
		return out[0]
	}
	sort.Strings(out)
	for i, s := range out {
		if strings.Contains(s, " OR ") {
			out[i] = "(" + s + ")"
		}
	}
	return strings.Join(out, " AND ")
}

// ---------------------------------------------------------------------------
// Detecting a licence from its text.

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normText(s string) string {
	return " " + strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " ")) + " "
}

var spdxTag = regexp.MustCompile(`SPDX-License-Identifier:\s*([^\r\n]+)`)

// spdxTagLicense returns the expression in an SPDX-License-Identifier line
// near the top of a file.
func spdxTagLicense(head string) string {
	m := spdxTag.FindStringSubmatch(head)
	if m == nil {
		return ""
	}
	v := strings.TrimSpace(m[1])
	for _, end := range []string{"*/", "-->", "#}", "--}}", "*)"} {
		v = strings.TrimSpace(strings.TrimSuffix(v, end))
	}
	return NormalizeLicense(v)
}

// licenseSignature is a phrase that starts a licence text or names a
// licence in a notice, and the function that reads the exact licence from
// the text that follows it.
type licenseSignature struct {
	phrase  string
	resolve func(rest, all string) string
}

// versionIn returns the value paired with whichever option phrase comes
// first in the opening part of rest (section numbers like "1.1" further on
// are not versions).
func versionIn(rest string, limit int, options ...string) string {
	if len(rest) > limit {
		rest = rest[:limit]
	}
	best, at := "", len(rest)+1
	for i := 0; i+1 < len(options); i += 2 {
		if j := strings.Index(rest, options[i]); j >= 0 && j < at {
			best, at = options[i+1], j
		}
	}
	return best
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// gnuLicense reads a GNU licence and its version from the text after its
// name. "Or any later version" only counts in a short notice: a full
// licence file always contains it in its "how to apply" example.
func gnuLicense(base string) func(rest, all string) string {
	return func(rest, all string) string {
		later := strings.Contains(rest, " or at your option any later version ") || strings.Contains(rest, " or any later version ")
		full := strings.Contains(all, " terms and conditions ")
		v := versionIn(rest, 400, " version 3 ", "3.0", " version 2 1 ", "2.1", " version 2 ", "2.0", " version 1 ", "1.0",
			" v3 ", "3.0", " v2 1 ", "2.1", " v2 ", "2.0")
		switch base {
		case "AGPL":
			v = "3.0"
		case "LGPL":
			if v != "3.0" {
				v = "2.1"
			}
		case "LGPL-lib":
			base, v = "LGPL", "2.0"
		default:
			if v == "" || v == "2.1" {
				v = "2.0"
			}
		}
		if later && !full {
			return base + "-" + v + "-or-later"
		}
		return base + "-" + v + "-only"
	}
}

func fixed(id string) func(string, string) string { return func(string, string) string { return id } }

var licenseSignatures = []licenseSignature{
	{"gnu affero general public license", gnuLicense("AGPL")},
	{"gnu lesser general public license", gnuLicense("LGPL")},
	{"gnu library general public license", gnuLicense("LGPL-lib")},
	{"gnu general public license", gnuLicense("GPL")},
	{"mozilla public license", func(rest, _ string) string {
		return orDefault(versionIn(rest, 80, " 1 1 ", "MPL-1.1", " 2 0 ", "MPL-2.0"), "MPL-2.0")
	}},
	{"eclipse public license", func(rest, _ string) string {
		return orDefault(versionIn(rest, 80, " 2 0 ", "EPL-2.0", " 1 0 ", "EPL-1.0"), "EPL-1.0")
	}},
	{"apache license", func(rest, _ string) string {
		return orDefault(versionIn(rest, 80, " 2 0 ", "Apache-2.0", " 1 1 ", "Apache-1.1"), "Apache-2.0")
	}},
	{"common development and distribution license", func(rest, _ string) string {
		return orDefault(versionIn(rest, 120, " 1 1 ", "CDDL-1.1"), "CDDL-1.0")
	}},
	{"european union public licen", func(rest, _ string) string {
		return orDefault(versionIn(rest, 80, " 1 1 ", "EUPL-1.1"), "EUPL-1.2")
	}},
	{"server side public license", fixed("SSPL-1.0")},
	{"business source license", fixed("BUSL-1.1")},
	{"elastic license 2 0", fixed("Elastic-2.0")},
	{"polyform noncommercial license", fixed("PolyForm-Noncommercial-1.0.0")},
	{"polyform shield license", fixed("PolyForm-Shield-1.0.0")},
	{"commons clause", fixed("LicenseRef-Commons-Clause")},
	{"open software license", fixed("OSL-3.0")},
	{"microsoft reciprocal license", fixed("MS-RL")},
	{"boost software license", fixed("BSL-1.0")},
	{"blue oak model license", fixed("BlueOak-1.0.0")},
	{"artistic license 2 0", fixed("Artistic-2.0")},
	{"python software foundation license", fixed("PSF-2.0")},
	{"university of illinois ncsa open source license", fixed("NCSA")},
	{"this is free and unencumbered software released into the public domain", fixed("Unlicense")},
	{"do what the fuck you want to public license", fixed("WTFPL")},
	{"cc0 1 0 universal", fixed("CC0-1.0")},
	{"creative commons zero", fixed("CC0-1.0")},
	{"creative commons legal code cc0", fixed("CC0-1.0")},
	{"creative commons attribution", func(rest, _ string) string {
		switch {
		case strings.HasPrefix(rest, " creative commons attribution noncommercial sharealike"):
			return "CC-BY-NC-SA-4.0"
		case strings.HasPrefix(rest, " creative commons attribution noncommercial"):
			return "CC-BY-NC-4.0"
		case strings.HasPrefix(rest, " creative commons attribution noderivatives"):
			return "CC-BY-ND-4.0"
		case strings.HasPrefix(rest, " creative commons attribution sharealike"):
			return "CC-BY-SA-4.0"
		}
		return "CC-BY-4.0"
	}},
	{"permission is hereby granted free of charge to any person obtaining a copy", func(_, all string) string {
		if strings.Contains(all, " the above copyright notice and this permission notice shall be included ") {
			return "MIT"
		}
		return "MIT-0"
	}},
	{"permission to use copy modify and or distribute this software for any purpose with or without fee is hereby granted", isc},
	{"permission to use copy modify and distribute this software for any purpose with or without fee is hereby granted", isc},
	{"redistribution and use in source and binary forms with or without modification are permitted", func(_, all string) string {
		switch {
		case strings.Contains(all, " all advertising materials mentioning features or use of this software "):
			return "BSD-4-Clause"
		case strings.Contains(all, " neither the name ") || strings.Contains(all, " may be used to endorse or promote products derived "):
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	}},
	{"this software is provided as is without any express or implied warranty in no event will the authors be held liable", func(_, all string) string {
		if strings.Contains(all, " altered source versions must be plainly marked as such ") {
			return "Zlib"
		}
		return ""
	}},
	{"permission to use copy modify and distribute this software and its documentation for any purpose", func(_, all string) string {
		if strings.Contains(all, " postgresql ") {
			return "PostgreSQL"
		}
		return "X11"
	}},
	{"mit license", fixed("MIT")},
}

func isc(_, all string) string {
	if strings.Contains(all, " provided that the above copyright notice and this permission notice appear in all copies ") {
		return "ISC"
	}
	return "0BSD"
}

// DetectLicense names the licence in a licence file or a licence notice.
// It returns "" when the text holds no licence it recognises. Licence texts
// mention other licences (MPL-2.0 names the GPL, the GPL names the LGPL), so
// the licence whose own wording comes first in the text wins.
func DetectLicense(text string) string {
	if id := spdxTagLicense(firstLines(text, 30)); id != "" {
		return id
	}
	t := normText(text)
	type at struct {
		pos int
		sig licenseSignature
	}
	var found []at
	for _, sg := range licenseSignatures {
		if i := strings.Index(t, " "+sg.phrase+" "); i >= 0 {
			found = append(found, at{i, sg})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].pos < found[j].pos })
	for _, f := range found {
		if id := f.sig.resolve(t[f.pos:], t); id != "" {
			return id
		}
	}
	return ""
}

func firstLines(s string, n int) string {
	i := 0
	for k := 0; k < n; k++ {
		j := strings.IndexByte(s[i:], '\n')
		if j < 0 {
			return s
		}
		i += j + 1
	}
	return s[:i]
}

// headerComments returns the comment text at the top of a source file, where
// licence notices live, without the comment markers.
func headerComments(src string, maxLines int) string {
	var b strings.Builder
	for _, raw := range strings.Split(firstLines(src, maxLines), "\n") {
		l := strings.TrimSpace(raw)
		switch {
		case l == "":
			continue
		case strings.HasPrefix(l, "//"), strings.HasPrefix(l, "#"), strings.HasPrefix(l, "--"),
			strings.HasPrefix(l, "/*"), strings.HasPrefix(l, "*"), strings.HasPrefix(l, "<!--"),
			strings.HasPrefix(l, "\"\"\""), strings.HasPrefix(l, "'''"), strings.HasPrefix(l, ";"):
			b.WriteString(strings.TrimLeft(l, "/#*-<!;\"' "))
			b.WriteByte('\n')
		default:
			// Some licence notices sit in a docstring or block comment
			// without leading markers; keep the line, the detector only
			// reacts to licence wording.
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

var copyrightLine = regexp.MustCompile(`(?i)^(?:copyright\b|\(c\)\s|©)`)

// copyrightLines returns the copyright statements in a text, such as
// "Copyright (c) 2019 Ada Lovelace", without template placeholders.
func copyrightLines(text string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		l := strings.TrimSpace(raw)
		l = strings.TrimSpace(strings.TrimLeft(l, "/#*-;!<>\"' \t"))
		l = strings.TrimSpace(strings.TrimRight(l, "*/ \t"))
		if len(l) < 10 || len(l) > 200 || !copyrightLine.MatchString(l) {
			continue
		}
		// "Copyright 2009 The Go Authors. All rights reserved. Use of this
		// source code ..." keeps only the copyright.
		if m := copyrightTail.FindStringIndex(l); m != nil {
			l = strings.TrimSpace(strings.TrimSuffix(l[:m[0]+1], ";"))
		}
		low := strings.ToLower(l)
		if strings.ContainsAny(l, "<[") && (strings.Contains(low, "year") || strings.Contains(low, "owner") ||
			strings.Contains(low, "holder") || strings.Contains(low, "yyyy") || strings.Contains(low, "name of")) {
			continue
		}
		bad := false
		for _, w := range []string{"copyright notice", "copyright holders", "copyright holder ", "copyright owner",
			"copyright law", "copyright and license", "copyright and related", "copyright license", "copyright statement",
			"copyright, patent", "copyright protection", "copyrighted", "copyright interest", "copyright notices"} {
			if strings.Contains(low, w) {
				bad = true
				break
			}
		}
		if bad || !(strings.ContainsAny(l, "0123456789") || strings.Contains(low, "(c)") || strings.Contains(l, "©")) {
			continue
		}
		if !seen[low] {
			seen[low] = true
			out = append(out, l)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

var copyrightTail = regexp.MustCompile(`[.;] +(Use of this|Licensed |Released |Distributed |This (file|program|library|software|code|project)|See |Permission |SPDX-|Redistribution|All files|For licens)`)

var yearsAndMarks = regexp.MustCompile(`(?i)copyright|\(c\)|©|all rights reserved\.?|\b\d{4}(\s*[-–,]\s*(\d{4}|present))*\b|[,.]`)

// copyrightHolder strips "Copyright (c) 2019-2024" and similar from a
// copyright line, leaving the holder's name for searching notice files.
func copyrightHolder(line string) string {
	h := yearsAndMarks.ReplaceAllString(line, " ")
	h = strings.Join(strings.Fields(h), " ")
	h = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(h, "by "), "the "))
	return h
}

// ---------------------------------------------------------------------------
// What a licence asks of you, and whether it fits the project's licence.

// Base statuses before the project's notice files are checked.
const (
	baseOK          = "ok"
	baseAttribution = "attribution"
	baseReview      = "review"
	baseConflict    = "conflict"
	baseNoLicense   = "no-license"
)

var baseRank = map[string]int{baseOK: 0, baseAttribution: 1, baseReview: 2, baseNoLicense: 3, baseConflict: 4}

type verdict struct {
	status string
	reason string
}

// projectKind is the project's licence reduced to what compatibility needs.
type projectKind struct {
	id     string
	family string // proprietary, permissive, lgpl2only, lgpl2plus, lgpl3, mpl, weak, gpl2only, gpl2plus, gpl3, agpl3, eupl, copyleft
	note   string
}

func classifyProject(id string) projectKind {
	l := lookupLicense(id)
	k := projectKind{id: l.ID}
	switch l.Cat {
	case CatNone:
		k.family, k.id, k.note = "proprietary", "proprietary", "the project has no licence file, so it is treated as all rights reserved"
		return k
	case CatUnknown:
		k.family, k.id, k.note = "proprietary", "proprietary", "the project's licence wasn't recognised, so it is treated as proprietary"
		return k
	case CatSourceAvailable:
		k.family = "proprietary"
		if l.ID == LicenseProprietary {
			k.id = "proprietary"
		}
		return k
	case CatPublicDomain, CatPermissive:
		k.family = "permissive"
		return k
	}
	switch l.ID {
	case "GPL-1.0-only", "GPL-2.0-only":
		k.family = "gpl2only"
	case "GPL-1.0-or-later", "GPL-2.0-or-later":
		k.family = "gpl2plus"
	case "GPL-3.0-only", "GPL-3.0-or-later":
		k.family = "gpl3"
	case "AGPL-3.0-only", "AGPL-3.0-or-later":
		k.family = "agpl3"
	case "LGPL-2.0-only", "LGPL-2.1-only":
		k.family = "lgpl2only"
	case "LGPL-2.0-or-later", "LGPL-2.1-or-later":
		k.family = "lgpl2plus"
	case "LGPL-3.0-only", "LGPL-3.0-or-later":
		k.family = "lgpl3"
	case "MPL-2.0", "MPL-1.1":
		k.family = "mpl"
	case "EUPL-1.1", "EUPL-1.2":
		k.family = "eupl"
	default:
		if l.Cat == CatWeakCopyleft {
			k.family = "weak"
		} else {
			k.family = "copyleft"
		}
	}
	return k
}

func gplFamily(f string) bool {
	switch f {
	case "gpl2only", "gpl2plus", "gpl3", "agpl3":
		return true
	}
	return false
}

// evaluate decides what copying code under src means for a project under
// proj. Both are SPDX expressions. A choice of licences (OR) takes the best
// option; licences that all apply (AND) take the worst. When the project
// itself offers a choice, the copied code has to fit every option.
func evaluate(src, proj string) verdict {
	pe := parseLicenseExpr(proj)
	var worst verdict
	first := true
	for _, p := range projectOptions(pe) {
		v := evalExpr(parseLicenseExpr(src), classifyProject(p))
		if first || baseRank[v.status] > baseRank[worst.status] {
			worst, first = v, false
		}
	}
	return worst
}

func projectOptions(e *licExpr) []string {
	var out []string
	for _, l := range e.leaves() {
		out = append(out, l.id)
	}
	if len(out) == 0 {
		out = []string{LicenseNone}
	}
	return out
}

func evalExpr(e *licExpr, p projectKind) verdict {
	switch e.op {
	case "OR":
		var best verdict
		for i, k := range e.kids {
			v := evalExpr(k, p)
			if i == 0 || baseRank[v.status] < baseRank[best.status] {
				best = v
			}
		}
		return best
	case "AND":
		var worst verdict
		var reasons []string
		for i, k := range e.kids {
			v := evalExpr(k, p)
			if i == 0 || baseRank[v.status] > baseRank[worst.status] {
				worst = v
			}
			reasons = append(reasons, v.reason)
		}
		if worst.status == baseAttribution {
			worst.reason = strings.Join(dedupe(reasons), " ")
		}
		return worst
	}
	v := evalLeaf(lookupLicense(e.id), p)
	if e.with != "" && v.status != baseOK {
		v.reason += " (" + e.with + " applies: read its terms too.)"
	}
	return v
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// article returns "a" or "an" for a licence name as it is read aloud:
// "an MIT project", "a GPL-3.0 project", "an Apache-2.0 project".
func article(name string) string {
	if name == "" || name == "proprietary" {
		return "a"
	}
	if strings.ContainsRune("AEFHILMNORSXaeiou", rune(name[0])) {
		return "an"
	}
	return "a"
}

func evalLeaf(src License, p projectKind) verdict {
	id := src.ID
	proj := p.id
	keep := func() verdict {
		return verdict{baseAttribution, "Keep its copyright line and the " + id + " licence text with your copy, for example in a third-party notices file."}
	}
	allowedIn := func() verdict {
		return verdict{baseAttribution, "Allowed in " + article(proj) + " " + proj + " project. Keep its copyright line and " + id + " licence notice."}
	}
	cantMix := func() verdict {
		return verdict{baseConflict, id + " code and " + proj + " code can't be combined in one program."}
	}
	switch src.Cat {
	case CatPublicDomain:
		return verdict{baseOK, id + " asks for nothing in return: no conditions to meet."}
	case CatNone:
		return verdict{baseNoLicense, "No licence was found, so nobody has given permission to copy this code. Remove it or ask the author."}
	case CatUnknown:
		if id == LicenseNoAssertion {
			return verdict{baseReview, "No licence information was found for this source. Check its repository before keeping the copy."}
		}
		if id == LicenseUnrecognised {
			return verdict{baseReview, "The source has a licence text Remixware doesn't recognise. Read it before keeping the copy."}
		}
		return verdict{baseReview, "The licence (" + id + ") wasn't recognised. Read it before keeping the copy."}
	case CatSourceAvailable:
		if strings.EqualFold(id, proj) {
			return verdict{baseReview, id + " is not an open source licence. Your project uses it too: check its terms allow this copy."}
		}
		return verdict{baseConflict, id + " is not an open source licence: copying this code needs the owner's permission."}
	case CatPermissive:
		if (id == "Apache-2.0" || id == "Apache-1.1") && (p.family == "gpl2only" || p.family == "lgpl2only") {
			return verdict{baseConflict, "Apache-2.0 code can't go into " + article(proj) + " " + proj + " project: the two licences conflict over patents."}
		}
		if id == "BSD-4-Clause" && (gplFamily(p.family) || strings.HasPrefix(p.family, "lgpl")) {
			return verdict{baseConflict, "BSD-4-Clause's advertising clause can't be combined with the " + proj + "."}
		}
		switch id {
		case "Apache-2.0":
			return verdict{baseAttribution, "Keep its copyright line, the Apache-2.0 licence text and any NOTICE text the project ships, and mark files you changed."}
		case "Zlib":
			return verdict{baseAttribution, "Don't claim you wrote it, mark it as changed if you changed it, and keep the zlib notice in the source."}
		case "BSD-4-Clause":
			return verdict{baseAttribution, "Keep the copyright line and licence text, and credit the authors in advertising as the licence's fourth clause asks."}
		case "CC-BY-4.0":
			return verdict{baseAttribution, "Credit the author, link the CC-BY-4.0 licence and say whether you changed it."}
		}
		return keep()
	case CatWeakCopyleft:
		lgpl := strings.HasPrefix(id, "LGPL-")
		switch {
		case strings.EqualFold(id, proj):
			return allowedIn()
		case lgpl && strings.HasPrefix(id, "LGPL-3.0"):
			switch p.family {
			case "gpl3", "agpl3", "lgpl3":
				return allowedIn()
			case "gpl2plus", "lgpl2plus":
				return verdict{baseReview, id + " code fits only if your project moves to version 3 of its licence."}
			case "gpl2only", "lgpl2only":
				return cantMix()
			}
		case lgpl:
			if gplFamily(p.family) || strings.HasPrefix(p.family, "lgpl") {
				return verdict{baseAttribution, "Allowed: " + id + " code can be used under the " + proj + ". Keep its copyright line and licence notice."}
			}
		case strings.HasPrefix(id, "MPL-2.0"):
			if gplFamily(p.family) || strings.HasPrefix(p.family, "lgpl") {
				return verdict{baseAttribution, "Allowed through MPL-2.0's secondary licence terms, unless the file is marked \"Incompatible With Secondary Licenses\". Keep its notice."}
			}
		case id == "EPL-2.0":
			if gplFamily(p.family) {
				return verdict{baseReview, "EPL-2.0 code fits a GPL project only if its owner named the GPL as a secondary licence. Check the file's notice."}
			}
		case id == "EPL-1.0" || strings.HasPrefix(id, "CDDL") || id == "MPL-1.1":
			if gplFamily(p.family) || strings.HasPrefix(p.family, "lgpl") {
				return cantMix()
			}
		}
		if lgpl {
			return verdict{baseReview, "Copying " + id + " code into your own files makes them a changed version of that library, and your changes fall under " + id + ". Keep it as a separate library, or have it reviewed."}
		}
		return verdict{baseReview, id + " applies file by file: keep the copied code in its own file under " + id + ", keep its notice, and publish your changes to that file."}
	case CatCopyleft:
		switch id {
		case "GPL-1.0-only", "GPL-2.0-only":
			switch p.family {
			case "gpl2only":
				return allowedIn()
			case "gpl2plus":
				return verdict{baseReview, id + " code would make your " + proj + " project GPL-2.0-only as a whole."}
			}
		case "GPL-1.0-or-later", "GPL-2.0-or-later":
			if gplFamily(p.family) {
				return allowedIn()
			}
		case "GPL-3.0-only", "GPL-3.0-or-later":
			switch p.family {
			case "gpl3", "agpl3":
				return allowedIn()
			case "gpl2plus":
				return verdict{baseReview, id + " code fits only if your project moves to GPL-3.0."}
			}
		case "EUPL-1.1", "EUPL-1.2":
			if p.family == "eupl" {
				return allowedIn()
			}
			if gplFamily(p.family) {
				return verdict{baseReview, id + " lists the GPL among its compatible licences, with conditions. Have it reviewed."}
			}
		case "CC-BY-SA-4.0":
			if p.family == "gpl3" || p.family == "agpl3" {
				return allowedIn()
			}
		}
		if strings.EqualFold(id, proj) {
			return allowedIn()
		}
		if gplFamily(p.family) || strings.HasPrefix(p.family, "lgpl") || p.family == "eupl" {
			return cantMix()
		}
		return verdict{baseConflict, id + " code can't go into " + article(proj) + " " + proj + " project unless the whole program is released under " + id + ". Remove it, rewrite it, or relicense."}
	case CatNetworkCopyleft:
		switch p.family {
		case "agpl3":
			return allowedIn()
		case "gpl3":
			return verdict{baseReview, "GPL-3.0 allows combining with " + id + " code, but that part keeps AGPL's network clause: people using the program over a network must be offered its source."}
		}
		return verdict{baseConflict, id + " code can't go into " + article(proj) + " " + proj + " project unless the whole program is released under " + id + ", which also means offering the source to everyone who uses it over a network."}
	}
	return verdict{baseReview, "The licence (" + id + ") needs a look."}
}

// licenseCategory returns the category that governs an expression: the most
// demanding licence that has to be followed.
func licenseCategory(expr string) Category {
	e := parseLicenseExpr(expr)
	return exprCategory(e)
}

func exprCategory(e *licExpr) Category {
	switch e.op {
	case "OR":
		best := Category(100)
		for _, k := range e.kids {
			if c := exprCategory(k); c < best {
				best = c
			}
		}
		return best
	case "AND":
		worst := Category(0)
		for _, k := range e.kids {
			if c := exprCategory(k); c > worst {
				worst = c
			}
		}
		return worst
	}
	return lookupLicense(e.id).Cat
}

// KnownLicenses returns the licences Remixware knows, in catalogue order.
func KnownLicenses() []License { return append([]License(nil), knownLicenses...) }

// Verdict returns, in a few words, what copying code under src into a
// project under proj means: no conditions, keep the notice, needs a look,
// conflict or no permission.
func Verdict(src, proj string) string {
	switch evaluate(src, proj).status {
	case baseOK:
		return "no conditions"
	case baseAttribution:
		return "keep the notice"
	case baseReview:
		return "needs a look"
	case baseNoLicense:
		return "no permission"
	}
	return "conflict"
}
