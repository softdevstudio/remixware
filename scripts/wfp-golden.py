# Writes the reference fingerprints in testdata/scanoss/wfp/expected.wfp with
# scanoss.py's own Winnowing class, called directly so that its file filters
# don't leave any test file out. TestWFPMatchesScanossPy compares Remixware's
# fingerprints with them. Made with scanoss 1.54.3:
#
#   pip install scanoss
#   python3 scripts/wfp-golden.py testdata/scanoss/wfp > testdata/scanoss/wfp/expected.wfp
import os, sys
from scanoss.winnowing import Winnowing
d = sys.argv[1]
w = Winnowing(quiet=True)
out = []
for f in sorted(os.listdir(d)):
    p = os.path.join(d, f)
    if f.endswith('.wfp') or not os.path.isfile(p):
        continue
    out.append(w.wfp_for_file(p, f))
sys.stdout.write(''.join(out))
