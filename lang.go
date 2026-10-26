package remixware

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// syntax describes how one family of languages writes comments, strings and
// identifiers. The tokenizer only needs to know enough to drop comments,
// keep string and number literals whole, and tell keywords from names.
type syntax struct {
	name          string
	lineComments  []string
	blockComments [][2]string
	nestedBlocks  bool // block comments nest (Rust, Swift, Kotlin, Scala)
	tripleQuotes  bool // Python """ and ''' strings
	stringPrefix  bool // Python r"", b"", f"" prefixes
	backtick      bool // ` strings (Go raw strings, JS templates, shell)
	singleQuote   bool // ' starts a string or char literal
	rustLifetimes bool // 'a is a lifetime, not a char literal
	dollarIdent   bool // $ is part of names (JS, PHP, shell)
	regexLiterals bool // /.../ is a regular expression where a value can start (JS)
	foldCase      bool // keywords and names are case-insensitive (SQL)
	keywords      map[string]bool
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool)
	for _, group := range words {
		for _, w := range strings.Fields(group) {
			m[w] = true
		}
	}
	return m
}

// Names that are common to most languages and almost never renamed.
const literalWords = "true false null nil None True False undefined NaN Infinity this self super"

var (
	goKeywords = set(literalWords, `break case chan const continue default defer else fallthrough for func go goto if
		import interface map package range return select struct switch type var iota append cap clear close complex copy
		delete imag len make max min new panic print println real recover bool byte complex64 complex128 error float32
		float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable`)

	cKeywords = set(literalWords, `auto break case char const continue default do double else enum extern float for
		goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned
		void volatile while bool class namespace template typename public private protected virtual override friend
		operator new delete throw try catch using nullptr NULL std include define ifdef ifndef endif elif undef pragma
		constexpr noexcept static_cast dynamic_cast reinterpret_cast const_cast explicit mutable final size_t ssize_t
		uint8_t uint16_t uint32_t uint64_t int8_t int16_t int32_t int64_t uintptr_t wchar_t char16_t char32_t
		alignas alignof decltype thread_local static_assert concept requires co_await co_return co_yield
		id interface implementation end property synthesize selector protocol YES NO`)

	javaKeywords = set(literalWords, `abstract assert boolean break byte case catch char class const continue default do
		double else enum extends final finally float for goto if implements import instanceof int interface long native
		new package private protected public return short static strictfp super switch synchronized throw throws
		transient try void volatile while var record yield sealed permits non-sealed String Object Integer Long Double
		Boolean Character List Map Set ArrayList HashMap HashSet Override
		val fun object when is in out open data companion lateinit suspend internal inline reified crossinline noinline
		typealias init constructor get set field it Unit Any Nothing Int Float Byte Short Char
		def lazy implicit match trait with sealed type forSome
		func struct protocol extension deinit guard where repeat inout mutating defer some fileprivate let rethrows
		using namespace readonly ref params async await base checked unchecked fixed lock decimal sbyte ushort uint
		ulong event delegate operator virtual override string bool object dynamic nameof typeof is as foreach
		late required factory library part show hide dynamic covariant mixin`)

	jsKeywords = set(literalWords, `break case catch class const continue debugger default delete do else export
		extends finally for function if import in instanceof let new return switch throw try typeof var void while
		with yield async await of static get set type interface enum implements declare namespace module readonly
		keyof infer never unknown any number string boolean symbol bigint object as from require exports
		private protected public abstract override satisfies is asserts unique global`)

	pyKeywords = set(literalWords, `and as assert async await break class continue def del elif else except finally
		for from global if import in is lambda nonlocal not or pass raise return try while with yield match case
		cls print len range str int float bool bytes list dict set tuple isinstance issubclass enumerate zip open
		object type sorted reversed min max sum any all map filter iter next hasattr getattr setattr property
		staticmethod classmethod Exception ValueError TypeError KeyError IndexError RuntimeError`)

	rustKeywords = set(literalWords, `as break const continue crate else enum extern fn for if impl in let loop match
		mod move mut pub ref return Self static struct trait type unsafe use where while async await dyn Box Vec
		String Option Some Result Ok Err usize isize u8 u16 u32 u64 u128 i8 i16 i32 i64 i128 f32 f64 str char bool
		macro_rules println format vec panic assert assert_eq unwrap expect clone into iter`)

	rubyKeywords = set(literalWords, `alias and begin break case class def defined do else elsif end ensure for if in
		module next not or redo rescue retry return then undef unless until when while yield puts require
		require_relative attr_accessor attr_reader attr_writer private protected public raise lambda proc new`)

	phpKeywords = set(literalWords, `abstract and array as break callable case catch class clone const continue
		declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile extends final
		finally fn for foreach function global goto if implements include include_once instanceof insteadof interface
		isset list match namespace new or print private protected public readonly require require_once return static
		switch throw trait try unset use var while xor yield $this int string bool float mixed void`)

	shellKeywords = set(literalWords, `if then else elif fi for while until do done case esac function in local
		export return echo exit set shift read printf test source unset declare`)

	sqlKeywords = set(literalWords, `select from where insert into values update set delete create table index view
		drop alter add column primary key foreign references not null default unique join left right inner outer full
		cross on group by order having limit offset union all distinct as and or in is like ilike between exists case
		when then else end begin commit rollback transaction integer int varchar char text boolean timestamp date time
		serial bigint smallint numeric decimal real returning with recursive count sum min max avg coalesce if
		procedure function returns declare language trigger replace temporary asc desc`)

	luaKeywords = set(literalWords, `and break do else elseif end false for function goto if in local nil not or
		repeat return then true until while require print pairs ipairs table string math`)
)

var (
	synGo     = &syntax{name: "go", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, backtick: true, singleQuote: true, keywords: goKeywords}
	synC      = &syntax{name: "c", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, singleQuote: true, keywords: cKeywords}
	synJava   = &syntax{name: "java", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, singleQuote: true, tripleQuotes: true, dollarIdent: true, keywords: javaKeywords}
	synNested = &syntax{name: "kotlin-swift-scala", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, nestedBlocks: true, singleQuote: true, tripleQuotes: true, backtick: true, keywords: javaKeywords}
	synJS     = &syntax{name: "js", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, backtick: true, singleQuote: true, dollarIdent: true, regexLiterals: true, keywords: jsKeywords}
	synRust   = &syntax{name: "rust", lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, nestedBlocks: true, singleQuote: true, rustLifetimes: true, keywords: rustKeywords}
	synPython = &syntax{name: "python", lineComments: []string{"#"}, tripleQuotes: true, stringPrefix: true, singleQuote: true, keywords: pyKeywords}
	synRuby   = &syntax{name: "ruby", lineComments: []string{"#"}, blockComments: [][2]string{{"\n=begin", "\n=end"}}, singleQuote: true, backtick: true, keywords: rubyKeywords}
	synPHP    = &syntax{name: "php", lineComments: []string{"//", "#"}, blockComments: [][2]string{{"/*", "*/"}}, singleQuote: true, backtick: true, dollarIdent: true, keywords: phpKeywords}
	synShell  = &syntax{name: "shell", lineComments: []string{"#"}, singleQuote: true, backtick: true, dollarIdent: true, keywords: shellKeywords}
	synPerl   = &syntax{name: "perl", lineComments: []string{"#"}, singleQuote: true, backtick: true, dollarIdent: true, keywords: shellKeywords}
	synSQL    = &syntax{name: "sql", lineComments: []string{"--"}, blockComments: [][2]string{{"/*", "*/"}}, singleQuote: true, foldCase: true, keywords: sqlKeywords}
	synLua    = &syntax{name: "lua", lineComments: []string{"--"}, blockComments: [][2]string{{"--[[", "]]"}}, singleQuote: true, keywords: luaKeywords}
	synCSS    = &syntax{name: "css", blockComments: [][2]string{{"/*", "*/"}}, singleQuote: true, keywords: set(literalWords)}
	synHash   = &syntax{name: "hash", lineComments: []string{"#"}, singleQuote: true, keywords: set(literalWords)}
)

// languages maps file extensions to their syntax. Only source code is
// fingerprinted: documentation, data and configuration files are left out.
var languages = map[string]*syntax{
	".go": synGo,

	".c": synC, ".h": synC, ".cc": synC, ".cpp": synC, ".cxx": synC, ".c++": synC, ".hpp": synC, ".hh": synC,
	".hxx": synC, ".h++": synC, ".ipp": synC, ".inl": synC, ".m": synC, ".mm": synC, ".cu": synC, ".cuh": synC,

	".java": synJava, ".cs": synJava, ".dart": synJava, ".groovy": synJava, ".gradle": synJava, ".sol": synJava,
	".proto": synC, ".zig": synC, ".v": synC, ".d": synC,

	".kt": synNested, ".kts": synNested, ".swift": synNested, ".scala": synNested, ".sc": synNested,

	".js": synJS, ".jsx": synJS, ".mjs": synJS, ".cjs": synJS, ".ts": synJS, ".tsx": synJS, ".mts": synJS,
	".cts": synJS, ".vue": synJS, ".svelte": synJS,

	".rs": synRust,

	".py": synPython, ".pyw": synPython, ".pyi": synPython, ".pyx": synPython,

	".rb": synRuby, ".rake": synRuby,

	".php": synPHP,

	".sh": synShell, ".bash": synShell, ".zsh": synShell, ".ksh": synShell,
	".pl": synPerl, ".pm": synPerl,

	".sql": synSQL,
	".lua": synLua,
	".css": synCSS, ".scss": synJS, ".less": synJS,

	".r": synHash, ".jl": synHash, ".ex": synHash, ".exs": synHash, ".nim": synHash, ".cr": synRuby,
	".ps1": synHash, ".psm1": synHash, ".tcl": synHash,
}

// languageFor returns the syntax for a file name, or nil when the file isn't
// source code Remixware reads.
func languageFor(name string) *syntax {
	base := strings.ToLower(filepath.Base(name))
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".pb.go") {
		return nil
	}
	ext := filepath.Ext(base)
	if ext == "" {
		return nil
	}
	return languages[ext]
}

// skipReason says why a file that looks like source code is not read: it is
// binary, generated by a tool, minified, or too large. Empty means read it.
// readSource reads a source file for fingerprinting, or says why it is
// skipped. Only regular files are read, and a file over the size limit is
// skipped without reading it.
func readSource(path string) ([]byte, string) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, "unreadable"
	}
	if !st.Mode().IsRegular() {
		return nil, "not-a-file"
	}
	if st.Size() > maxFileSize {
		return nil, "too-large"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "unreadable"
	}
	if r := skipReason(data); r != "" {
		return nil, r
	}
	return data, ""
}

func skipReason(data []byte) string {
	if len(data) > maxFileSize {
		return "too-large"
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "binary"
	}
	// Generated code: the Go convention and the common @generated marker,
	// looked for near the top of the file only.
	top := head
	if len(top) > 2048 {
		top = top[:2048]
	}
	low := bytes.ToLower(top)
	if (bytes.Contains(low, []byte("code generated")) && bytes.Contains(low, []byte("do not edit"))) ||
		bytes.Contains(low, []byte("@generated")) || bytes.Contains(low, []byte("autogenerated file")) ||
		bytes.Contains(low, []byte("auto-generated file")) || bytes.Contains(low, []byte("this file was automatically generated")) {
		return "generated"
	}
	// Minified: very long lines.
	if len(data) > 4096 {
		lines := bytes.Count(data, []byte{'\n'}) + 1
		if len(data)/lines > 400 {
			return "minified"
		}
	}
	return ""
}

const maxFileSize = 2 << 20

// skipDirs are folders Remixware doesn't walk into when scanning a project:
// version control, installed dependencies and virtual environments. Declared
// dependencies are what package managers already report; Remixware looks for
// the code that arrived without one.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".bzr": true,
	"node_modules": true, "vendor": true, "third_party": true, "third-party": true, "bower_components": true,
	".venv": true, "venv": true, "__pycache__": true, ".tox": true, ".mypy_cache": true, "Pods": true,
	".gradle": true, ".idea": true, ".vscode": true,
}

// skipIndexDirs are folders never read while indexing.
var skipIndexDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, ".bzr": true, "__pycache__": true}

// TokenCount returns how many tokens Remixware reads in a source file, or 0
// for files it doesn't read.
func TokenCount(name string, src []byte) int {
	sx := languageFor(name)
	if sx == nil {
		return 0
	}
	return len(tokenize(src, sx))
}
