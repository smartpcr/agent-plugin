# M1 evidence runbook

## What this file is

**Purpose.** This runbook tells an operator how to prove a commit of milestone M1 on
the two local lanes, how to run each manual check, and how to write both down so that
a reviewer can repeat them. It is the result of plan Stage 12.4 of
[gap-closure-plan.md](../gap-closure-plan.md) (gaps M1-G-CLOSE-5, -6, -13 and
M1-G-GATE-22).

**Who reads it.**
- **Operators** run the lane scripts and the manual checks, and fill in the records.
- **Reviewers** check a record against this file: every field present, every value
  traceable to a run.
- **Engineers** cite a record's fields when they close a stage.

**What it does not repeat.** How a lane is provisioned and what each lane script
checks are written once, in [tools/harness-gate/README.md sec. Lanes](../../../../tools/harness-gate/README.md#lanes)
and in the scripts themselves. This file links to them.

### Words used here

- **Lane.** One system on which the gate of record runs. M1 has two: **Windows
  native** (`win`) and **Linux under WSL** on the same Windows host (`wsl`). macOS is
  out of scope (`platforms-windows-wsl`): a macOS row reads "out of scope
  (`platforms-windows-wsl`)", never a pass and never a skip. The hosted CI lanes are
  regression signals, not evidence of record (M1-D-GATE-21 and M1-D-TOOL-5, open; the
  plan's default).
- **Run record.** The text file one lane run writes: a header that names the commit,
  the system and the toolchain, then the full gate output, then the exit status and a
  SHA-256 of the file. It replaces a CI run id (`local-run-record-format`).
- **Run-record id.** `<lane>-<sha7>-<UTC yyyymmddThhmmZ>`, for example
  `wsl-cf64524-20261122T0700Z`. The file is `runs/<run id>.txt` in this directory.
- **Evidence record.** One `DH-S-n.md` file per impl.md stage DH-S-1 to DH-S-11. It
  holds the history of the stage as it was built and, at its end, the eight fields of
  impl.md sec. 7.3, which the closure fills in.
- **Manual check.** A person runs a check at a recorded commit, watches it and writes
  down what was seen, labelled `manual` (impl.md sec. 1.4 rule 3). A manual check
  supports the status "built". It never ticks a scenario box, and it is never a gate.
- **Closure status.** The line `Closure status: open` or `Closure status: closed at
  <sha>` near the top of each record. Only plan step 18.1.14 writes "closed at".

### How the parts fit

```text
lane script (wsl.sh / win.ps1)
  -> preflight (stops with exit 3, naming the first unmet check)
  -> harness-gate --evidence runs/<run id>.txt   (writes the run record)
       |
       v
DH-S-n.md "Evidence record (impl.md sec. 7.3)"
  - "Run identifiers" cites the run-record id and SHA-256
  - "Results" copies the PASS lines and the coverage lines
  - "Manual checks" register: one row per impl.md step, filled at the closure commit
```

## The eleven records

| Record | impl.md stage | Steps | Closure status |
| --- | --- | --- | --- |
| [DH-S-1.md](DH-S-1.md) | [DH-S-1](../../../harness/impl.md#dh-s-1) repository, modules and gates | 1.1 to 1.9 | open |
| [DH-S-2.md](DH-S-2.md) | [DH-S-2](../../../harness/impl.md#dh-s-2) cordis changes | 2.1 to 2.8 | open |
| [DH-S-3.md](DH-S-3.md) | [DH-S-3](../../../harness/impl.md#dh-s-3) loader changes | 3.1 to 3.9 | open |
| [DH-S-4.md](DH-S-4.md) | [DH-S-4](../../../harness/impl.md#dh-s-4) SDK codec, schema and leaves | 4.1 to 4.14 | open |
| [DH-S-5.md](DH-S-5.md) | [DH-S-5](../../../harness/impl.md#dh-s-5) session core contracts | 5.1 to 5.13 | open |
| [DH-S-6.md](DH-S-6.md) | [DH-S-6](../../../harness/impl.md#dh-s-6) boot, launch, egress, secrets, test kit | 6.1 to 6.17 | open |
| [DH-S-7.md](DH-S-7.md) | [DH-S-7](../../../harness/impl.md#dh-s-7) SQLite store, leases, recovery | 7.1 to 7.6 | open |
| [DH-S-8.md](DH-S-8.md) | [DH-S-8](../../../harness/impl.md#dh-s-8) machine state, views, spill | 8.1 to 8.7 | open |
| [DH-S-9.md](DH-S-9.md) | [DH-S-9](../../../harness/impl.md#dh-s-9) first model provider, retry, usage | 9.1 to 9.3 | open |
| [DH-S-10.md](DH-S-10.md) | [DH-S-10](../../../harness/impl.md#dh-s-10) tool pipeline, approvals, commands | 10.1 to 10.10 | open |
| [DH-S-11.md](DH-S-11.md) | [DH-S-11](../../../harness/impl.md#dh-s-11) agent loop (milestone M1) | 11.1 to 11.7 | open |

103 steps in all. Each record's own `Closure status:` line is the authority; this table
is a copy for orientation.

## Lanes

Provisioning, the clone rule and the preflight checks are in
[tools/harness-gate/README.md sec. Lanes](../../../../tools/harness-gate/README.md#lanes).
The two scripts are:

- [`tools/harness-gate/lanes/wsl.sh`](../../../../tools/harness-gate/lanes/wsl.sh): the
  Linux lane, run inside the WSL distribution from a WSL-native clone on ext4, never
  from `/mnt/e`. It runs `rehearse` first (while the tree is still clean), then the gate
  with `--evidence`.
- [`tools/harness-gate/lanes/win.ps1`](../../../../tools/harness-gate/lanes/win.ps1):
  the Windows native lane, run from a non-elevated PowerShell in the checkout. It runs
  the gate with `--evidence`.

Exit status of both scripts: `0` every gate passed, `1` a gate failed, `2` a setup error
of the gate (for example a toolchain that is not the floor, or a dirty tree), `3` a
preflight check failed. The WSL script exits with the gate's status, else the
rehearsal's.

### Verification lines

Before trusting a run, the operator runs these lines in the same shell and sees the
stated value. They repeat, by eye, what the preflight checks by script, and add the
facts the run record does not hold. A line that prints anything else means the run is
not evidence: fix the lane and run again.

| Lane | Run | Must see | Why |
| --- | --- | --- | --- |
| WSL | `go env GOOS GOARCH` | `linux` and `amd64` | The Linux toolchain is in use, not the Windows `go.exe` through interop (`appendWindowsPath=false`). |
| Windows | `go env GOOS GOARCH` | `windows` and `amd64` | The native lane. |
| both | `go version` (Windows: after `$env:GOTOOLCHAIN = 'go1.27.4'`) | `go1.27.4` | The go floor of `src/harness/go.mod` (`go-floor-1-27-4`, superseding `lane-toolchain-floor`'s go1.26.0). |
| WSL | `df -T .` (and `stat -f -c %T .`) | `ext4` (`stat` prints `ext2/ext3`) | DrvFs and 9p (`/mnt/e`) break the privacy tests, `flock` and the budget gate. |
| WSL | `df -T "${TMPDIR:-/tmp}"` | `ext4` | Temporary homes of the tests live there. |
| WSL | `id -u` | not `0` | Root bypasses the permission tests. |
| both | `git status --porcelain` | nothing (empty output) | A run record is refused on a dirty tree; a reviewer must be able to check out the same bytes. |
| both | `git config --get core.autocrlf` | nothing, or `false` | `.gitattributes` forces `eol=lf`. |
| both | `go env CC`, then `gcc --version` | a compiler path or name, then a gcc version line | `-race` needs cgo and so a C compiler. Without one the `test` gate fails with `race: no C compiler` (EN-6); it is a failure, never a skip. |
| both | `go env CGO_ENABLED` | the value, recorded as seen | The gate sets `CGO_ENABLED=1` for the `test` gate itself; the ambient value is recorded for the reader. |
| WSL | `systemctl --user status` | a status, not "Failed to connect to bus" | With a user service manager the Linux user-scope path is used. Without it the process-group fallback (register entry U-K1-1) is in use, and the record says so (EN-5). |
| WSL (from Windows) | `wsl.exe --version` | the WSL and kernel versions | Identifies the WSL build; the run record does not hold it. |
| Windows | `cmd /c ver` | the Windows build | Repeats the run record's `host os` line. |

Before a run whose budget gate matters (every run of record), run `wsl --shutdown` from
Windows first: a long-running WSL VM drifts its clock (tools/harness-gate README).

## The run record

### What `--evidence` writes, and what the operator adds

The gate writes most fields itself (`tools/harness-gate/evidence.go`, README sec.
"Evidence files"). The operator adds the rest by hand, in the record's "Run
identifiers" and "Environment and fixture" fields. Every value is copied, never retyped
from memory.

| Field (plan 12.4.2) | Written by `--evidence` | Filled by the operator |
| --- | --- | --- |
| Run-record id `<lane>-<sha7>-<UTC yyyymmddThhmmZ>` | -- (the lane script builds it and uses it as the file name) | copied from the file name |
| Operator | -- | the person's name |
| Full commit SHA | `commit:` | -- |
| Empty `git status --porcelain` | `tree: clean (git status --porcelain --untracked-files=all is empty)`; a dirty tree is refused with exit 2 | -- |
| Command | `command:` | -- |
| GOOS/GOARCH | `goos/goarch:` | -- |
| Go version and floor | `go version:`, `go floor:`, `goroot:` | -- |
| C compiler and gcc version | `cc:` (`go env CC`) and `cc --version:` (first line) | -- |
| Host OS build | `host os:` (`cmd /c ver` on Windows, `uname -srvm` on Linux) | -- |
| WSL distribution, `/etc/os-release` PRETTY_NAME, kernel | `wsl distro:`, `os-release:`, `kernel:` (`uname -r`), Linux only | -- |
| `wsl.exe --version` (WSL lane) | -- | output of `wsl.exe --version`, run from Windows |
| `CGO_ENABLED` | -- | `go env CGO_ENABLED` |
| `TMPDIR` (WSL lane) | -- | from the script's `preflight passed` line, or `echo ${TMPDIR:-/tmp}` |
| `id -u` (WSL lane); not elevated (Windows lane) | -- | from the script's `preflight passed` line (`uid N`; `not elevated`) |
| Checkout file system type | -- | from the `preflight passed` line (`ext4 checkout`; `NTFS checkout` or `ReFS checkout`) and `df -T .` |
| Whether `systemctl --user` exists (WSL lane) | -- | from the verification line above (EN-5) |
| Children's environment | `children: CI=1` (goldens refuse `-update`) | -- |
| Start and end times (UTC) | `started:`, `ended:` | -- |
| Per-gate PASS lines | inside `--- output ---`: `harness-gate: PASS <gate> (<time>)`, ending `harness-gate: PASSED all <N> gates` or `harness-gate: FAILED at gate <gate>` | copied into "Results" |
| Coverage table | inside `--- output ---`: one `coverage: <package> <percent>%` line per package | copied into "Results" (or cited by line range) |
| Exit status | `exit:` | -- |
| SHA-256 of the record | `sha256:` (of every byte before that line) | checked: `head -n -1 runs/<run id>.txt \| sha256sum` prints the same value |
| ASCII only (DC-6) | -- | checked: `grep -nP '[^\x00-\x7F]' runs/<run id>.txt` prints nothing |
| Rehearsal record (WSL lane) | `runs/<run id>-rehearse.txt`, written by `wsl.sh`: commit, start, one line per binary (`vcs.modified=false`), exit, end, `sha256:` | its SHA-256 is cited beside the gate record's |
| CI run ids | -- | supplementary only, when a hosted run exists for the same commit |
| macOS | -- | the row reads "out of scope (`platforms-windows-wsl`)" |

### The template

Each run of record is cited in a record by this table (one column per lane). Copy it into
the record's "Run identifiers" field and fill every cell; a cell that cannot be filled
says why.

```text
| Field | WSL lane | Windows lane |
| --- | --- | --- |
| Run-record id | wsl-<sha7>-<yyyymmddThhmmZ> | win-<sha7>-<yyyymmddThhmmZ> |
| Record file and SHA-256 | runs/<run id>.txt, <sha256> | runs/<run id>.txt, <sha256> |
| Rehearsal file and SHA-256 | runs/<run id>-rehearse.txt, <sha256> | -- (WSL lane only) |
| Operator | <name> | <name> |
| Commit | <full sha> | <full sha> |
| git status --porcelain | empty | empty |
| System | <os-release PRETTY_NAME>, kernel <uname -r>, <wsl.exe --version> | <cmd /c ver> |
| Go | <go version> | <go version> |
| CC and gcc | <cc>, <cc --version> | <cc>, <cc --version> |
| CGO_ENABLED, TMPDIR, id -u | <value>, <path>, <uid> | <value>, --, not elevated |
| Checkout file system | ext4 | NTFS or ReFS |
| systemctl --user | present or absent (EN-5) | -- |
| Started, ended (UTC) | <started>, <ended> | <started>, <ended> |
| Exit status | <exit> | <exit> |
| Result | PASSED all <N> gates, or FAILED at gate <gate> | same |
| CI run ids (supplementary) | <ids or none> | <ids or none> |
| macOS | out of scope (platforms-windows-wsl) | |
```

impl.md sec. 7.3 points here for local-lane runs (a pointer line, under rule DC-2).

## Manual checks

### The register

Each record has a "Manual checks register" under its "Manual checks" field: one row per
impl.md step (103 rows in all). The columns are:

| Column | Meaning |
| --- | --- |
| Step | The impl.md step. |
| Check | The check impl.md's "Manual checks" line names for the step, or "rule-3 default" when it names none. |
| Procedure | How to run it: its kind (below) and the exact command or action. Pre-filled. |
| Systems | Where it must run. "Windows, WSL" where impl.md says "each operating system"; otherwise one lane is enough, and the operator records which. |
| Operator | The named person who ran and watched it. Blank until run. |
| Commit | The full commit SHA at which it ran. Blank until run. |
| Seen | What the person saw: the printed lines, counts or outcome, per system. Blank until run. |
| Label | `manual`. The label says what kind of record the row is; a row whose Commit is blank has not run and counts as open. |

Plan step 18.1.5 runs every row at the frozen M1 closure commit and fills Operator,
Commit and Seen. Nothing is carried over from an older commit: the historical manual
records earlier in each file stay as history and do not fill a row.

### Kinds of procedure

- **composed test.** A named Go test that composes the real component, in a tree with
  the test kit where the component is a harness one. The operator runs it with `-v` at
  the commit, watches it, and records the lines that show the step's "Result" behaviour.
  This is how the rule-3 default is run (impl.md sec. 1.4 rule 3: "start the step's
  component in a composed tree with the test kit, exercise it once by hand, and record
  that the behaviour of the deliverable's Result line was seen").
- **probe.** A program or test behind the build tag `manual` that reaches a real outside
  system, such as the user's signed-in Claude or Copilot client. The default build and
  the gate never compile it.
- **by hand.** A person acts at a real machine: saves a file from a real editor, writes
  a layer file, reads a workflow file.
- **seeded gate.** `harness-gate` run on a copy of a module with one seeded violation;
  the person records the gate it names and its exit status.

**Which of these count (M1-D-CLOSE-2).** The decision is `open`. The register is written
for the plan's default, option A: **a scripted composed test or probe, run at the
recorded commit and watched by a named person, counts as a manual check**, and the
rule-3 default covers each step whose stage names no check. If the owner chooses option
B instead (only an interactive exercise counts), each "composed test" and "probe" row
also needs an interactive run by hand before it is filled, and this paragraph changes.

### The signed-in route checks (DH-S-9 and DH-S-11)

Under `models-signed-in-claude-copilot` and M1-D-CLOSE-3 (a boundary route, in force),
the real-model checks of DH-S-9 and DH-S-11 run on Claude and GitHub Copilot through the
user's own signed-in client, with no API key:

- **DH-S-9.** One streamed request per client while signed in, and one with a
  **signed-out or expired session** in place of the old "bad key": exactly one
  `assistant/attempt` settlement with `llm/sign-in-required` (or `llm/auth`), no retry
  (no `llm/retry` event) and no fallback (no attempt on another route). The probe is
  built by plan step 16.5.6 and run by the runbook of
  [plan Stage 16.5](../gap-closure-plan.md#stage-165-signed-in-client-routes-and-the-dh-s-9-manual-check).
- **DH-S-11 (milestone M1).** One turn with one test tool and a mid-stream cancel, per
  route. The probe is retargeted by plan steps 17.4.1 and 17.4.2 and run by 17.4.3
  ([plan Stage 17.4](../gap-closure-plan.md#stage-174-m1-on-the-signed-in-route-and-dh-s-11-closure)).
- **Which route must pass (M1-D-LOOP-6, lead settlement).** Claude must pass. Copilot is
  run and recorded either way; a Copilot failure is recorded as seen and stays open,
  never a pass.
- **Who.** The owner or operator on their own workstation, signed in as themselves;
  never CI or an agent session. Every printed line passes the mask and the token-shape
  filter, and a second person reviews the record before it is committed (R-11).
- **If a vendor is unreachable.** Record `not run: <client>, <date>, <host>, <error
  line>`, never a fixture. The row stays open.

Until 16.5 and 17.4 land, these rows cannot run; their procedure names the step that
builds the probe.

## The evidence record headings

Each `DH-S-n.md` ends with the section "Evidence record (impl.md sec. 7.3)", holding the
eight fields of impl.md sec. 7.3 as headings, in this order:

1. **Source and build identity** -- the commit (one commit for the three modules), Go
   version, the binary's checksum and embedded version where a binary is built.
2. **Environment and fixture** -- the lane, system and version, toolchain and compiler,
   sandbox or tree owner reported (EN-5), external programs and their versions, the
   fixture used.
3. **Exact tests and commands** -- the gate runner's command, the `go test` command
   lines, the value of `DSH_REQUIRE_SCENARIOS`.
4. **Run identifiers** -- the run-record ids of both lanes in the template above, any
   CI run ids as supplementary, scenario ids, session and request ids.
5. **Results** -- pass or fail per gate and per test; "skipped", "blocked" and "not run"
   are written as such and count as open.
6. **Manual checks** -- the register.
7. **Cleanup evidence** -- the before-and-after comparison the stage names, or what was
   kept because cleanup failed.
8. **Open items** -- held-open deliverables (`H-n`), deliverables built but not done
   because their scenario runs later, and any scenario left open with its reason.

Until the closure commit (plan step 18.1.7), each field points to the historical
sections above it, which stay exactly as they were recorded; existing results are never
rewritten. The earlier hosted runs (run 44670956416 in DH-S-1, run 44860891149 in DH-S-6)
stay as rows labelled historical; their macOS jobs are historical and out of scope
(`platforms-windows-wsl`).

## Statuses kept apart

- **built** and **done** are different (plan "Statuses"). A manual record supports
  "built"; only a passed `DH-E` scenario makes a deliverable "done".
- **manual** never ticks a `Test Scenarios` box and is never a gate.
- **not closure evidence** labels a run made to test the lanes or the runbook, such as
  the first lane runs of plan step 12.3.9 and the dry run of 12.4.7. They are kept, but a
  stage never closes on them.
- **historical** labels a record made before a decision changed its meaning, such as
  every macOS result. It is kept as written and counts as neither a pass nor a skip.
