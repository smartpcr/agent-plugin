"""Render the 'Done at M1' and 'Built, not done' markdown tables (plan 18.1.11, 18.1.12).

Usage: python -I closure_tables.py <repo root> <done_at_m1.tsv> <out.md>
"""
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
rows = [line.split("\t") for line in pathlib.Path(sys.argv[2]).read_text(encoding="utf-8").splitlines()[1:]]
index = (root / "test/harness/acceptance-index.yaml").read_text(encoding="utf-8")

# DH-E id -> executing stage (the key the entry sits under).
runs_in = {}
stage = None
for line in index.splitlines():
    m = re.match(r"\s{2}(DH-S-\d+):", line)
    if m:
        stage = m.group(1)
    m = re.match(r"\s+- id: (DH-E-[A-Z]+-\d+)", line)
    if m and stage:
        runs_in[m.group(1)] = stage

out = ["### Done at M1", "",
       "Plan 18.1.12. The deliverables of impl.md steps 1.1 to 11.7 that M1 claims **done**: each",
       "Done-when line names no `DH-E-*` scenario and no deferred `DH-T` ID, the deliverable is",
       "not held open, and its tagged tests pass on both lanes at `593ceaa` (Windows",
       "`win-593ceaa-20261123T2122Z`, WSL `wsl-593ceaa-20261123T2122Z`). The list is derived by",
       "`evidence/runs/manual-593ceaa/done_at_m1.py` from impl.md, the area specs' Done-when",
       "lines, the trace-tests deferrals and the H-n tables. **Proposed; the owner signs it as",
       "`m1-done-claims` in [decisions.md](../decisions.md).**", "",
       "| Step | Deliverable | Done-when tests |", "| --- | --- | --- |"]
done = [r for r in rows if r[2] == "DONE"]
for step, did, _, why in done:
    out.append(f"| {step} | {did} | {why.replace('tests: ', '')} |")
out += ["", f"{len(done)} of {len(rows)} deliverables.", "",
        "### Built, not done",
        "",
        "Plan 18.1.11. Every other deliverable of steps 1.1 to 11.7 is **built, not done**: it is",
        "recorded \"built, done when <the item named> passes in <stage>\". A `DH-E` scenario runs",
        "in the stage the acceptance index keys it under; a deferred test passes in the stage its",
        "`later` directive names; a held-open part closes at its H entry's closing step.", "",
        "| Step | Deliverable | Waits for | Runs in |", "| --- | --- | --- | --- |"]
built = [r for r in rows if r[2] == "built"]
for step, did, _, why in built:
    es = re.findall(r"DH-E-[A-Z]+-\d+", why)
    where = sorted({runs_in.get(e, "not indexed") for e in es})
    extra = []
    if "deferred" in why:
        extra.append("the deferral's stage")
    if "held open" in why:
        extra.append("the H entry's closing step")
    out.append(f"| {step} | {did} | {why} | {', '.join(where + extra)} |")
out += ["", f"{len(built)} of {len(rows)} deliverables.", ""]
pathlib.Path(sys.argv[3]).write_text("\n".join(out), encoding="utf-8")
print(len(done), len(built), sum(1 for r in built if 'not indexed' in r[3]))
