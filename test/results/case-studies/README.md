# Case studies

Remixware run on real open source projects that took code from others, for
the case studies on remixware.com. `scripts/record-results.sh` writes these
files with `scripts/case_studies.py`, which clones the projects at the
versions below and downloads the releases pip vendors from PyPI, checking
each against the SHA-256 PyPI publishes for it. Nothing here is the
projects' code: only what Remixware reported.

Each folder holds the full report of every scan (`*.txt`, which starts
with the command that made it) and a `summary.txt` with the counts the
site quotes.

- `typer/`: typer 0.12.5 compared with Click 8.1.7, and typer 0.27.3
  compared with Click 8.3.1, the version its `typer/_click` folder says it
  was adapted from. Both are MIT; Click is BSD-3-Clause.
- `coyim/`: coyim 0.4.1, a GPL-3.0 chat client, compared with
  github.com/agl/xmpp-client at commit 3030ad4, the code coyim names as a
  source in `LICENSE.xmpp-client`. The packages coyim vendors, and their
  licences, are listed in `../benchmark/corpus.txt`.
- `pip/`: pip 26.2.2's vendored folder, `src/pip/_vendor`, compared with
  the 18 releases its `vendor.txt` lists. Only that folder is scanned
  (`--exclude` leaves out pip's own code, tests and docs): the question is
  whether the libraries pip ships carry their licences.

These runs are why 0.2.1 reads licence files kept with the code. Remixware
0.2.0 read notice files only at a project's top, and the copied file's own
header, so it reported the copies in typer 0.27.3 and coyim as "attribution
missing" although their licences sit next to them.
