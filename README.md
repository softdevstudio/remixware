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

Remixware also guards dependency changes. `remixware deps` compares the
packages your lockfiles name with those on your main branch, and fails a
pull request that brings in a package nobody allowed, a package that runs
install scripts, or a release from a new publisher. See
[Dependency changes](#dependency-changes).

Remixware is one program. It connects to nothing unless you ask it to,
with `--scanoss` (it sends fingerprints of your files, never the code) or
with `deps --online` (it asks the npm registry and PyPI about package
versions). It's free and open source under the Apache License 2.0. Site:
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
The archives hold the program and its README, licence and notices with no
folder around them, so unpack one into an empty folder.

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

# 5. In a pull request, fail on new packages nobody allowed and on install scripts.
remixware deps . --base-ref origin/main
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
share of all public open source, Remixware can ask the
[SCANOSS](https://www.scanoss.com) service, directly or through a results
file from SCANOSS's own scanner.

Directly:

```sh
remixware check . --scanoss
```

`--scanoss` fingerprints the files a scan reads, plus the minified files a
scan skips (`.min.js`, `.min.css` and source files with very long lines),
which SCANOSS can name as a whole. It makes its fingerprints the way
SCANOSS's scanner does and sends them to the free service at
`https://api.osskb.org`, which the Software Transparency Foundation runs.
For each file it sends a number with the file's extension, the file's MD5
and size, a second MD5 taken with the other kind of line endings, and the
hashes of its text: no code and no file names. Each request also carries
`remixware/VERSION` as its user agent, a random request ID and, when you
set one, your API key. Two differences from SCANOSS's scanner: it sends
each file's path in place of the number unless you run it with
`--obfuscate`, and it takes a few text files for binary by their first
bytes, such as a source file starting with "BM", and sends no hashes for
them, where Remixware hashes them. The answers are matched
back to your files by those numbers, and they have to cover every file
sent: an answer that leaves files out is asked for again, and then ends
the command with exit code 2. The service's
[terms](https://www.softwaretransparency.org/terms) apply: it's free and
comes with no guarantees, it limits how much you ask, and it may log usage.
Remixware waits and tries again when the service asks it to slow down for a
moment. When the service asks for a longer wait, as it does once a limit is
used up (sometimes until the next month), Remixware stops and says how
long, and for a long wait until when, and a check exits with 2. Hosted CI runners are shared by many
projects, so there the free service's limits may already be used up; an API
key avoids that.

With a SCANOSS API key, set `SCANOSS_API_KEY` (or pass `--scanoss-key`)
and Remixware asks `https://api.scanoss.com` instead. `--scanoss-url` (or
`SCANOSS_SCAN_URL`) points it at another SCANOSS server, such as one you
run: as SCANOSS's scanner does, Remixware asks `/scan/direct` on that URL's
host and leaves out any other path. It doesn't follow redirects, which
could take the key and the fingerprints elsewhere. `--scanoss-out FILE`
saves the answers as a SCANOSS results file, which `--from` reads later
without asking again.

Through SCANOSS's scanner:

```sh
pip3 install scanoss
scanoss-py scan -o scanoss.json .
remixware check . --from scanoss.json
```

`--index`, `--from` and `--scanoss` work together; a copy that more than
one of them finds is reported once.

`--from` also reads a findings file written by `remixware scan --json`,
so a scan made on one machine can be checked on another.

Answers from SCANOSS, files read with `--from` and indexes made elsewhere
can't take over your terminal or your notices file. Characters a terminal
acts on (escape sequences, line breaks inside a name, direction marks) are
taken out of their text, which otherwise stays as it is, spacing included,
so copyright lines still match. Everything Remixware prints goes through
the same filter, and a copyright line that would start a Markdown block in
a notices file is escaped. An entry of a results or findings file whose
path points outside the project, or holds such characters, is left out
with a warning, and `check` then exits with 2. A results file may give one
file at most 100 SCANOSS matches, each listing at most 100 licences,
copyright lines or package URLs, and a findings file may give one file at
most 20,000 findings, each with at most 100 copyright lines; more ends the
command with exit code 2. A file whose name holds a backslash or starts
like a drive letter (possible on Linux and macOS) is found by `--scanoss`,
but answers saved with `--scanoss-out` and read back with `--from` take
the backslash for a folder separator, and leave out a name like `c:x.go`
(a check then exits with 2).

## Dependency changes

`remixware deps` is a second check, next to the licence check. It compares
the dependency set of a change with that of your main branch and fails the
change when it brings in supply-chain risk:

```sh
# In a pull request, against main (read from the local git repository):
remixware deps . --base-ref origin/main

# Or against another checkout of main:
remixware deps . --base ../main-checkout
```

A run on a change that adds two packages and updates esbuild reads like
this (trimmed):

```text
Compared the dependencies of . with origin/main
Lockfiles: go.mod (4 packages), requirements.txt (3 packages), web/package-lock.json (7 packages)

INSTALL SCRIPT       npm/esbuild 0.19.2 -> 0.21.5 (direct)
                     in web/package-lock.json
                     Runs an install script (preinstall, install or postinstall, or a node-gyp
                     build) when npm installs it. Accept it with a deps allow rule that has
                     install_scripts: true and a reason.

NOT ALLOWED          npm/is-thirteen 2.0.0, added (transitive)
                     in web/package-lock.json
                     New, and not on the allow list. Add a deps allow rule with a reason to the
                     policy file, or drop it.

Checks:
  allow list         ran, 5 added packages: 5 checked from the lockfiles
  install scripts    partly, 10 added or updated versions: 4 checked from the
                     lockfiles, 3 not checked (needs --online), 3 not applicable (Go
                     modules run no install scripts)
  publisher changes  not run, 10 added or updated versions: 7 not checked (needs
                     --online), 3 not applicable (the Go module proxy records no
                     publisher)

10 changes: 1 install-script, 2 not-allowed, 3 allowed, 4 updated.
FAIL: 3 changes break the policy (fail on: not-allowed, install-script, publisher-changed, not-checked).
```

The `Checks:` block says what each check covered, so a pass can be read
for what it means. A check that didn't run says why, and a run without
`--online` never reports an online check.

### What it reads

Remixware finds the lockfiles in the project, leaving out `node_modules`,
`vendor`, virtual environments, `.git` and the policy's `ignore` paths, in
the change and in the base:

- `go.mod` with `go.sum`: go.mod's requirements are direct, or transitive
  when marked `// indirect`, and go.sum's code hashes add the rest of the
  module graph. A `replace` by another module counts as that module; a
  replacement by a local folder is your own code.
- `package-lock.json` and `npm-shrinkwrap.json`, lockfile versions 1 to 3.
  The root package's dependencies (and those of workspace folders) are
  direct. Versions 2 and 3 record which packages run install scripts.
- `requirements*.txt` and the `.txt` files in a `requirements/` folder,
  with `-r` includes inside the project. Requirements are direct unless
  pip-compile's `# via` comments say they came in through other packages.
- `poetry.lock`, with `pyproject.toml` naming the direct packages.

A lockfile Remixware can't make sense of stops the command with exit code
2, on either side: a gate that skipped a broken lockfile would pass
whatever it hides. A lockfile kind it doesn't read (`yarn.lock`,
`pnpm-lock.yaml`, `Pipfile.lock`, `uv.lock`, `Cargo.lock` and others)
that the change touches is reported as `not-checked`, which fails by
default. A `package.json` or `pyproject.toml` without a lockfile isn't
read: commit your lockfiles.

### Statuses

| Status | Meaning |
|---|---|
| `not-allowed` | A package no lockfile on the base names, direct or transitive, and no allow rule names. |
| `install-script` | An added or updated package runs code when it's installed: an npm `preinstall`, `install` or `postinstall` script or a node-gyp build, or a Python package without a wheel, which pip builds from source. |
| `publisher-changed` | An added or updated version was published within `publisher_days` (90 by default) by someone who neither published nor maintained the version it's compared with: the base's version for an update, the release before it for a new package. Only with `--online`. |
| `not-checked` | A lockfile Remixware doesn't read changed, or a requirements file now sends pip to another package index (`--index-url`, `--extra-index-url`, `--find-links`), where a package of the same name could take the place of the one you expect, or an updated package that main takes from the public registry now comes from somewhere else (a URL, git, another registry). |
| `allowed` | A new package an allow rule names. |
| `updated` | A package at a new version, with nothing wrong found. |
| `removed` | A package no lockfile names any more. |

A package counts as new when no lockfile on the base names it under that
name, in that ecosystem. The install-script and publisher checks look at
every added or updated version and nothing else: what main already has
isn't checked again. `deps` fails on `not-allowed`, `install-script`,
`publisher-changed` and `not-checked` unless `--fail-on` or the policy's
`deps.fail_on` says otherwise.

### Offline and online checks

Without `--online`, `deps` reads only the lockfiles, and two runs on the
same files give the same answer. The allow list always runs. Install
scripts are known from `package-lock.json` versions 2 and 3
(`hasInstallScript`) and from `poetry.lock`'s file lists (no wheel means a
build from source). Everything else is listed as not checked.

With `--online`, `deps` also asks the npm registry
(`https://registry.npmjs.org`) and PyPI's JSON API (`https://pypi.org/pypi`)
about each added or updated version from those registries: whether it runs
install scripts, who published it and who maintained it. npm records the
account that published each version. PyPI doesn't say who uploaded a
release, so for PyPI the publisher is the release's maintainer and author
metadata, which is weaker evidence. Go modules run no code when they're
downloaded, and the Go module proxy keeps no record of publishers, so both
checks are "not applicable" to them. Packages from other registries, git or
local folders, and requirements that aren't pinned to one version aren't
looked up, and the output counts them.

What a registry says about a published version doesn't change, so answers
are kept in a cache file (`--cache FILE`, by default
`remixware/registry.json` in your user cache folder, such as
`~/.cache` on Linux) and never asked for again; `--no-cache` keeps none.
In CI, keep that file between runs with your CI's cache. A lookup that
fails (the registry is down, a version isn't there, the answer isn't the
JSON expected) ends the command with exit code 2, never a pass.
`--npm-registry` and `--pypi-url` point the lookups at a mirror.

### Allow rules and the policy

The `deps` object in `.remixware.json` holds the gate's settings:

```json
{
  "deps": {
    "allow": [
      {"package": "golang/golang.org/x/*", "reason": "modules of the Go project"},
      {"package": "npm/@acme/*", "reason": "our own packages"},
      {"package": "npm/esbuild", "install_scripts": true, "reason": "downloads its platform binary"},
      {"package": "pypi/requests", "reason": "reviewed by the platform team"}
    ],
    "fail_on": ["not-allowed", "install-script", "publisher-changed", "not-checked"],
    "lockfiles": ["go.mod", "web/package-lock.json"],
    "publisher_days": 90
  }
}
```

- `allow`: each rule names a `package` as ecosystem and name (`golang/`,
  `npm/` or `pypi/`, then the module path or package name; `*` matches
  anything) and gives a `reason`. A new package a rule names is `allowed`.
  A rule doesn't accept the package's install scripts or a publisher change
  unless it says `install_scripts: true` or `publisher_change: true`. Go
  and npm names compare exactly, case included (`github.com/Sirupsen/logrus`
  and `github.com/sirupsen/logrus` are different modules); PyPI names are
  normalised as pip does (`Zope.Interface` is `zope-interface`).
- `fail_on`: the statuses that fail. An empty list is an error.
- `lockfiles`: the lockfiles to read, instead of searching for them. A
  path that's in neither the change nor the base is an error.
- `publisher_days`: how recent a publisher change has to be to count.

Unknown keys are errors, as everywhere in the policy file. The policy comes
from the change being checked, so a change can add allow rules for its own
packages. `deps` lists the allow rules the change adds compared with the
base's policy, and `--base-policy` uses the base's policy instead, so only
rules already on main count.

### In a pull request

```yaml
on: pull_request
jobs:
  deps:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Dependency gate
        run: |
          mkdir -p "$HOME/bin"
          curl -sSL https://github.com/softdevstudio/remixware/releases/latest/download/remixware_linux_amd64.tar.gz \
            | tar -xz -C "$HOME/bin" ./remixware
          "$HOME/bin/remixware" deps . --base-ref "origin/${{ github.base_ref }}" --base-policy --json deps.json
```

`--base-ref` reads the base's lockfiles with `git ls-tree` and
`git cat-file`, so the base branch has to be fetched (`fetch-depth: 0`, or
`git fetch origin main`). Add `--online` to run the install-script and
publisher checks against the registries.

`--json FILE` writes the result for other tools: `project`, `base`,
`online`, `lockfiles` (each with `path`, `kind`, `head_packages`,
`base_packages`, `in_head`, `in_base`), `changes` (each with `package`,
`ecosystem`, `name`, `change` (added, updated, removed or unread),
`from`, `to`, `direct`, `lockfiles`, `status`, `reason`, `allowed_by` and
`issues`, each issue with `status`, `version` and `reason`), `checks` (each
with `name`, `status` (ran, partly, not-run, not-applicable or
nothing-to-check), `packages`, `offline`, `online`, `cached`,
`not_applicable` and `not_run`, the count per reason), `counts` per status
and `new_allow_rules`.

## Findings

Every finding has a status:

| Status | Meaning |
|---|---|
| `conflict` | The copied code's licence can't be combined with your project's licence as it stands, for example GPL code in an MIT project. |
| `no-license` | No licence was found for the source, so nobody has given permission to copy it. |
| `review` | A person has to look: a file-level copyleft licence such as MPL-2.0 or LGPL, code that fits only if your project moves to another licence version (Apache-2.0 into GPL-2.0-or-later), a licence text Remixware doesn't recognise, or the same code also found under stricter terms. A copy that also needs its notice shows as `attribution-missing` until your notices have it. |
| `attribution-missing` | The licence allows the copy, but its copyright line and licence text have to ship with your code, and your notice files don't have them. |
| `attributed` | Allowed, and credited: your notice files name the source, a licence or notice file elsewhere in the project does, or the copied file kept its original copyright header. |
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

Credit kept with the code counts too. Projects that vendor a library often
keep its licence in the library's own folder (`typer/_click/LICENSE.txt`,
`pip/_vendor/requests/LICENSE`), or keep a second licence file at the top
(`LICENSE.xmpp-client`). Remixware reads the licence files (`LICENSE*`,
`COPYING*`) and notice files anywhere in the project, and a source counts as
credited when one of them has its full copyright line or its name as above.
Source and build files named like notices (`attribution.go`,
`ThirdParty.cmake`) don't count. Paths `--exclude` and the policy's `ignore`
name are left out, and so are folders a scan skips, such as `vendor/` and
`node_modules/`: a dependency's licence there doesn't credit code copied out
of it into your own files, since the package manager may update or drop it.
Some licence texts name no copyright holder, as Apache-2.0 and MPL-2.0
don't. For a source like that, its own licence text counts when it is kept
whole in the nearest folder with a licence file (`LICENSE*`, `COPYING*`),
starting with the copy's own, but not in the project's top folder, where the
same text could belong to any other library. If the source also ships a
NOTICE, its NOTICE text has to be there as well, as Apache-2.0 asks. Without
it, the finding says the licence is there and the NOTICE text isn't.

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
  "min_lines": 5,
  "deps": {"allow": [{"package": "npm/@acme/*", "reason": "our own packages"}]}
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
- `min_tokens`, `min_lines`: the smallest match from an index to report.
- `deps`: the dependency gate's allow rules and settings (see
  [Allow rules and the policy](#allow-rules-and-the-policy)). `ignore`
  also leaves lockfiles out of its search.

## Commands

```text
remixware index <folder>... [--out FILE] [--name N] [--version V] [--license EXPR] [--exclude PATTERN]
                            [--max-common N] [--quiet]
remixware scan [project] (--index FILE | --from FILE | --scanoss)... [--html F] [--json F] [--spdx F]
remixware check [project] (--index FILE | --from FILE | --scanoss)... [--fail-on LIST] [--html F] [--json F]
remixware notice [project] (--index FILE | --from FILE | --scanoss)... [--out FILE] [--all]
remixware deps [project] (--base FOLDER | --base-ref REV) [--online] [--fail-on LIST] [--json F]
                         [--base-policy] [--cache F] [--no-cache] [--npm-registry URL] [--pypi-url URL]
remixware licenses
remixware version
```

`scan`, `check` and `notice` also take `--license`, `--policy`,
`--exclude`, `--include-deps`, `--min-tokens`, `--min-lines` and `--quiet`,
and with `--scanoss`, `--scanoss-url`, `--scanoss-key` and `--scanoss-out`
(see [With SCANOSS](#with-scanoss)).
For `index`, `--max-common N` drops fingerprints found in more than N
components (0, the default, picks a value; -1 keeps them all), and
`--quiet` prints nothing but errors.
`remixware help <command>` lists a command's flags. Flags can come before or
after the project folder.

`deps` also takes `--policy` and `--quiet` (see
[Dependency changes](#dependency-changes)).

Exit codes: 0 when all went well, 1 when `check` found findings that break
the policy or `deps` found changes that do, 2 for bad arguments, unreadable files and other errors. A
`check` that found no source files to scan, or none to send to SCANOSS,
also exits with 2, so a wrong folder or an `--exclude` that covers
everything can't pass. So does a `--scanoss` check that the service didn't
answer or refused.

## Reports

- `--html FILE`: one self-contained page with every finding and the reason
  for each status, with your code and the matching source side by side for
  matches from an index, and a link to the source for SCANOSS matches. It
  works offline and in dark mode.
- `--json FILE`: the findings for other tools. The fields are `findings`
  (each with `id`, `file`, `lines`, `status`, `reason`, `source` and
  `match`, and `credited` when the project carries the copy's notice),
  `counts` per status, and what was scanned. `inputs` lists what
  the project was compared with, each with a `kind`: `index`, `scanoss`
  (a results file), `remixware` (a findings file) or `scanoss-api` (the
  SCANOSS service, with its URL as `path` and the number of `files` sent).
  For a results or findings file, `dropped` counts the entries left out.
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

Remixware reads Go, C and C++, Objective-C, Java, C#, Dart, Kotlin, Swift,
Scala, JavaScript and TypeScript (with Vue and Svelte files), Rust, Python,
Ruby, PHP, shell, Perl, SQL, Lua, CSS and SCSS, R, Julia, Elixir, Nim,
PowerShell and Tcl.
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

The dependency gate's tests read every lockfile kind, broken ones
included, run the gate against a fake registry for the online checks, and
check its promise on generated changes across all five lockfile formats: a
new package that isn't allowed always fails, an unchanged set (in any
order) or an allowed package always passes, a new install script always
fails, and a broken lockfile always stops the command. The counts are in
[test/results/deps](test/results/deps).

```sh
go test ./...                  # unit, fixture and command tests
scripts/record-results.sh      # tests, benchmark and gallery, recorded to test/results
scripts/record-deps.sh         # the dependency gate only, recorded to test/results/deps
```

## License

Apache License 2.0. See [LICENSE](LICENSE), [NOTICE](NOTICE) and
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
