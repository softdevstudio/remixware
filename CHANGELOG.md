# Changelog

## 0.1.0

First release.

- `remixware index` fingerprints third-party code (dependency folders, the
  Go module cache, cloned projects) and records each file's component and
  licence. Components are found by `go.mod`, `package.json`, `Cargo.toml`,
  `composer.json`, `pyproject.toml` and `.dist-info` folders, and by
  folders with a licence file. Licences come from manifests, licence
  files, REUSE `LICENSES/` folders and `SPDX-License-Identifier` lines.
- `remixware scan` finds code a project shares with the indexes, including
  copies that were reformatted, had their comments changed, had their names
  changed or were partly edited, and reports each with its source, its
  licence and a status: conflict, no-license, review, attribution-missing,
  attributed, allowed or ok.
- `remixware check` is the same as a pass or fail gate for CI, with exit
  code 1 when findings break the policy.
- `remixware notice` writes third-party notice entries with copyright lines
  and licence texts, and skips entries the notices file already has.
- `--from` reads results files from the SCANOSS scanner (`scanoss-py scan
  -o results.json`) and findings files from `remixware scan --json`. A copy
  found by both an index and SCANOSS is reported once.
- A policy file (`.remixware.json`) sets the project's licence, the
  statuses that fail a check, paths to ignore, notice files, and allow
  rules that need a reason.
- Reports: a self-contained HTML page with both sides of every match, a
  JSON findings file and an SPDX 2.3 document.
- Reads Go, C and C++, Java, C#, Kotlin, Swift, Scala, JavaScript and
  TypeScript, Rust, Python, Ruby, PHP, shell, Perl, SQL, Lua, CSS and a few
  more. Windows line endings give the same results as Unix ones.
- `remixware licenses` lists the licences Remixware knows and what copying
  each one into an MIT project needs.
