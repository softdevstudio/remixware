# Remixware

Remixware finds code in your project that was copied from open source,
names the licence that code came with, and tells you what that licence asks
of you. It catches the GPL function pasted into an MIT project, the MIT
helper whose copyright line got lost on the way, and the snippet an AI
assistant reproduced from a library it learned from.

A finding reads like this:

```text
CONFLICT             internal/files/order.go:7-46
                     example.org/gplsort, natural.go:14-53 (GPL-3.0-or-later), copied with names changed, 98% match
                     GPL-3.0-or-later code can't go into an MIT project unless the whole program is
                     released under GPL-3.0-or-later. Remove it, rewrite it, or relicense.
```

Remixware is one program with no network access of its own. It's free and
open source under the Apache License 2.0. Site:
[remixware.com](https://remixware.com).

Remixware flags licensing questions. It isn't legal advice: when a finding
matters, ask someone who can give it.

## Install

Download the archive for your system from the
[latest release](https://github.com/softdevstudio/remixware/releases/latest),
unpack it and put `remixware` on your PATH. The archives are
`remixware_linux_amd64.tar.gz`, `remixware_linux_arm64.tar.gz`,
`remixware_darwin_arm64.tar.gz`, `remixware_darwin_amd64.tar.gz` and
`remixware_windows_amd64.zip`, and `SHA256SUMS` lists their checksums.

Or build it with Go 1.24 or later. Remixware uses only the Go standard
library:

```sh
go install github.com/softdevstudio/remixware/cmd/remixware@latest
```

## Quick start

Remixware compares your code with code you point it at. First make an
index of that code, then scan your project against it:

```sh
# 1. Fingerprint the open source code you depend on, or any code you suspect was copied.
remixware index ~/go/pkg/mod node_modules --out deps.rmx

# 2. List what your project shares with it, with an HTML report.
remixware scan . --index deps.rmx --html remixware-report.html

# 3. Fail a CI build on licence conflicts, code with no licence and missing attribution.
remixware check . --index deps.rmx

# 4. Write the notices that missing attribution asks for.
remixware notice . --index deps.rmx --out THIRD_PARTY_NOTICES.md
```

`check` exits with 1 when findings break your policy, so a build stops
there. After `notice` has written the entries, the same `check` passes for
attribution. When `notice` can't find something an entry needs, such as a
copyright line, it says so, and that entry counts once you've filled it in.
Conflicts and code with no licence can't be fixed with a notice: those need
the code removed, rewritten, or a different licence.

## What to index

An index holds fingerprints of source files, the component each file
belongs to, and that component's licence. Good things to index:

- Your dependencies as they sit on disk: `node_modules`, `vendor`, the Go
  module cache (`~/go/pkg/mod`), a Python virtual environment's
  `site-packages`, Cargo's sources (`~/.cargo/registry/src`).
- Projects you suspect, cloned with `git clone --depth 1`.
- Your company's other code, with `--license LicenseRef-Proprietary`, to
  see what moved between projects. In a proprietary project those copies
  are listed for `review` (fine when both belong to the same owner); in an
  open source project they are conflicts.

Remixware finds the components inside a folder by their manifests
(`go.mod`, `package.json`, `Cargo.toml`, `composer.json`, `pyproject.toml`,
`.dist-info` folders) and by folders with a licence file. A component's
licence comes from its manifest, its licence files and `LICENSES/` folders
(REUSE). A folder without a licence takes the licence of the folder above
it, except across `node_modules`, `vendor`, `third_party` and similar
folders, where it gets none. Code with no licence at all is reported as
`no-license`, because nobody has given permission to copy it.

- Several licence files all apply, except files that each name one licence,
  such as `LICENSE-MIT` and `LICENSE-APACHE`, or a short file saying the
  project is dual licensed: those are a choice (`Apache-2.0 OR MIT`).
- A crate whose `Cargo.toml` says `license.workspace = true` takes the
  licence of its workspace, when the workspace is inside the indexed
  folder. A licence file that a manifest names counts only inside the
  indexed folder too, with links followed.
- A licence text with a condition on top, such as the Commons Clause after
  the Apache License, counts as both.
- Python distributions that install into one folder, such as `google/`, are
  told apart by their `RECORD` files.
- A file's own header can say more. An `SPDX-License-Identifier` line in the
  comments at its top is that file's licence. A licence notice there that
  names something other than the component's licence is counted along with
  it, so a header can't hide a stricter licence: a file headed MIT inside a
  GPL project counts as both. Licences a header offers as a choice ("BSD,
  or alternatively the GPL", "either the GPL or the CDDL", "at your option")
  count as one, and when the component's licence is among them the header's
  wider grant counts as written. A GNU notice's "version 2, or (at your
  option) any later version" is a choice of versions, not of licences.
  Where the component has no licence, a header that only names the licence
  of the code it came from ("Based on Foo. Foo is MIT licensed.") gives the
  file none. Only comments count, not code.
- `--license` sets the licence of the components. Headers and SPDX tags
  still count with it, and add to it rather than replace it.
- Symbolic links are never followed and only regular files are read, so a
  `NOTICE` that links to a file elsewhere on the machine stays out of the
  index.

```sh
remixware index ./some-project --name github.com/acme/tool --version v1.2.0 --out tool.rmx
remixware index ~/src/old-product --license LicenseRef-Proprietary --out ours.rmx
```

An index can hold code inside your project, such as your own `vendor`
folder, or the whole project when you index a folder above it. Indexed
code inside the project is third-party code when it sits in a folder such
as `vendor` or `third_party`, has a licence of its own that differs from
the project's, or comes from an index of a folder inside the project. The
rest of the project is checked for code copied from it, and no file is
reported as a copy of the component it's part of. Everything else inside
the project is the project's own code: it's scanned like the rest, and
nothing is reported as copied from it. An index never makes a scan skip a
file. `node_modules`, `vendor`, `third_party` and virtual environments
aren't scanned unless you pass `--include-deps`, and `.git` never is.
Symbolic links aren't followed in the project, and the scan says how many
it passed over.

An index is data. One made on another machine, or by someone else, can't
show a scan any file on your machine that it doesn't hold: an indexed file
is read back only when its bytes still match the checksum in the index. An
index that doesn't hold together, or that unpacks to far more than its
size or lists more files than it could hold, is refused as damaged.

## With SCANOSS

An index only knows the code you put in it. For a check against a large
share of all public open source, Remixware reads the results of the free
[SCANOSS](https://www.scanoss.com) scanner:

```sh
pip3 install scanoss
scanoss-py scan -o scanoss.json .
remixware check . --from scanoss.json
```

`--from` and `--index` work together; a copy both find is reported once.
SCANOSS's scanner talks to the SCANOSS service. According to SCANOSS it
sends fingerprints of your files, with their paths and checksums, instead
of the source code; check their documentation if that matters for your
project. Remixware itself never connects to anything.

`--from` also reads a findings file written by `remixware scan --json`,
so a scan made on one machine can be checked on another.

## Findings

Every finding has a status:

| Status | Meaning |
|---|---|
| `conflict` | The copied code's licence can't be combined with your project's licence as it stands, for example GPL code in an MIT project. |
| `no-license` | No licence was found for the source, so nobody has given permission to copy it. |
| `review` | A person has to look: a file-level copyleft licence such as MPL-2.0 or LGPL, code that fits only if your project moves to another licence version (Apache-2.0 into GPL-2.0-or-later), a licence text Remixware doesn't recognise, or the same code also found under stricter terms. A copy that also needs its notice shows as `attribution-missing` until your notices have it. |
| `attribution-missing` | The licence allows the copy, but its copyright line and licence text have to ship with your code, and your notice files don't have them. |
| `attributed` | Allowed, and credited: your notice files name the source, or the copied file kept its original copyright header. |
| `allowed` | Accepted by a rule in your policy file. |
| `ok` | Public domain or similar: nothing to do. |

`check` fails on `conflict`, `no-license` and `attribution-missing` unless
you say otherwise with `--fail-on` or the policy file. `remixware licenses`
lists every licence Remixware knows and what copying it into an MIT
project needs.

A finding also says how the code was copied: `verbatim` (copied as is,
maybe reformatted or with comments changed), `edited` (some lines changed),
`renamed` (names changed throughout), or for SCANOSS results `file` and
`snippet`. Each finding has an ID like `RW-5441d221` that stays the same
from scan to scan while the file and source stay the same.

Your project's licence comes from its licence file or manifest. Pass
`--license MIT` (any SPDX expression) or set `license` in the policy file
when it can't be found, or when the code ships under different terms.
Without any licence, your project is treated as proprietary.

Notice files are found by name in the project's top folder:
`NOTICE`, `THIRD_PARTY_NOTICES.md`, `CREDITS`, `ATTRIBUTION`,
`ACKNOWLEDGEMENTS` and similar, and the files in folders such as
`licenses/` and `third_party_licenses/`. A source counts as credited when a
notice has its full component name (such as `github.com/acme/tool` or
`click@8.1.7`), its repository address, its package URL, one of its full
copyright lines, or the `Source:` line `remixware notice` writes, as a whole
word: `github.com/acme/tool-extras` doesn't credit `github.com/acme/tool`. A
bare name like `errors` doesn't count, since it's an everyday word, and
neither does an entry `remixware notice` left for you to finish.

## Policy file

Put `.remixware.json` in your project's top folder, or pass `--policy FILE`.
Every field is optional. Unknown fields are errors, so a typo can't
quietly switch a rule off.

```json
{
  "license": "MIT",
  "fail_on": ["conflict", "no-license", "attribution-missing"],
  "ignore": ["testdata/**", "docs/examples/**"],
  "notice_files": ["THIRD_PARTY_NOTICES.md"],
  "allow": [
    {"component": "github.com/acme/*", "reason": "Acme gave us a commercial licence in 2025"},
    {"license": "MPL-2.0", "file": "third_party/mpl/**", "reason": "MPL files are kept unchanged and published"}
  ],
  "min_tokens": 50,
  "min_lines": 5
}
```

- `license`: your project's licence, as an SPDX expression.
- `fail_on`: the statuses that make `check` fail. An empty list is an
  error, since it would pass everything.
- `ignore`: project paths not to scan. Patterns work like `.gitignore`:
  `*` stays inside a folder, `**` crosses folders, a bare name matches at
  any depth.
- `notice_files`: where you credit third-party code, when the defaults
  don't find it. Folders are read file by file.
- `allow`: findings you accept on purpose. Each rule names a `component`
  (`*` works as a wildcard), a `file` pattern, a `license`, or several of
  them, and must give a `reason`. The finding is then `allowed`, and the
  report shows the reason.
- `min_tokens`, `min_lines`: the smallest match to report.

## Commands

```text
remixware index <folder>... [--out FILE] [--name N] [--version V] [--license EXPR] [--exclude PATTERN]
remixware scan [project] (--index FILE | --from FILE)... [--html F] [--json F] [--spdx F]
remixware check [project] (--index FILE | --from FILE)... [--fail-on LIST] [--html F] [--json F]
remixware notice [project] (--index FILE | --from FILE)... [--out FILE] [--all]
remixware licenses
remixware version
```

`scan`, `check` and `notice` also take `--license`, `--policy`,
`--exclude`, `--include-deps`, `--min-tokens`, `--min-lines` and `--quiet`.
`remixware help <command>` lists a command's flags. Flags can come before or
after the project folder.

Exit codes: 0 when all went well, 1 when `check` found findings that break
the policy, 2 for bad arguments, unreadable files and other errors. A
`check` that found no source files to scan also exits with 2, so a wrong
folder or an `--exclude` that covers everything can't pass.

## Reports

- `--html FILE`: one self-contained page with every finding, your code and
  the matching source side by side, and the reason for each status. It
  works offline and in dark mode.
- `--json FILE`: the findings for other tools. The fields are `findings`
  (each with `id`, `file`, `lines`, `status`, `reason`, `source` and
  `match`), `counts` per status, and what was scanned.
- `--spdx FILE`: an SPDX 2.3 document (JSON) with each copied snippet (its
  byte and line ranges), its source, its licence and its copyright lines,
  for software bills of materials.
- `remixware notice`: Markdown entries for a third-party notices file, one
  per component, with the copyright lines, the licence texts and where the
  code is used. Entries already in the file aren't written again. Each
  entry carries the component's own licence files when the index has them,
  and the standard text of any other licence the copy is under, built in
  for MIT, BSD-2-Clause, BSD-3-Clause, ISC, Zlib, BSL-1.0, Apache-2.0 and
  MPL-2.0. For other licences the entry says where to get the text, and
  `notice` lists what's left to finish.

## In CI

```yaml
- name: Licence check
  run: |
    mkdir -p "$HOME/bin"
    curl -sSL https://github.com/softdevstudio/remixware/releases/latest/download/remixware_linux_amd64.tar.gz \
      | tar -xz -C "$HOME/bin" ./remixware
    "$HOME/bin/remixware" index vendor --out deps.rmx
    "$HOME/bin/remixware" check . --index deps.rmx --html remixware-report.html
```

Keep the HTML report as a build artifact, so whoever looks at a failed
build sees the code side by side.

## How matching works

Remixware reads source files as tokens: it drops comments and layout,
keeps keywords, operators and literals, and numbers each name in the order
it first appears. A copy with different spacing, comments or variable names
gives the same tokens. It hashes every run of 17 tokens and keeps a sample
of those hashes, chosen so that a shared run of 28 tokens or more always
leaves a shared hash (winnowing). Hashes shared with an indexed file are
chained into stretches that advance through both files together, and a
stretch becomes a finding when it covers at least 50 tokens and 5 lines.
When only the shape matches and the names don't, it has to cover more,
with fewer gaps, and where a line matches exactly, names included, that
place wins over look-alike lines. Code that everyone writes the same way,
such as a run of near-identical lines or Go's `sort.Interface` methods,
isn't matched, and neither are tables of numbers or strings with no names
in them, such as a hash function's constants.

Go, C and C++, Java, C#, Kotlin, Swift, Scala, JavaScript and TypeScript,
Rust, Python, Ruby, PHP, shell, Perl, SQL, Lua, CSS and a few more are read.
Generated, minified and binary files are skipped, and so are files over
2 MB.

## What Remixware can't see

- Code shorter than the thresholds. A five-line helper copied from
  anywhere won't be found, and that's on purpose: short code looks alike
  in every project.
- Code that was rewritten in another style or translated to another
  language. Remixware finds copies, not ideas.
- Code that isn't in your indexes or in the SCANOSS results. An index
  can't know the rest of the world.
- Whether a licence was actually granted to you some other way, such as a
  commercial agreement. Record those as `allow` rules.

## How it was tested

The tests cover every operation: the tokenizer, fingerprinting, licence
detection and the licence compatibility table, manifests, indexes and
damaged index files, SCANOSS import, policy files, notices, the reports
and the command line. Fixture projects in `testdata` copy code from small
open source projects written for the tests, with names changed or lines
edited, next to code of their own that only looks like it. The tests check
that every copy is found with the right source, licence, lines and status,
and nothing else is.

The benchmark (`scripts/bench`) clones real open source projects at pinned
versions, indexes them, copies thousands of their functions into a test
project (as is, reformatted, with comments changed, with names changed,
partly, and all at once), and counts how many copies are traced to the
right function. It also scans projects that aren't in the index to count
false alarms. The recorded results are in [test/results](test/results), and
the sample reports are in [test/results/gallery](test/results/gallery).

```sh
go test ./...                  # unit, fixture and command tests
scripts/record-results.sh      # tests, benchmark and gallery, recorded to test/results
```

## License

Apache License 2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
