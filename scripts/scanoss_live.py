#!/usr/bin/env python3
"""A check of `remixware --scanoss` against the live SCANOSS service, for
when the live check workflow can't get an answer (the free service turns
away GitHub's runners once their monthly limit is used up).

    python3 scripts/scanoss_live.py request WORKDIR
        Builds the test project in WORKDIR (copies of logrus v1.9.3, as in
        .github/workflows/scanoss-live.yml), captures the request remixware
        sends for it with a stand-in server, and writes WORKDIR/request.wfp
        and WORKDIR/submit.js. Run submit.js in a browser tab on
        https://api.osskb.org/ from a connection the service still answers:
        it posts the same fingerprints in the same form, and the tab then
        shows the answer. Save that text as WORKDIR/answer.json.

    python3 scripts/scanoss_live.py record WORKDIR
        Replays answer.json to remixware through a stand-in server and
        writes what remixware reports to test/results/live. The answer
        itself isn't kept: the service's terms allow its information to be
        used for software identification only.

Needs git (for logrus), Go and network access for the first step.
"""
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import uuid
from email.parser import BytesParser
from email.policy import default
from http.server import BaseHTTPRequestHandler, HTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "test", "results", "live")


def build(work):
    """Builds remixware and the test project; returns the binary's path."""
    os.makedirs(work, exist_ok=True)
    binary = os.path.join(work, "remixware")
    subprocess.run(["go", "build", "-o", binary, "./cmd/remixware"], cwd=ROOT, check=True)
    logrus = os.path.join(work, "logrus")
    if not os.path.isdir(logrus):
        subprocess.run(["git", "clone", "-q", "-c", "advice.detachedHead=false", "--depth", "1",
                        "--branch", "v1.9.3", "https://github.com/sirupsen/logrus.git", logrus], check=True)
    proj = os.path.join(work, "proj")
    shutil.rmtree(proj, ignore_errors=True)
    os.makedirs(os.path.join(proj, "internal", "logging"))
    os.makedirs(os.path.join(proj, "internal", "fields"))
    # A whole file, and part of another inside code of our own.
    shutil.copy(os.path.join(logrus, "text_formatter.go"), os.path.join(proj, "internal", "logging", "format.go"))
    entry = open(os.path.join(logrus, "entry.go")).read()
    fn = re.search(r"^func \(entry \*Entry\) WithFields.*?^}\n", entry, re.M | re.S).group(0)
    with open(os.path.join(proj, "internal", "fields", "fields.go"), "w") as f:
        f.write("package fields\n\n// Count reports how many fields a map holds.\n"
                "func Count(m map[string]interface{}) int { return len(m) }\n\n" + fn)
    with open(os.path.join(proj, "LICENSE"), "w") as f:
        f.write("Copyright 2026 Example\n\nLicensed under the Apache License, Version 2.0.\n")
    return binary


def serve(handle):
    """Starts a stand-in SCANOSS service; handle(headers, body) returns the answer."""
    class H(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers["Content-Length"]))
            answer = handle(self.headers, body)
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(answer)))
            self.end_headers()
            self.wfile.write(answer)

        def log_message(self, *a):
            pass

    srv = HTTPServer(("127.0.0.1", 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_address[1]}"


def scan(binary, work, url, *extra):
    return subprocess.run([binary, "scan", os.path.join(work, "proj"), "--scanoss", "--scanoss-url", url,
                           "--license", "Apache-2.0", *extra], capture_output=True, text=True)


def request(work):
    binary = build(work)
    got = {}

    def capture(headers, body):
        msg = BytesParser(policy=default).parsebytes(
            b"Content-Type: " + headers["Content-Type"].encode() + b"\r\n\r\n" + body)
        for part in msg.iter_parts():
            if part.get_filename():
                got["wfp"] = part.get_payload(decode=True).decode()
            else:
                got[part.get_param("name", header="content-disposition")] = part.get_content().strip()
        return b"{}"

    srv, url = serve(capture)
    r = scan(binary, work, url)
    srv.shutdown()
    if r.returncode != 0 or "wfp" not in got:
        sys.exit(f"capturing the request failed: {r.stderr}")
    with open(os.path.join(work, "request.wfp"), "w") as f:
        f.write(got["wfp"])
    js = f"""const wfp = {json.dumps(got["wfp"])};
const f = document.createElement('form');
f.method = 'POST'; f.action = 'https://api.osskb.org/scan/direct'; f.enctype = 'multipart/form-data';
const fi = document.createElement('input'); fi.type = 'file'; fi.name = 'file';
const dt = new DataTransfer(); dt.items.add(new File([wfp], '{uuid.uuid4()}.wfp', {{type: 'application/octet-stream'}})); fi.files = dt.files;
const fmt = document.createElement('input'); fmt.type = 'hidden'; fmt.name = 'format'; fmt.value = {json.dumps(got.get("format", "plain"))};
f.append(fi, fmt); document.body.append(f);
setTimeout(() => f.submit(), 50);
'submitting ' + wfp.length + ' bytes for ' + (wfp.match(/^file=/gm) || []).length + ' files'
"""
    with open(os.path.join(work, "submit.js"), "w") as f:
        f.write(js)
    print(f"wrote {work}/request.wfp and {work}/submit.js; save the service's answer as {work}/answer.json")


def record(work):
    binary = build(work)
    answer = open(os.path.join(work, "answer.json"), "rb").read()
    json.loads(answer)
    sent = {}

    def replay(headers, body):
        sent["body"] = body
        return answer

    srv, url = serve(replay)
    findings = os.path.join(work, "findings.json")
    r = scan(binary, work, url, "--json", findings)
    srv.shutdown()
    if r.returncode != 0:
        sys.exit(f"the replay failed: {r.stderr}")
    # The answer only fits the fingerprints it was given for.
    if open(os.path.join(work, "request.wfp")).read().encode() not in sent["body"]:
        sys.exit("remixware sent other fingerprints than request.wfp, which the answer is about")
    proj = os.path.join(work, "proj")
    clean = lambda s: (s.replace(proj, "proj").replace(work, "WORKDIR").replace(url, "http://127.0.0.1:PORT")
                       .replace(url[len("http://"):], "127.0.0.1:PORT"))
    os.makedirs(OUT, exist_ok=True)
    version = subprocess.run([binary, "version"], capture_output=True, text=True).stdout.strip()
    with open(os.path.join(OUT, "check.txt"), "w") as f:
        f.write(f"$ {version}: remixware scan proj --scanoss --license Apache-2.0 (answer replayed)\n\n")
        f.write(clean(r.stderr + r.stdout))
    with open(os.path.join(OUT, "findings.json"), "w") as f:
        f.write(clean(open(findings).read()))
    shutil.copy(os.path.join(work, "request.wfp"), os.path.join(OUT, "request.wfp"))
    print(clean(r.stdout))
    print(f"recorded in {OUT}")


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[1] not in ("request", "record"):
        sys.exit(__doc__)
    {"request": request, "record": record}[sys.argv[1]](os.path.abspath(sys.argv[2]))
