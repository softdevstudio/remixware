// Command bench measures Remixware on real open source code.
//
// It clones the projects in corpus.json at pinned versions into a cache
// folder (never into this repository), indexes the "indexed" ones, and
// builds a test project of copied functions: verbatim, with the layout
// changed, with comments changed, with names changed, partly copied, and
// with all of those at once. Go functions are rewritten through go/ast;
// Python, JavaScript and C functions are changed as text. It then scans
// that project and counts which copies were found and traced to the right
// source, scans the "unrelated" projects against the same index to count
// false alarms, and times it all. Results go to test/results.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/softdevstudio/remixware"
)

type repo struct {
	Name, URL, Ref, License, Lang string
	Why                           string // why the expected licence is what it is, when that isn't obvious
}

type corpusFile struct {
	Note      string
	Indexed   []repo
	Unrelated []repo
}

type sample struct {
	ID      string
	Lang    string
	Variant string
	Bucket  string
	Tokens  int
	File    string // path in the test project
	Origin  string // absolute path of the original file
	Range   remixware.Range
	Repo    string
	Lines   int
}

var variants = []string{"verbatim", "layout", "comments", "renamed", "partial", "combined"}

func main() {
	home, _ := os.UserHomeDir()
	cache := flag.String("cache", filepath.Join(home, ".cache", "remixware-bench"), "folder for cloned projects and the test project")
	out := flag.String("out", "test/results", "folder for the result files")
	goN := flag.Int("go", 300, "Go functions to copy")
	otherN := flag.Int("other", 60, "functions to copy per other language")
	seed := flag.Int64("seed", 7, "random seed for picking functions")
	minTokens := flag.Int("min-tokens", 0, "scan setting (0: Remixware's default)")
	minLines := flag.Int("min-lines", 0, "scan setting (0: Remixware's default)")
	corpusPath := flag.String("corpus", "scripts/bench/corpus.json", "corpus definition")
	flag.Parse()

	var c corpusFile
	data, err := os.ReadFile(*corpusPath)
	check(err)
	check(json.Unmarshal(data, &c))
	check(os.MkdirAll(*out, 0o755))

	// 1. Fetch.
	for _, r := range c.Indexed {
		fetch(r, filepath.Join(*cache, "indexed", r.Name))
	}
	for _, r := range c.Unrelated {
		fetch(r, filepath.Join(*cache, "unrelated", r.Name))
	}

	// 2. Index.
	var roots []string
	for _, r := range c.Indexed {
		roots = append(roots, filepath.Join(*cache, "indexed", r.Name))
	}
	t0 := time.Now()
	ix, err := remixware.BuildIndex(roots, remixware.IndexOptions{})
	check(err)
	indexTime := time.Since(t0)
	ixPath := filepath.Join(*cache, "corpus.rmx")
	check(ix.Save(ixPath))
	st, _ := os.Stat(ixPath)
	ix, err = remixware.LoadIndex(ixPath)
	check(err)

	var corpusTxt bytes.Buffer
	fmt.Fprintf(&corpusTxt, "Indexed projects, cloned at these versions. Licence: expected / found by remixware index.\n\n")
	licOK := 0
	for _, r := range c.Indexed {
		root, _ := filepath.Abs(filepath.Join(*cache, "indexed", r.Name))
		found := "(no component)"
		files, lines := 0, 0
		for ci, comp := range ix.Components {
			if comp.Root == root {
				found = comp.License
			}
			if within(comp.Root, root) {
				for _, f := range ix.Files {
					if f.Comp == ci {
						files++
						lines += f.Lines
					}
				}
			}
		}
		mark := "ok"
		if found == r.License {
			licOK++
		} else {
			mark = "DIFFERENT"
		}
		fmt.Fprintf(&corpusTxt, "%-12s %-8s %-41s %-6s %-14s / %-14s %s  %d files, %d lines\n", r.Name, r.Ref, r.URL, r.Lang, r.License, found, mark, files, lines)
		if r.Why != "" {
			fmt.Fprintf(&corpusTxt, "%-12s (%s)\n", "", r.Why)
		}
	}
	fmt.Fprintf(&corpusTxt, "\nLicences found as expected: %d of %d\n", licOK, len(c.Indexed))
	fmt.Fprintf(&corpusTxt, "Index: %d components, %d files, %d lines, %d fingerprints, %d bytes\n", len(ix.Components), len(ix.Files), ix.Lines, ix.Fingerprints(), st.Size())
	fmt.Fprintf(&corpusTxt, "\nComponents found inside the projects:\n")
	for _, comp := range ix.Components {
		n := 0
		for _, f := range ix.Files {
			if f.Comp >= 0 && ix.Components[f.Comp] == comp {
				n++
			}
		}
		rel, _ := filepath.Rel(filepath.Join(*cache, "indexed"), comp.Root)
		fmt.Fprintf(&corpusTxt, "  %-45s %-20s %4d files  (%s)\n", comp.Key, comp.License, n, filepath.ToSlash(rel))
	}
	write(*out, "corpus.txt", corpusTxt.String())
	repoComponent := map[string]string{}
	for _, r := range c.Indexed {
		root, _ := filepath.Abs(filepath.Join(*cache, "indexed", r.Name))
		for _, comp := range ix.Components {
			if comp.Root == root {
				repoComponent[r.Name] = comp.Name
			}
		}
	}

	// 3. Build the test project of copies.
	rng := rand.New(rand.NewSource(*seed))
	project := filepath.Join(*cache, "project")
	check(os.RemoveAll(project))
	var samples []sample
	samples = append(samples, goSamples(c, *cache, project, *goN, rng)...)
	for _, lang := range []string{"python", "js", "c"} {
		samples = append(samples, textSamples(c, *cache, project, lang, *otherN, rng)...)
	}

	// 4. Scan it.
	t1 := time.Now()
	res, err := remixware.Scan(project, remixware.ScanOptions{Indexes: []*remixware.Index{ix}, MinTokens: *minTokens, MinLines: *minLines})
	check(err)
	scanTime := time.Since(t1)
	byFile := map[string][]remixware.Finding{}
	for _, f := range res.Findings {
		byFile[f.File] = append(byFile[f.File], f)
	}

	type tally struct{ found, total, wrong int }
	byLangVariant := map[string]*tally{}
	byBucket := map[string]*tally{}
	byTokens := map[string]*tally{}
	var misses, wrongs bytes.Buffer
	for _, s := range samples {
		k := s.Lang + "/" + s.Variant
		if byLangVariant[k] == nil {
			byLangVariant[k] = &tally{}
		}
		t := byLangVariant[k]
		t.total++
		if s.Lang == "go" {
			bk := s.Variant + "/" + s.Bucket
			if byBucket[bk] == nil {
				byBucket[bk] = &tally{}
			}
			byBucket[bk].total++
		}
		ok, other := traced(byFile[s.File], s, *cache, repoComponent)
		tk := s.Variant + "/" + tokenBucket(s.Tokens)
		if byTokens[tk] == nil {
			byTokens[tk] = &tally{}
		}
		byTokens[tk].total++
		if ok {
			byTokens[tk].found++
		}
		switch {
		case ok:
			t.found++
			if s.Lang == "go" {
				byBucket[s.Variant+"/"+s.Bucket].found++
			}
		default:
			fmt.Fprintf(&misses, "%s %-6s %-8s %3d lines %4d tokens  %s:%s\n", s.ID, s.Lang, s.Variant, s.Lines, s.Tokens, rel(*cache, s.Origin), s.Range)
		}
		if other {
			t.wrong++
			for _, f := range byFile[s.File] {
				fmt.Fprintf(&wrongs, "%s %s %s: matched %s %s:%s (%s)\n", s.ID, s.Lang, s.Variant, f.Source.Component, f.Source.File, f.Source.Lines, f.Match.Kind)
			}
		}
	}

	var recall bytes.Buffer
	fmt.Fprintf(&recall, "Copies found and traced to the function they came from.\n")
	fmt.Fprintf(&recall, "Scan settings: min tokens %d, min lines %d.\n\n", res.MinTokens, res.MinLines)
	fmt.Fprintf(&recall, "%-8s", "")
	for _, v := range variants {
		fmt.Fprintf(&recall, "%14s", v)
	}
	fmt.Fprintf(&recall, "%14s\n", "all")
	summary := map[string]any{}
	allFound, allTotal := 0, 0
	for _, lang := range []string{"go", "python", "js", "c"} {
		fmt.Fprintf(&recall, "%-8s", lang)
		lf, lt := 0, 0
		for _, v := range variants {
			t := byLangVariant[lang+"/"+v]
			if t == nil || t.total == 0 {
				fmt.Fprintf(&recall, "%14s", "-")
				continue
			}
			fmt.Fprintf(&recall, "%14s", fmt.Sprintf("%d/%d %3.0f%%", t.found, t.total, pct(t.found, t.total)))
			lf += t.found
			lt += t.total
			summary["recall_"+lang+"_"+v] = round(pct(t.found, t.total))
		}
		fmt.Fprintf(&recall, "%14s\n", fmt.Sprintf("%d/%d %3.0f%%", lf, lt, pct(lf, lt)))
		allFound += lf
		allTotal += lt
		summary["recall_"+lang] = round(pct(lf, lt))
	}
	fmt.Fprintf(&recall, "\nAll copies: %d of %d found (%.1f%%).\n", allFound, allTotal, pct(allFound, allTotal))
	summary["recall_all"] = round(pct(allFound, allTotal))
	summary["copies"] = allTotal
	summary["copies_found"] = allFound
	fmt.Fprintf(&recall, "\nGo, by length of the original function:\n%-10s", "")
	for _, b := range []string{"8-14", "15-29", "30+"} {
		fmt.Fprintf(&recall, "%16s", b+" lines")
	}
	fmt.Fprintln(&recall)
	for _, v := range variants {
		fmt.Fprintf(&recall, "%-10s", v)
		for _, b := range []string{"8-14", "15-29", "30+"} {
			t := byBucket[v+"/"+b]
			if t == nil || t.total == 0 {
				fmt.Fprintf(&recall, "%16s", "-")
				continue
			}
			fmt.Fprintf(&recall, "%16s", fmt.Sprintf("%d/%d %3.0f%%", t.found, t.total, pct(t.found, t.total)))
			summary["recall_go_"+v+"_"+b] = round(pct(t.found, t.total))
		}
		fmt.Fprintln(&recall)
	}
	fmt.Fprintf(&recall, "\nAll languages, by size of the copy in tokens (what Remixware measures):\n%-10s", "")
	for _, b := range tokenBuckets {
		fmt.Fprintf(&recall, "%16s", b)
	}
	fmt.Fprintln(&recall)
	for _, v := range variants {
		fmt.Fprintf(&recall, "%-10s", v)
		for _, b := range tokenBuckets {
			t := byTokens[v+"/"+b]
			if t == nil || t.total == 0 {
				fmt.Fprintf(&recall, "%16s", "-")
				continue
			}
			fmt.Fprintf(&recall, "%16s", fmt.Sprintf("%d/%d %3.0f%%", t.found, t.total, pct(t.found, t.total)))
			summary["recall_tokens_"+v+"_"+b] = round(pct(t.found, t.total))
		}
		fmt.Fprintln(&recall)
	}
	fmt.Fprintf(&recall, "%-10s", "all kinds")
	bigFound, bigTotal := 0, 0
	for _, b := range tokenBuckets {
		found, total := 0, 0
		for _, v := range variants {
			if t := byTokens[v+"/"+b]; t != nil {
				found += t.found
				total += t.total
			}
		}
		if b == "100-199" || b == "200+" {
			bigFound += found
			bigTotal += total
		}
		if total == 0 {
			fmt.Fprintf(&recall, "%16s", "-")
			continue
		}
		fmt.Fprintf(&recall, "%16s", fmt.Sprintf("%d/%d %3.0f%%", found, total, pct(found, total)))
		summary["recall_tokens_all_"+b] = round(pct(found, total))
	}
	fmt.Fprintln(&recall)
	fmt.Fprintf(&recall, "\nCopies of 100 tokens or more, all kinds of change: %d of %d found (%.1f%%).\n", bigFound, bigTotal, pct(bigFound, bigTotal))
	summary["copies_100plus"] = bigTotal
	summary["copies_100plus_found"] = bigFound
	summary["recall_100plus"] = round(pct(bigFound, bigTotal))
	wrongTotal := 0
	for _, t := range byLangVariant {
		wrongTotal += t.wrong
	}
	fmt.Fprintf(&recall, "\nCopies that were also matched to a different project: %d\n", wrongTotal)
	summary["wrong_source"] = wrongTotal
	write(*out, "recall.txt", recall.String())
	write(*out, "misses.txt", "Copies Remixware did not trace to their source.\n\n"+misses.String())
	write(*out, "wrong-source.txt", "Copies that were also matched to a different project than the one they came from.\n\n"+wrongs.String())

	// 5. False alarms: scan projects that are not in the index.
	var fa bytes.Buffer
	fmt.Fprintf(&fa, "Projects that are not in the index, scanned against it. Every match is listed for review.\n\n")
	faLines, faFiles, faCount := 0, 0, 0
	var faTime time.Duration
	for _, r := range c.Unrelated {
		dir := filepath.Join(*cache, "unrelated", r.Name)
		t := time.Now()
		ur, err := remixware.Scan(dir, remixware.ScanOptions{Indexes: []*remixware.Index{ix}, MinTokens: *minTokens, MinLines: *minLines})
		check(err)
		faTime += time.Since(t)
		faLines += ur.LinesScanned
		faFiles += ur.FilesScanned
		faCount += len(ur.Findings)
		fmt.Fprintf(&fa, "%-11s %-8s %5d files %7d lines  %d matches\n", r.Name, short(r.Ref), ur.FilesScanned, ur.LinesScanned, len(ur.Findings))
		for _, f := range ur.Findings {
			fmt.Fprintf(&fa, "    %s:%s matches %s %s:%s (%s, %.0f%%, %d tokens)\n", f.File, f.Lines, f.Source.Component, f.Source.File, f.Source.Lines, f.Match.Kind, f.Match.Coverage*100, f.Match.Tokens)
			fmt.Fprintf(&fa, "%s", excerpt(filepath.Join(dir, f.File), f.Lines, "      | "))
			fmt.Fprintf(&fa, "%s", excerpt(f.Source.Abs, f.Source.Lines, "      > "))
		}
	}
	fmt.Fprintf(&fa, "\nTotal: %d files, %d lines scanned, %d matches.\n", faFiles, faLines, faCount)
	write(*out, "false-alarms.txt", fa.String())
	summary["unrelated_lines"] = faLines
	summary["unrelated_files"] = faFiles
	summary["unrelated_matches"] = faCount

	// 6. Speed.
	var sp bytes.Buffer
	fmt.Fprintf(&sp, "Index: %d files, %d lines in %s (%.0f lines/s)\n", len(ix.Files), ix.Lines, indexTime.Round(time.Millisecond), float64(ix.Lines)/indexTime.Seconds())
	fmt.Fprintf(&sp, "Scan of the copies: %d files, %d lines in %s (%.0f lines/s)\n", res.FilesScanned, res.LinesScanned, scanTime.Round(time.Millisecond), float64(res.LinesScanned)/scanTime.Seconds())
	fmt.Fprintf(&sp, "Scan of the unrelated projects: %d files, %d lines in %s (%.0f lines/s)\n", faFiles, faLines, faTime.Round(time.Millisecond), float64(faLines)/faTime.Seconds())
	fmt.Fprintf(&sp, "Index file: %d bytes for %d lines\n", st.Size(), ix.Lines)
	write(*out, "speed.txt", sp.String())
	summary["index_lines"] = ix.Lines
	summary["index_files"] = len(ix.Files)
	summary["index_seconds"] = round(indexTime.Seconds())
	summary["index_lines_per_second"] = int(float64(ix.Lines) / indexTime.Seconds())
	summary["scan_lines_per_second"] = int(float64(faLines) / faTime.Seconds())
	summary["index_bytes"] = st.Size()
	summary["licenses_detected"] = licOK
	summary["licenses_total"] = len(c.Indexed)
	summary["min_tokens"] = res.MinTokens
	summary["min_lines"] = res.MinLines
	js, _ := json.MarshalIndent(summary, "", "  ")
	write(*out, "summary.json", string(js)+"\n")
	fmt.Print(recall.String())
	fmt.Printf("\nunrelated: %d matches in %d lines\n", faCount, faLines)
	fmt.Print(sp.String())
}

// traced reports whether the findings on a copy point back to the project
// it came from, and whether any of them point to a different project. A
// match to a duplicate of the function elsewhere in the same project counts
// as traced: it is the same code under the same licence.
func traced(fs []remixware.Finding, s sample, cache string, names map[string]string) (ok, other bool) {
	root := filepath.Join(cache, "indexed", s.Repo) + string(filepath.Separator)
	abs, _ := filepath.Abs(root)
	root = abs + string(filepath.Separator)
	for _, f := range fs {
		hit := false
		for _, src := range append([]remixware.Source{f.Source}, f.Also...) {
			if src.Abs == s.Origin && src.Lines.Start <= s.Range.End && src.Lines.End >= s.Range.Start {
				hit = true
			}
			if strings.HasPrefix(src.Abs, root) || src.Component == names[s.Repo] {
				hit = true
			}
		}
		if hit {
			ok = true
		} else {
			other = true
		}
	}
	return
}

func fetch(r repo, dir string) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return
	}
	check(os.MkdirAll(filepath.Dir(dir), 0o755))
	fmt.Fprintf(os.Stderr, "cloning %s %s\n", r.URL, r.Ref)
	if len(r.Ref) == 40 && strings.Trim(r.Ref, "0123456789abcdef") == "" {
		check(os.MkdirAll(dir, 0o755))
		git(dir, "init", "-q")
		git(dir, "remote", "add", "origin", r.URL)
		git(dir, "fetch", "-q", "--depth", "1", "origin", r.Ref)
		git(dir, "checkout", "-q", "FETCH_HEAD")
		return
	}
	git("", "clone", "-q", "--depth", "1", "--branch", r.Ref, r.URL, dir)
}

func git(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "git %s: %v\n%s", strings.Join(args, " "), err, out)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Go copies, rewritten through go/ast.

type goFunc struct {
	repo, path string
	start, end int // byte offsets
	lines      int
	first      int // first line
}

func goSamples(c corpusFile, cache, project string, n int, rng *rand.Rand) []sample {
	var funcs []goFunc
	perRepo := map[string]int{}
	for _, r := range c.Indexed {
		if r.Lang != "go" {
			continue
		}
		root := filepath.Join(cache, "indexed", r.Name)
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() && (info.Name() == "vendor" || info.Name() == "testdata" || info.Name() == ".git") {
				return filepath.SkipDir
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil || bytes.Contains(src[:min(len(src), 2048)], []byte("DO NOT EDIT")) {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
			if err != nil {
				return nil
			}
			abs, _ := filepath.Abs(path)
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				a, b := fset.Position(fd.Pos()), fset.Position(fd.End())
				lines := b.Line - a.Line + 1
				if lines < 8 || lines > 150 {
					continue
				}
				funcs = append(funcs, goFunc{repo: r.Name, path: abs, start: a.Offset, end: b.Offset, lines: lines, first: a.Line})
			}
			return nil
		})
	}
	sort.Slice(funcs, func(i, j int) bool {
		if funcs[i].path != funcs[j].path {
			return funcs[i].path < funcs[j].path
		}
		return funcs[i].start < funcs[j].start
	})
	rng.Shuffle(len(funcs), func(i, j int) { funcs[i], funcs[j] = funcs[j], funcs[i] })
	var out []sample
	limit := n/4 + 1
	count := 0
	for _, fn := range funcs {
		if count >= n {
			break
		}
		if perRepo[fn.repo] >= limit {
			continue
		}
		perRepo[fn.repo]++
		count++
		id := fmt.Sprintf("g%04d", count)
		src, _ := os.ReadFile(fn.path)
		orig := string(src[fn.start:fn.end])
		full := remixware.Range{Start: fn.first, End: fn.first + fn.lines - 1}
		texts := map[string]string{
			"verbatim": orig,
			"layout":   relayout(orig),
			"comments": newComments(goPrint(fn, src, false, false, false), "//", rng),
			"renamed":  goPrint(fn, src, true, true, false),
			"combined": newComments(goPrint(fn, src, true, false, true), "//", rng),
		}
		pText, pRange := partial(orig, full)
		texts["partial"] = pText
		for _, v := range variants {
			file := filepath.Join("go", v, id+".go")
			body := "package sample\n\n" + texts[v] + "\n"
			writeFile(filepath.Join(project, file), body)
			r := full
			if v == "partial" {
				r = pRange
			}
			out = append(out, sample{ID: id, Lang: "go", Variant: v, Bucket: bucket(fn.lines), File: filepath.ToSlash(file),
				Origin: fn.path, Range: r, Repo: fn.repo, Lines: fn.lines, Tokens: remixware.TokenCount("x.go", []byte(texts[v]))})
		}
	}
	return out
}

var goNames = []string{"item", "value", "count", "buf", "next", "cur", "acc", "node", "entry", "tmp", "res", "data",
	"key", "idx", "total", "out", "src", "dst", "pos", "state", "opts", "cfg", "elem", "part", "chunk", "field", "msg",
	"ctx2", "handle", "target"}

// goPrint parses the file again and prints one function, optionally with
// its own names (locals, parameters, results, the function itself)
// consistently renamed, with comments, and with spaces instead of tabs.
func goPrint(fn goFunc, src []byte, rename, keepComments, spaces bool) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fn.path, src, parser.ParseComments)
	check(err)
	var fd *ast.FuncDecl
	for _, d := range f.Decls {
		if x, ok := d.(*ast.FuncDecl); ok && fset.Position(x.Pos()).Offset == fn.start {
			fd = x
		}
	}
	if fd == nil {
		panic("function not found again")
	}
	if rename {
		objs := map[*ast.Object]string{}
		ast.Inspect(fd, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Obj == nil || id.Name == "_" {
				return true
			}
			p := id.Obj.Pos()
			if p >= fd.Pos() && p <= fd.End() && (id.Obj.Kind == ast.Var || id.Obj.Kind == ast.Con || id.Obj.Kind == ast.Lbl) {
				if _, seen := objs[id.Obj]; !seen {
					objs[id.Obj] = fmt.Sprintf("%s%d", goNames[len(objs)%len(goNames)], len(objs)/len(goNames)+1)
				}
			}
			return true
		})
		ast.Inspect(fd, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Obj != nil {
				if nn, ok := objs[id.Obj]; ok {
					id.Name = nn
				}
			}
			return true
		})
		fd.Name.Name = "copied" + strings.ToUpper(fd.Name.Name[:1]) + fd.Name.Name[1:]
	}
	cfg := printer.Config{Mode: printer.TabIndent | printer.UseSpaces, Tabwidth: 8}
	if spaces {
		cfg = printer.Config{Mode: printer.UseSpaces, Tabwidth: 3}
	}
	var buf bytes.Buffer
	var node any = fd
	if keepComments {
		node = &printer.CommentedNode{Node: fd, Comments: f.Comments}
	}
	check(cfg.Fprint(&buf, fset, node))
	return buf.String()
}

// ---------------------------------------------------------------------------
// Python, JavaScript and C copies, changed as text.

var (
	pyDef    = regexp.MustCompile(`^(\s*)(async\s+)?def\s+[A-Za-z_]\w*\s*\(`)
	jsFunc   = regexp.MustCompile(`\bfunction\b[^;]*\{\s*$`)
	cFunc    = regexp.MustCompile(`^[A-Za-z_][\w \t\*,]*\([^;]*$`)
	identRE  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	keepWord = map[string]map[string]bool{
		"python": words(`False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield self cls`),
		"js":     words(`break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof let new return super switch this throw try typeof var void while with yield async await of null undefined true false`),
		"c":      words(`auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while NULL size_t uInt uLong Bytef voidpf cJSON_bool`),
	}
)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

type textFunc struct {
	repo, path  string
	first, last int // lines, 1-based
	text        string
}

func textSamples(c corpusFile, cache, project, lang string, n int, rng *rand.Rand) []sample {
	ext := map[string]string{"python": ".py", "js": ".js", "c": ".c"}[lang]
	var funcs []textFunc
	for _, r := range c.Indexed {
		if r.Lang != lang {
			continue
		}
		root := filepath.Join(cache, "indexed", r.Name)
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				switch info.Name() {
				case ".git", "test", "tests", "examples", "docs", "node_modules", "contrib", "fuzzing":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ext) {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			abs, _ := filepath.Abs(path)
			for _, fn := range findTextFuncs(lang, strings.Split(string(src), "\n")) {
				fn.repo, fn.path = r.Name, abs
				funcs = append(funcs, fn)
			}
			return nil
		})
	}
	sort.Slice(funcs, func(i, j int) bool {
		if funcs[i].path != funcs[j].path {
			return funcs[i].path < funcs[j].path
		}
		return funcs[i].first < funcs[j].first
	})
	rng.Shuffle(len(funcs), func(i, j int) { funcs[i], funcs[j] = funcs[j], funcs[i] })
	if len(funcs) > n {
		funcs = funcs[:n]
	}
	comment := map[string]string{"python": "#", "js": "//", "c": "//"}[lang]
	var out []sample
	for i, fn := range funcs {
		id := fmt.Sprintf("%c%04d", lang[0], i+1)
		full := remixware.Range{Start: fn.first, End: fn.last}
		lines := fn.last - fn.first + 1
		texts := map[string]string{
			"verbatim": fn.text,
			"layout":   relayout(fn.text),
			"comments": newComments(stripComments(fn.text, lang), comment, rng),
			"renamed":  renameText(fn.text, lang),
			"combined": newComments(relayout(stripComments(renameText(fn.text, lang), lang)), comment, rng),
		}
		pText, pRange := partial(fn.text, full)
		texts["partial"] = pText
		for _, v := range variants {
			file := filepath.Join(lang, v, id+ext)
			writeFile(filepath.Join(project, file), texts[v]+"\n")
			r := full
			if v == "partial" {
				r = pRange
			}
			out = append(out, sample{ID: id, Lang: lang, Variant: v, Bucket: bucket(lines), File: filepath.ToSlash(file),
				Origin: fn.path, Range: r, Repo: fn.repo, Lines: lines, Tokens: remixware.TokenCount("x"+ext, []byte(texts[v]))})
		}
	}
	return out
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

func findTextFuncs(lang string, lines []string) []textFunc {
	var out []textFunc
	for i := 0; i < len(lines); i++ {
		end := -1
		switch lang {
		case "python":
			m := pyDef.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			ind := len(m[1])
			end = i
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t == "" {
					continue
				}
				if indentOf(lines[j]) <= ind {
					break
				}
				end = j
			}
		case "js", "c":
			if lang == "js" && !jsFunc.MatchString(lines[i]) {
				continue
			}
			open := i
			if lang == "c" {
				if !cFunc.MatchString(lines[i]) || strings.HasPrefix(lines[i], "static ") && strings.HasSuffix(strings.TrimSpace(lines[i]), ";") {
					continue
				}
				// The signature may run over a few lines; the body opens with
				// a { at the end of a line or alone on the next line.
				for open = i; open < len(lines) && open < i+6 && !strings.HasSuffix(strings.TrimSpace(lines[open]), "{") && !strings.HasSuffix(strings.TrimSpace(lines[open]), ";"); open++ {
				}
				if open >= len(lines) || !strings.HasSuffix(strings.TrimSpace(lines[open]), "{") {
					continue
				}
			}
			depth := 0
			for j := open; j < len(lines); j++ {
				depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
				if depth <= 0 && j > open || (depth == 0 && strings.Contains(lines[j], "}")) {
					end = j
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		n := end - i + 1
		if n >= 8 && n <= 150 {
			out = append(out, textFunc{first: i + 1, last: end + 1, text: strings.Join(lines[i:end+1], "\n")})
		}
		if lang != "python" {
			i = end
		}
	}
	return out
}

// renameText renames, consistently, the names a function declares itself:
// its own name, its parameters and the local variables it assigns. Names
// of types, modules, fields and called functions are left alone, as they
// would be when someone renames a copy by hand or asks a tool to.
func renameText(s, lang string) string {
	declared := declaredNames(s, lang)
	names := map[string]string{}
	var b strings.Builder
	last := 0
	for _, m := range identRE.FindAllStringIndex(s, -1) {
		w := s[m[0]:m[1]]
		b.WriteString(s[last:m[0]])
		last = m[1]
		if !declared[w] || (m[0] > 0 && (s[m[0]-1] == '.' || (s[m[0]-1] >= '0' && s[m[0]-1] <= '9'))) {
			b.WriteString(w)
			continue
		}
		if _, ok := names[w]; !ok {
			names[w] = fmt.Sprintf("%s%d", goNames[len(names)%len(goNames)], len(names)/len(goNames)+1)
		}
		b.WriteString(names[w])
	}
	b.WriteString(s[last:])
	return b.String()
}

var (
	pySig    = regexp.MustCompile(`(?s)^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\((.*?)\)\s*(?:->[^:]*)?:`)
	pyAssign = regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*)\s*(?::[^=\n]*)?=[^=]`)
	pyFor    = regexp.MustCompile(`\bfor\s+([A-Za-z_][\w\s,]*?)\s+in\b`)
	pyAs     = regexp.MustCompile(`\bas\s+([A-Za-z_]\w*)`)
	jsSig    = regexp.MustCompile(`(?s)function\s*([A-Za-z_$][\w$]*)?\s*\((.*?)\)`)
	jsDecl   = regexp.MustCompile(`\b(?:let|const|var)\s+([A-Za-z_$][\w$]*)`)
	cSig     = regexp.MustCompile(`(?s)^[^(]*?([A-Za-z_]\w*)\s*\((.*?)\)\s*\{`)
	cDecl    = regexp.MustCompile(`(?m)^\s*(?:const\s+|static\s+|unsigned\s+|signed\s+|struct\s+|register\s+)*[A-Za-z_]\w*\s*\**\s+\**\s*([A-Za-z_]\w*)\s*(?:=|;|\[|,)`)
	lastName = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*(?:\[\s*\])?\s*$`)
)

func declaredNames(s, lang string) map[string]bool {
	out := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && n != "self" && n != "cls" && !keepWord[lang][n] {
			out[n] = true
		}
	}
	params := func(list string, pick func(string) string) {
		depth := 0
		cur := ""
		for _, r := range list + "," {
			switch {
			case r == '(' || r == '[' || r == '{':
				depth++
			case r == ')' || r == ']' || r == '}':
				depth--
			case r == ',' && depth == 0:
				add(pick(cur))
				cur = ""
				continue
			}
			cur += string(r)
		}
	}
	switch lang {
	case "python":
		if m := pySig.FindStringSubmatch(s); m != nil {
			add(m[1])
			params(m[2], func(p string) string {
				p = strings.TrimLeft(strings.TrimSpace(p), "*")
				return identRE.FindString(p)
			})
		}
		for _, m := range pyAssign.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
		for _, m := range pyFor.FindAllStringSubmatch(s, -1) {
			for _, n := range strings.Split(m[1], ",") {
				add(n)
			}
		}
		for _, m := range pyAs.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
	case "js":
		if m := jsSig.FindStringSubmatch(s); m != nil {
			add(m[1])
			params(m[2], func(p string) string {
				p = strings.TrimSpace(p)
				if i := strings.Index(p, "="); i >= 0 {
					p = p[:i]
				}
				return identRE.FindString(p)
			})
		}
		for _, m := range jsDecl.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
	case "c":
		if m := cSig.FindStringSubmatch(s); m != nil {
			add(m[1])
			params(m[2], func(p string) string {
				if m := lastName.FindStringSubmatch(strings.TrimSpace(p)); m != nil {
					return m[1]
				}
				return ""
			})
		}
		for _, m := range cDecl.FindAllStringSubmatch(s, -1) {
			add(m[1])
		}
	}
	return out
}

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment  = regexp.MustCompile(`(?m)^(\s*)//.*$`)
	pyComment    = regexp.MustCompile(`(?m)^\s*#.*$`)
)

func stripComments(s, lang string) string {
	if lang == "python" {
		return pyComment.ReplaceAllString(s, "")
	}
	s = blockComment.ReplaceAllString(s, "")
	return lineComment.ReplaceAllString(s, "$1")
}

var notes = []string{"check the input first", "main loop", "handle the edge case", "done", "clean up",
	"keep going", "fast path", "build the result", "see above", "TODO tidy"}

// newComments drops a new comment line in every few lines.
func newComments(s, marker string, rng *rand.Rand) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i, l := range lines {
		if i > 0 && i%5 == 0 {
			out = append(out, strings.Repeat(" ", indentOf(l))+marker+" "+notes[rng.Intn(len(notes))])
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// relayout swaps tabs for two spaces, halves four-space indents, and adds a
// blank line after every line that opens a block.
func relayout(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		ind := l[:indentOf(l)]
		rest := l[len(ind):]
		ind = strings.ReplaceAll(ind, "\t", "  ")
		ind = strings.ReplaceAll(ind, "    ", "  ")
		out = append(out, ind+rest)
		t := strings.TrimSpace(rest)
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, ":") {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

// partial keeps the middle of a function: from 20% to 80% of its lines.
func partial(s string, full remixware.Range) (string, remixware.Range) {
	lines := strings.Split(s, "\n")
	n := len(lines)
	a, b := n/5, n*4/5
	if b-a < 6 {
		a, b = max(0, n/2-3), min(n, n/2+3)
	}
	return strings.Join(lines[a:b], "\n"), remixware.Range{Start: full.Start + a, End: full.Start + b - 1}
}

var tokenBuckets = []string{"under 50", "50-99", "100-199", "200+"}

func tokenBucket(n int) string {
	switch {
	case n < 50:
		return "under 50"
	case n < 100:
		return "50-99"
	case n < 200:
		return "100-199"
	}
	return "200+"
}

func bucket(lines int) string {
	switch {
	case lines < 15:
		return "8-14"
	case lines < 30:
		return "15-29"
	}
	return "30+"
}

// ---------------------------------------------------------------------------

func excerpt(path string, r remixware.Range, prefix string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	for i := r.Start; i <= r.End && i <= len(lines) && i < r.Start+8; i++ {
		fmt.Fprintf(&b, "%s%4d %s\n", prefix, i, strings.ReplaceAll(lines[i-1], "\t", "  "))
	}
	return b.String()
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func rel(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}

func short(ref string) string {
	if len(ref) > 12 {
		return ref[:7]
	}
	return ref
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func round(x float64) float64 { return float64(int(x*10+0.5)) / 10 }

func write(dir, name, text string) {
	check(os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644))
}

func writeFile(path, text string) {
	check(os.MkdirAll(filepath.Dir(path), 0o755))
	check(os.WriteFile(path, []byte(text), 0o644))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}
