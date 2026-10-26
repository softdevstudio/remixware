#!/usr/bin/env python3
"""Records the case studies on remixware.com: Remixware run on real projects
that took code from others, at pinned versions.

    python3 scripts/case_studies.py REMIXWARE CACHE OUT

REMIXWARE is the binary to run, CACHE a folder outside the repository for
the clones and downloads, OUT the folder for the results
(test/results/case-studies). scripts/record-results.sh runs it. The first
run needs git and network access: projects are cloned from GitHub, and the
releases pip vendors are downloaded from PyPI and checked against the
SHA-256 sums PyPI publishes for them.

  typer   typer 0.12.5 compared with Click 8.1.7, and typer 0.27.3 compared
          with Click 8.3.1, the version its typer/_click folder came from.
  coyim   coyim 0.4.1 compared with xmpp-client, whose code it took.
  pip     pip 26.2.2's vendored folder (src/pip/_vendor) compared with the
          releases its vendor.txt lists, downloaded from PyPI.
"""
import collections
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import urllib.request
import zipfile

REMIXWARE, CACHE, OUT = (os.path.abspath(a) for a in sys.argv[1:4])
WORK = os.path.join(CACHE, "case-studies")

GIT = {
    "typer-0.12.5": ("https://github.com/fastapi/typer.git", "0.12.5"),
    "typer-0.27.3": ("https://github.com/fastapi/typer.git", "0.27.3"),
    "click-8.1.7": ("https://github.com/pallets/click.git", "8.1.7"),
    "click-8.3.1": ("https://github.com/pallets/click.git", "8.3.1"),
    "coyim-0.4.1": ("https://github.com/coyim/coyim.git", "v0.4.1"),
    "xmpp-client": ("https://github.com/agl/xmpp-client.git", "3030ad42ee2df255ffc108dc6d04893d9501c863"),
    "pip-26.2.2": ("https://github.com/pypa/pip.git", "26.2.2"),
}


def run(*args, ok=(0,), **kw):
    r = subprocess.run(args, capture_output=True, text=True, **kw)
    if r.returncode not in ok:
        sys.exit(f"{' '.join(args)}: exit {r.returncode}\n{r.stderr}")
    return r


def clone(name):
    url, ref = GIT[name]
    dest = os.path.join(WORK, name)
    if os.path.isdir(dest):
        return dest
    if re.fullmatch(r"[0-9a-f]{40}", ref):
        run("git", "clone", "-q", url, dest)
        run("git", "-C", dest, "-c", "advice.detachedHead=false", "checkout", "-q", ref)
    else:
        run("git", "clone", "-q", "-c", "advice.detachedHead=false", "--depth", "1", "--branch", ref, url, dest)
    shutil.rmtree(os.path.join(dest, ".git"))
    return dest


def pypi(name, version):
    """Downloads a source release from PyPI, checks it and unpacks it."""
    dest = os.path.join(WORK, "pypi", f"{name}-{version}")
    if os.path.isdir(dest):
        return dest
    meta = json.load(urllib.request.urlopen(f"https://pypi.org/pypi/{name}/{version}/json", timeout=60))
    sdist = [u for u in meta["urls"] if u["packagetype"] == "sdist"][0]
    data = urllib.request.urlopen(sdist["url"], timeout=120).read()
    if hashlib.sha256(data).hexdigest() != sdist["digests"]["sha256"]:
        sys.exit(f"{name} {version}: the download doesn't match PyPI's SHA-256")
    part = dest + ".part"
    shutil.rmtree(part, ignore_errors=True)
    os.makedirs(part)
    if sdist["filename"].endswith(".zip"):
        zipfile.ZipFile(io.BytesIO(data)).extractall(part)
    else:
        with tarfile.open(fileobj=io.BytesIO(data)) as t:
            t.extractall(part, filter="data")
    top = os.listdir(part)
    src = os.path.join(part, top[0]) if len(top) == 1 and os.path.isdir(os.path.join(part, top[0])) else part
    os.rename(src, dest)
    shutil.rmtree(part, ignore_errors=True)
    with open(dest + ".sha256", "w") as f:
        f.write(f"{sdist['digests']['sha256']}  {sdist['filename']}\n")
    return dest


def index(name, folders, *flags):
    out = os.path.join("indexes", name + ".rmx")
    run(REMIXWARE, "index", *folders, "--out", out, "--quiet", *flags, cwd=WORK)
    return out


def scan(project, indexes, out, *flags):
    """Runs a scan as a user would and keeps its report; returns the JSON."""
    args = [REMIXWARE, "scan", project]
    for ix in indexes:
        args += ["--index", ix]
    js = os.path.join(WORK, "scan.json")
    r = run(*args, "--json", js, *flags, cwd=WORK)
    with open(out, "w") as f:
        f.write(f"$ remixware scan {project} " + " ".join(f"--index {ix}" for ix in indexes) +
                (" " + " ".join(flags) if flags else "") + "\n\n" + r.stdout)
    with open(js) as f:
        res = json.load(f)
    os.remove(js)
    return res


def statuses(findings):
    c = collections.Counter(f["status"] for f in findings)
    return ", ".join(f"{n} {s.replace('-', ' ')}" for s, n in sorted(c.items(), key=lambda x: -x[1])) or "none"


def credited_in(f):
    m = re.match(r"Credited in (\S+?)[,.] ", f["reason"])
    if m:
        return m.group(1)
    if f["reason"].startswith("Credited in the project's notices"):
        return "the project's notices"
    if f["reason"].startswith("The file keeps"):
        return "the file's own header"
    return ""


def holds(root, needle):
    """Files under root, outside tests, whose text holds needle."""
    hits = []
    for d, dirs, files in os.walk(root):
        dirs[:] = [x for x in dirs if x not in ("tests", "docs", ".git")]
        for name in files:
            p = os.path.join(d, name)
            try:
                with open(p, encoding="utf-8", errors="ignore") as f:
                    if needle in f.read():
                        hits.append(os.path.relpath(p, root))
            except OSError:
                pass
    return sorted(hits)


def typer(version_line):
    out = os.path.join(OUT, "typer")
    os.makedirs(out)
    old, new = clone("typer-0.12.5"), clone("typer-0.27.3")
    c817, c831 = clone("click-8.1.7"), clone("click-8.3.1")
    ix817 = index("click-8.1.7", [c817], "--name", "click", "--version", "8.1.7")
    ix831 = index("click-8.3.1", [c831], "--name", "click", "--version", "8.3.1")
    lines = [version_line, ""]
    for proj, ix, ver in (("typer-0.12.5", ix817, "Click 8.1.7"), ("typer-0.27.3", ix831, "Click 8.3.1")):
        res = scan(proj, [ix], os.path.join(out, proj + ".txt"))
        f = res["findings"]
        inside = [x for x in f if x["file"].startswith("typer/_click/")]
        lines += [f"{proj} (licence {res['project_license']}, {res['files_scanned']} files, {res['lines_scanned']} lines), compared with {ver}:",
                  f"  {len(f)} copies of Click code: {statuses(f)}"]
        if inside:
            lines.append(f"  {len(inside)} of them in typer/_click, {len(f) - len(inside)} elsewhere in typer")
        lines.append(f"  copied lines: {sum(x['lines'][1] - x['lines'][0] + 1 for x in f)}")
        by = collections.Counter(credited_in(x) for x in f if credited_in(x))
        for where, n in by.most_common():
            lines.append(f"  credited in {where}: {n}")
        lines.append(f"  files holding Click's copyright line \"Copyright 2014 Pallets\": {', '.join(holds(os.path.join(WORK, proj), 'Copyright 2014 Pallets')) or 'none'}")
        lines.append("")
    with open(os.path.join(out, "summary.txt"), "w") as fh:
        fh.write("\n".join(lines))


def coyim(version_line):
    out = os.path.join(OUT, "coyim")
    os.makedirs(out)
    clone("coyim-0.4.1")
    clone("xmpp-client")
    ix = index("xmpp-client", ["xmpp-client"], "--name", "github.com/agl/xmpp-client", "--version", "3030ad4")
    res = scan("coyim-0.4.1", [ix], os.path.join(out, "coyim-0.4.1.txt"))
    f = res["findings"]
    files = sorted({x["file"] for x in f})
    lines = [version_line, "",
             f"coyim 0.4.1 (licence {res['project_license']}, {res['files_scanned']} files, {res['lines_scanned']} lines), compared with xmpp-client at 3030ad4:",
             f"  {len(f)} copies of xmpp-client code in {len(files)} files: {statuses(f)}",
             f"  copied lines: {sum(x['lines'][1] - x['lines'][0] + 1 for x in f)}"]
    for where, n in collections.Counter(credited_in(x) for x in f if credited_in(x)).most_common():
        lines.append(f"  credited in {where}: {n}")
    lines += ["  files: " + ", ".join(files), "",
              "The components coyim vendors, and their licences, are in test/results/benchmark/corpus.txt.", ""]
    with open(os.path.join(out, "summary.txt"), "w") as fh:
        fh.write("\n".join(lines))


def pip(version_line):
    out = os.path.join(OUT, "pip")
    os.makedirs(out)
    proj = clone("pip-26.2.2")
    specs = []
    with open(os.path.join(proj, "src", "pip", "_vendor", "vendor.txt")) as f:
        for line in f:
            line = line.split("#")[0].strip()
            if "==" in line:
                specs.append(tuple(line.split("==")))
    releases = [pypi(n, v) for n, v in specs]
    ix = index("pip-vendored", [os.path.relpath(r, WORK) for r in releases])
    # Only the vendored folder: pip's own code is another question.
    excl = ["--exclude", "src/pip/_internal/", "--exclude", "src/pip/*.py", "--exclude", "tests/", "--exclude", "docs/",
            "--exclude", "tools/", "--exclude", "news/", "--exclude", "build-project/", "--exclude", "noxfile.py"]
    res = scan("pip-26.2.2", [ix], os.path.join(out, "pip-26.2.2.txt"), *excl)
    f = res["findings"]
    lines = [version_line, "",
             f"pip 26.2.2 (licence {res['project_license']}), its vendored folder src/pip/_vendor compared with the {len(specs)} releases vendor.txt lists:",
             f"  {len(f)} copies: {statuses(f)}", ""]
    by = collections.defaultdict(list)
    for x in f:
        by[x["file"].split("/")[3]].append(x)
    lines.append(f"  {'folder':18s} {'source':32s} {'licence':28s} {'copies':>6s}  status, credited in")
    for folder in sorted(by):
        xs = by[folder]
        src = sorted({f"{x['source']['component']} {x['source'].get('version', '')}".strip() for x in xs})
        lic = sorted({x["source"]["license"] for x in xs})
        where = sorted({credited_in(x) for x in xs if credited_in(x)})
        lines.append(f"  {folder:18s} {', '.join(src):32s} {', '.join(lic):28s} {len(xs):6d}  {statuses(xs)}"
                     + (f"; {', '.join(where)}" if where else ""))
    missing = [x for x in f if x["status"] == "attribution-missing"]
    if missing:
        lines += ["", "Why attribution is missing:"]
        for reason, n in collections.Counter(x["reason"] for x in missing).most_common():
            lines.append(f"  {n} x {reason}")
    lines += ["", "Releases, from PyPI, with the SHA-256 PyPI publishes:"]
    for r in releases:
        with open(r + ".sha256") as fh:
            lines.append("  " + fh.read().strip())
    with open(os.path.join(out, "summary.txt"), "w") as fh:
        fh.write("\n".join(lines) + "\n")


def main():
    os.makedirs(os.path.join(WORK, "indexes"), exist_ok=True)
    version = run(REMIXWARE, "version").stdout.strip()
    for case in ("typer", "coyim", "pip"):
        shutil.rmtree(os.path.join(OUT, case), ignore_errors=True)
    for case in (typer, coyim, pip):
        print(f"== case study: {case.__name__}")
        case(f"recorded with {version}")
        with open(os.path.join(OUT, case.__name__, "summary.txt")) as fh:
            print(fh.read())


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main()
