"""Classify the deliverables of impl.md steps 1.1-11.7 for the "Done at M1" list (plan 18.1.12).

A deliverable is a done candidate when its Done-when line names no DH-E scenario, none of
its DH-T IDs is deferred (trace-tests at the frozen commit), and it is not held open.
Usage: python -I done_at_m1.py <repo root> <trace-tests output file>
"""
import re, sys, pathlib

root = pathlib.Path(sys.argv[1])
trace = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
impl = (root / "docs/harness/impl.md").read_text(encoding="utf-8")

# Deliverables per step (first DH-D link of each step line 1.1-11.7).
steps = []
for line in impl.splitlines():
    m = re.match(r"- \[(.)\] \*\*((?:[1-9]|1[01])\.\d+)\*\* (.*)", line)
    if not m:
        continue
    links = re.findall(r"\[(DH-D-[A-Z]+-\d+)\]\(([^)#]+)#([^)]+)\)", m.group(3))
    steps.append((m.group(2), m.group(1) == "x", links))

# Deferred IDs from trace-tests.
deferred = set()
for line in trace.splitlines():
    if "deferred:" in line:
        deferred.update(re.findall(r"DH-T-[A-Z]+-\d+", line))

# Deliverables held open (H-n tables in the evidence records) and H-21 (DH-D-BOOT-6).
held = {"DH-D-BOOT-6": "H-21"}
for f in sorted((root / "docs/stories/harness-DSH-M1/evidence").glob("DH-S-*.md")):
    for line in f.read_text(encoding="utf-8").splitlines():
        m = re.match(r"\| (H-\d+) \| [\d.]+ \| (DH-D-[A-Z]+-\d+) \|", line)
        if m:
            held.setdefault(m.group(2), m.group(1))

def done_when(path, anchor):
    text = (root / "docs/harness" / path).read_text(encoding="utf-8")
    lines = text.splitlines()
    # Find the heading or anchor for the deliverable.
    want = anchor.lower()
    start = None
    for i, l in enumerate(lines):
        if re.search(r'id="%s"' % re.escape(want), l, re.I) or (
            l.startswith("#") and re.sub(r"[^a-z0-9 -]", "", l.lower().lstrip("# ")).replace(" ", "-").startswith(want)):
            start = i
            break
    if start is None:
        return None
    i = start + 1
    if i < len(lines) and lines[i].startswith("#"):
        i += 1  # the deliverable's own heading
    end = min(len(lines), i + 150)
    while i < end:
        l = lines[i]
        if re.search(r'<a id="dh-d-', l, re.I) or (re.match(r"#{1,4} ", l) and "DH-D-" in l):
            break
        if "done when" in l.lower():
            buf = [l.strip()]
            j = i + 1
            while j < len(lines) and lines[j].strip() and not lines[j].lstrip().startswith("- **") and not lines[j].startswith("#"):
                buf.append(lines[j].strip())
                j += 1
            return " ".join(buf)
        i += 1
    return None

seen = set()
rows = []
for step, ticked, links in steps:
    for did, path, anchor in links[:1]:
        if did in seen:
            continue
        seen.add(did)
        dw = done_when(path, anchor)
        if dw is None:
            rows.append((step, did, "NO-DONE-WHEN-FOUND", ""))
            continue
        des = sorted(set(re.findall(r"DH-E-[A-Z]+-\d+", dw)))
        dts = sorted(set(re.findall(r"DH-T-[A-Z]+-\d+", dw)))
        dfr = [t for t in dts if t in deferred]
        why = []
        if des:
            why.append("names " + ", ".join(des))
        if dfr:
            why.append("deferred " + ", ".join(dfr))
        if did in held:
            why.append("held open " + held[did])
        rows.append((step, did, "built" if why else "DONE", "; ".join(why) or "tests: " + ", ".join(dts)))

done = [r for r in rows if r[2] == "DONE"]
print(f"deliverables: {len(rows)}; done candidates: {len(done)}; built only: {sum(r[2]=='built' for r in rows)}; no done-when: {sum(r[2].startswith('NO') for r in rows)}")
for r in rows:
    print("\t".join(r))
