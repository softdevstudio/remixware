# Dependency gate case studies

`remixware deps` run on real dependency changes from the git history of
public projects, for the case studies and the use examples on
remixware.com. `scripts/deps_case_studies.sh REMIXWARE CACHE` writes
everything here except `inputs/`: it fetches each project shallowly at a
commit and its parent and compares the two with `--base-ref`. All runs are
offline (no `--online`): a publisher check measures "recent" from today,
so it says nothing useful about a release from years ago. Nothing here is
the projects' code, only what Remixware printed. `environment.txt` names
the binary (the released one, from releases/latest).

Each folder has `commits.txt` (project, base and head hashes, the manifest
diff), `gate.txt` and `gate.json` (the full run), `gate-quiet.txt`, and a
`summary.txt` with the counts the site quotes. Each `.txt` starts with the
command and ends with the exit code.

- `typescript/`: microsoft/TypeScript 4139807e7, "Add build via esbuild".
  `gate-allowed.txt` reruns it with `inputs/esbuild.json`, an example
  policy, not TypeScript's.
- `hugo/`: gohugoio/hugo c7165589, "Add a "deploy" command".
  `gate-allowed.txt` uses `inputs/go-project-modules.json`, an example.
- `serve/`: pytorch/serve b260776b5, "Enable opt-6.7b benchmark on inf2".
- `recipes/`: the use examples. A small git repository whose main holds
  TypeScript's lockfiles before esbuild and `inputs/demo-main.json` as its
  policy, a branch with the lockfiles after (`add-esbuild`) and one that
  also allows its own packages (`allow-itself`); and a monorepo made from
  the three projects' lockfiles (web/, api/, ml/). `actions-run.txt` runs
  the run blocks of `inputs/deps.yml` as written, downloading the latest
  release.

The policies in `inputs/` are examples written for these runs; none of the
projects uses Remixware.
