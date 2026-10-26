# Changelog

## 0.2.0

- `--scanoss` asks the SCANOSS service directly, so a check covers a large
  share of public open source without running SCANOSS's scanner first.
  Remixware makes its fingerprints the way that scanner does and sends
  them with numbers in place of file names: no code and no file names
  leave the machine. It works with `scan`, `check` and `notice`, alone or
  together with `--index` and `--from`.
- `--scanoss-key` (or `SCANOSS_API_KEY`) uses a SCANOSS API key and the
  service for key holders, `--scanoss-url` (or `SCANOSS_SCAN_URL`) another
  SCANOSS server, and `--scanoss-out` saves the answers as a SCANOSS results
  file for `--from`. As with SCANOSS's scanner, the URL's host is asked at
  `/scan/direct`. Redirects aren't followed, and a user name, password or
  query in the URL never shows up in output or reports.
- An answer has to cover every file sent. One that leaves files out, as
  the service does when part of its work fails, is asked for again and then
  ends the command with exit code 2, so a check can't pass on files
  SCANOSS didn't look at.
- A rate limit, a server error or an incomplete answer is tried again,
  after as long as the service asks, a minute at a time, with a line saying
  so. When the service asks for a longer wait than the tries left could
  cover, Remixware stops at once and says how long and until when. A refused
  key, an answer that isn't SCANOSS results or a service that doesn't
  answer ends the command with exit code 2.
- The minified files a scan skips (`.min.js`, `.min.css` and source files
  with very long lines) go to SCANOSS too, since it can name a whole
  minified library.
- JSON reports list the service as an input of kind `scanoss-api`, with
  its address.
- Text from SCANOSS answers and from files read with `--from` is kept to
  printable characters on one line, so it can't move the cursor, clear the
  screen or add lines to a notices file; licence texts keep their line
  breaks. Paths in those files with characters that aren't printable are
  left out.
- SCANOSS answers write a missing vendor as "None". Remixware no longer
  puts "None/" in front of such a component's name, in results files read
  with `--from` too.

## 0.1.1

- Copyright lines no longer come from inside a licence's own text. Item
  (c) of a licence's list, such as the MPL-2.0's "(c) under Patent Claims
  infringed by Covered Software" or the Apache-2.0's "(c) You must retain
  ...", was taken for a copyright line, and so was a licence document's own
  copyright, such as the Free Software Foundation's at the top of the GPL.
  A copyright line counts as credit, so a notices file carrying the same
  licence text for another library could credit the copied code. Templates
  such as "Copyright (C) year  name of author" are skipped too. A component
  under one of those licences with no copyright line of its own now gets a
  notice entry that asks for one, as other components already did.
- The README lists `index --max-common` and `--quiet`, and every language
  Remixware reads.

## 0.1.0

First release.

- `remixware index` fingerprints third-party code (dependency folders, the
  Go module cache, cloned projects) and records each file's component and
  licence. Components are found by `go.mod`, `package.json`, `Cargo.toml`,
  `composer.json`, `pyproject.toml` and `.dist-info` folders, and by
  folders with a licence file. Licences come from manifests, licence
  files, REUSE `LICENSES/` folders, Cargo workspaces and the comments at
  the top of each file. A file header adds to its component's licence but
  can't replace a stricter one, a header offering a choice is read as one,
  and dual licence files such as `LICENSE-MIT` and `LICENSE-APACHE` read as
  a choice.
- `remixware scan` finds code a project shares with the indexes, including
  copies that were reformatted, had their comments changed, had their names
  changed or were partly edited, and reports each with its source, its
  licence and a status: conflict, no-license, review, attribution-missing,
  attributed, allowed or ok. A copy that needs a review and its notice
  counts as attribution-missing until the notice is there. Indexed code
  inside the project counts as third-party when it sits in a vendor-style
  folder or has a licence of its own, and as the project's own otherwise.
  Symbolic links in the project aren't followed, and the scan says how
  many it passed.
- `remixware check` is the same as a pass or fail gate for CI, with exit
  code 1 when findings break the policy. A check that couldn't read a file,
  or found nothing to scan, exits with 2 instead of passing.
- `remixware notice` writes third-party notice entries with copyright lines
  and licence texts, and skips entries the notices file already has. An
  entry it couldn't finish doesn't count as credit until it's finished.
- `--from` reads results files from the SCANOSS scanner (`scanoss-py scan
  -o results.json`) and findings files from `remixware scan --json`. A copy
  found by both an index and SCANOSS is reported once.
- A policy file (`.remixware.json`) sets the project's licence, the
  statuses that fail a check, paths to ignore, notice files, and allow
  rules that need a reason.
- Reports: a self-contained HTML page with both sides of every match, a
  JSON findings file and an SPDX 2.3 document that passes SPDX validation.
- Indexes are data: a damaged or made-up index is refused, including one
  that unpacks to far more than its size or lists more files than it could
  hold, and an indexed file is read back only when its bytes match the
  checksum the index holds. No index makes a scan skip a file. Symbolic
  links aren't followed when indexing, and nothing outside the indexed
  folders is read.
- Reads Go, C and C++, Java, C#, Kotlin, Swift, Scala, JavaScript and
  TypeScript, Rust, Python, Ruby, PHP, shell, Perl, SQL, Lua, CSS and a few
  more. Windows line endings give the same results as Unix ones.
- `remixware licenses` lists the licences Remixware knows and what copying
  each one into an MIT project needs.
