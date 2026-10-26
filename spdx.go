package remixware

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type spdxDoc struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreation       `json:"creationInfo"`
	Files             []spdxFile         `json:"files"`
	Snippets          []spdxSnippet      `json:"snippets"`
	Relationships     []spdxRelationship `json:"relationships"`
	Extracted         []spdxExtracted    `json:"hasExtractedLicensingInfos,omitempty"`
}

type spdxCreation struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxChecksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"checksumValue"`
}

type spdxFile struct {
	SPDXID           string         `json:"SPDXID"`
	FileName         string         `json:"fileName"`
	Checksums        []spdxChecksum `json:"checksums"`
	LicenseConcluded string         `json:"licenseConcluded"`
	CopyrightText    string         `json:"copyrightText"`
}

type spdxPointer struct {
	Reference  string `json:"reference"`
	LineNumber int    `json:"lineNumber"`
}

type spdxRange struct {
	Start spdxPointer `json:"startPointer"`
	End   spdxPointer `json:"endPointer"`
}

type spdxSnippet struct {
	SPDXID           string      `json:"SPDXID"`
	Name             string      `json:"name"`
	SnippetFromFile  string      `json:"snippetFromFile"`
	Ranges           []spdxRange `json:"ranges"`
	LicenseConcluded string      `json:"licenseConcluded"`
	LicenseInfo      []string    `json:"licenseInfoInSnippets"`
	CopyrightText    string      `json:"copyrightText"`
	Comment          string      `json:"comment"`
}

type spdxRelationship struct {
	Element string `json:"spdxElementId"`
	Type    string `json:"relationshipType"`
	Related string `json:"relatedSpdxElement"`
}

type spdxExtracted struct {
	LicenseID string `json:"licenseId"`
	Text      string `json:"extractedText"`
	Name      string `json:"name"`
}

var spdxIDChars = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// WriteSPDX writes the findings as an SPDX 2.3 JSON document: one file
// entry for each project file with copied code, and one snippet entry,
// with its licence and copyright, for each copied stretch.
func WriteSPDX(w io.Writer, res *Results) error {
	name := filepath.Base(res.Project)
	doc := spdxDoc{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name:              name + " copied code",
		DocumentNamespace: "https://remixware.com/spdx/" + spdxIDChars.ReplaceAllString(name, "-") + "-" + docHash(res),
		CreationInfo:      spdxCreation{Created: res.Created, Creators: []string{"Tool: remixware-" + Version}},
	}
	fileIDs := map[string]string{}
	refs := map[string]bool{}
	for i, f := range res.Findings {
		fid, ok := fileIDs[f.File]
		if !ok {
			fid = fmt.Sprintf("SPDXRef-File-%d", len(fileIDs)+1)
			fileIDs[f.File] = fid
			sum := sha1.New()
			if data, err := os.ReadFile(filepath.Join(res.Project, filepath.FromSlash(f.File))); err == nil {
				sum.Write(data)
			}
			doc.Files = append(doc.Files, spdxFile{SPDXID: fid, FileName: "./" + f.File,
				Checksums:        []spdxChecksum{{"SHA1", hex.EncodeToString(sum.Sum(nil))}},
				LicenseConcluded: "NOASSERTION", CopyrightText: "NOASSERTION"})
			doc.Relationships = append(doc.Relationships, spdxRelationship{"SPDXRef-DOCUMENT", "DESCRIBES", fid})
		}
		lic := spdxLicense(f.Source.License)
		for _, l := range parseLicenseExpr(lic).leaves() {
			if strings.HasPrefix(l.id, "LicenseRef-") {
				refs[l.id] = true
			}
		}
		copyright := "NOASSERTION"
		if len(f.Source.Copyrights) > 0 {
			copyright = strings.Join(f.Source.Copyrights, "\n")
		}
		comment := fmt.Sprintf("Matches %s", f.Source.label())
		if f.Source.File != "" {
			comment += ", " + f.Source.File
			if f.Source.Lines.Start > 0 {
				comment += " lines " + f.Source.Lines.String()
			}
		}
		comment += fmt.Sprintf(" (%s). Remixware status: %s.", kindText(f.Match), f.Status)
		doc.Snippets = append(doc.Snippets, spdxSnippet{
			SPDXID: fmt.Sprintf("SPDXRef-Snippet-%d", i+1), Name: "from " + f.Source.Key, SnippetFromFile: fid,
			Ranges:           []spdxRange{{spdxPointer{fid, f.Lines.Start}, spdxPointer{fid, f.Lines.End}}},
			LicenseConcluded: lic, LicenseInfo: []string{lic}, CopyrightText: copyright, Comment: comment,
		})
		doc.Relationships = append(doc.Relationships, spdxRelationship{fid, "CONTAINS", fmt.Sprintf("SPDXRef-Snippet-%d", i+1)})
	}
	var ids []string
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		doc.Extracted = append(doc.Extracted, spdxExtracted{LicenseID: id, Name: id,
			Text: "Licence named " + id + " by Remixware. See the source of the copied code for its text."})
	}
	if doc.Files == nil {
		doc.Files = []spdxFile{}
		doc.Snippets = []spdxSnippet{}
		doc.Relationships = []spdxRelationship{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

// spdxLicense makes an expression valid in an SPDX document: unknown ids
// become LicenseRef- ids.
func spdxLicense(expr string) string {
	e := parseLicenseExpr(expr)
	var fix func(e *licExpr)
	fix = func(e *licExpr) {
		if e.op == "" {
			l := lookupLicense(e.id)
			switch {
			case e.id == LicenseNone || e.id == LicenseNoAssertion:
			case l.Cat == CatUnknown && !strings.HasPrefix(e.id, "LicenseRef-"):
				e.id = "LicenseRef-" + spdxIDChars.ReplaceAllString(e.id, "-")
			}
			if e.with != "" {
				e.with = spdxIDChars.ReplaceAllString(e.with, "-")
			}
			return
		}
		for _, k := range e.kids {
			fix(k)
		}
	}
	fix(e)
	return e.String()
}

func docHash(res *Results) string {
	h := sha1.New()
	h.Write([]byte(res.Project + res.Created))
	for _, f := range res.Findings {
		h.Write([]byte(f.ID))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
