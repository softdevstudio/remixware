# Third-party licenses

## Go

Remixware is written in Go and uses only the Go standard library. The
`remixware` binaries contain the Go runtime and standard library, which are
distributed under this license:

```
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## SCANOSS winnowing

`wfp.go` makes the fingerprints the SCANOSS service reads. Its algorithm,
settings and rules follow `winnowing.py` in scanoss.py
(https://github.com/scanoss/scanoss.py), which is distributed under this
license:

```
Copyright (c) 2021, SCANOSS

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

`testdata/scanoss/wfp/expected.wfp` holds fingerprints written by scanoss.py
for the test files next to it, made with `scripts/wfp-golden.py`.
`testdata/scanoss/scanoss-py-result.json` is `tests/data/result.json` from
scanoss.py, SCANOSS's sample of its service's answers, unchanged. Both are
under the license above.

## Licence texts

`licensetexts/` holds the standard texts of eight licences: MIT,
BSD-2-Clause, BSD-3-Clause, ISC, Zlib, BSL-1.0, Apache-2.0 and MPL-2.0.
They were taken unchanged from the data of the SPDX License List
(https://github.com/spdx/license-list-data), which the SPDX Legal Team, a
Linux Foundation project, maintains. Remixware copies them into the
third-party notices it writes. Each text remains the work of the licence's
author, such as the Apache Software Foundation for the Apache License 2.0
and the Mozilla Foundation for the Mozilla Public License 2.0.
