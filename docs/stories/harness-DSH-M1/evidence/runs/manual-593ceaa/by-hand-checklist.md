# Plan 18.1.5: the by-hand and signed-in rows of the manual-checks registers

**Frozen commit:** `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` (branch `m1-phase-18`).

**Who runs it:** the owner or a named operator. Under M1-D-CLOSE-2 A, a check counts as
manual only when a named person runs it at this commit and watches it.

**How to fill a row:** see `docs/stories/harness-DSH-M1/evidence/README.md#manual-checks`.
- Fill **Operator** with the name of the person who ran it.
- Fill **Commit** with the full SHA above.
- Fill **Seen** with the printed lines or counts, for each system.
- Keep **Label** as `manual`.
- A row that was not run, or that failed, stays open. Record what you saw. Never record
  a pass you did not see.

This file covers 8 register rows done by hand and 3 by-hand parts of mixed rows. It also
covers the 7 signed-in probe rows; only the owner runs those. The composed-test and
seeded-gate rows are run by `run-register.sh`, which sits next to this file.

> **No non-model probe rows exist.** All 7 "probe" rows in the registers are
> signed-in model checks: DH-S-9 rows 9.1 and 9.2, and DH-S-11 rows 11.1 to 11.4 and
> 11.7. Three probe tests drive them: `TestManualSignedIn`, `TestManualM1OneTurn` and
> `TestManualM1CancelMidStream`. They are in section S below.

## 0. Common setup: work only in a throwaway worktree

Some checks need a throwaway program or test, such as the DH-S-3 layer test or the
DH-S-4 search and watch programs. Never write these into the main checkout. Make a
throwaway worktree of the frozen commit, and remove it afterwards.

Bash (Git Bash on Windows, or WSL):

```bash
ROOT=/e/work/github/ai/agent-plugin          # WSL: the ext4 clone, e.g. ~/src/agent-plugin
S=/c/Users/xiaodoli.REDMOND/AppData/Local/Temp/claude/095fd4c454d7f56c/32b1560e-0d49-424c-b2a5-d7e6081d5719/scratchpad/manual
                                              # WSL: S=/mnt/c/Users/xiaodoli.REDMOND/AppData/Local/Temp/claude/095fd4c454d7f56c/32b1560e-0d49-424c-b2a5-d7e6081d5719/scratchpad/manual
SHA=593ceaa3efd55d41bf0eeaced5ba7e26acc71966
export GOTOOLCHAIN=go1.27.4 GOWORK=off
T=$(mktemp -d)                                # WSL: TMPDIR must be on ext4
git -C "$ROOT" status --porcelain --untracked-files=all > "$T/status-before.txt"   # keep for the cleanup record
git -C "$ROOT" worktree add --detach "$T/wt" "$SHA"
```

Cleanup, which you also record as the "Cleanup evidence" of the record:

```bash
git -C "$ROOT" worktree remove --force "$T/wt" && git -C "$ROOT" worktree prune
rm -rf "$T"
git -C "$ROOT" status --porcelain --untracked-files=all     # must equal status-before.txt (normally empty)
git -C "$ROOT" worktree list                                # the throwaway worktree is gone
```

---

## DH-S-1

### Row 1.1: by hand, one lane

**Check:** rule-3 default. The three modules build together, and the release build
ignores `go.work`.

**Steps.** Work from a clean clone at the frozen commit. Use the workspace here: do NOT
set `GOWORK=off` for the first two commands.

```bash
T=$(mktemp -d)
git clone --no-local --no-checkout "$ROOT" "$T/clone"
git -C "$T/clone" checkout --detach 593ceaa3efd55d41bf0eeaced5ba7e26acc71966
cd "$T/clone"
export GOTOOLCHAIN=go1.27.4; unset GOWORK
go build ./src/go/... ./src/sdk/... ./src/harness/... ; echo "build exit $?"
go list -m                                                 # three modules
GOWORK=off go -C src/harness build ./cmd/dsh ; echo "release build exit $?"
./src/harness/dsh            # Windows: ./src/harness/dsh.exe   -> prints "dsh (devel)"
cd / && rm -rf "$T"
```

**Record:**
- the exit status of both builds (0);
- the three `go list -m` lines: `github.com/smartpcr/agent-plugin/src/go`, `.../src/sdk`
  and `.../src/harness`;
- the line `dsh` printed (`dsh (devel)`);
- the lane, and the `git -C "$T/clone" rev-parse HEAD` value.

### Row 1.8: by hand, one lane

**Check:** rule-3 default. The CI lanes need no key, and the preflight is credentialed.

**Steps:**
1. Read the `harness` job matrix of `.github/workflows/ci.yml` (line ~136) and the
   `scenarios` job matrix of `.github/workflows/harness-scenarios.yml` (line ~73). At
   `593ceaa` both read `os: [ubuntu-latest, windows-latest]`, with the comment
   `platforms-windows-wsl: hosted regression lanes; no macOS.` Compare them with the
   decided list of M1-D-GATE-21 / M1-D-TOOL-5 in `docs/stories/harness-DSH-M1/decisions.md`.
   Under the plan default, that list is hosted `ubuntu-latest` and `windows-latest` as
   regression signals, with macOS removed. Check the decision's status before you
   record "equal".
2. Run the preflight with no model variable set. It must pass:

   ```bash
   cd "$ROOT"
   env -u DSH_REQUIRE_SCENARIOS -u DSH_DEEPSEEK_API_KEY -u ANTHROPIC_API_KEY -u GH_TOKEN -u GITHUB_TOKEN \
     GOTOOLCHAIN=go1.27.4 GOWORK=off go -C tools/harness-gate run . preflight --root ../.. ; echo "exit $?"
   ```

   Expect `harness-gate preflight: passed; 0 required scenarios` and exit 0.
3. Run it with an unknown required ID. It must fail and name the ID:

   ```bash
   DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9 GOTOOLCHAIN=go1.27.4 GOWORK=off \
     go -C tools/harness-gate run . preflight --root ../.. ; echo "exit $?"
   ```

   Expect `harness-gate preflight: FAILED, no scenario is started:` with a line naming
   `DH-E-FAKE-9`, and a non-zero exit (`go run` reports it as 1).

**Record:** the two matrices as read, whether they equal the decided list, and both
preflight outputs with their exit statuses.

### Row 1.9, by-hand part: WSL, the rehearsal

`run-register.sh --os wsl` runs the seeded `cross-broken` part (see "Needs a decision" in
the report: the gate command cannot reach `cross-compile` on a single-module root).

**Steps.** Use the WSL lane run at `593ceaa`:
`docs/stories/harness-DSH-M1/evidence/runs/wsl-593ceaa-<UTC stamp>-rehearse.txt`. The
script prints its path and counts as "AID" lines if the file is in the tree. If there is
no WSL lane run at `593ceaa`, run the lane (`tools/harness-gate/lanes/wsl.sh` in the
clean ext4 clone). It runs the rehearsal first: `GOWORK=off CI=1 go -C tools/harness-gate
run . rehearse --root ../..`.

Before and after the lane run, in the clone, run:

```bash
git for-each-ref refs/tags > /tmp/tags-before.txt     # before
git for-each-ref refs/tags | diff /tmp/tags-before.txt - && echo "tags unchanged"   # after
git status --porcelain --untracked-files=all          # after: only the new run records
```

**Record:**
- the six binary lines: `dsh-linux-amd64`, `dsh-linux-arm64`, `dsh-darwin-amd64`,
  `dsh-darwin-arm64`, `dsh-windows-amd64.exe` and `dsh-windows-arm64.exe`, each with its
  sha256, `vcs.revision=593ceaa…` and `vcs.modified=false`;
- the file's `exit:` line and its `sha256` line;
- that the tags are unchanged and that nothing was published (no upload, no release).

---

## DH-S-3: rows 3.1, 3.4, 3.5 and 3.8 (one run, by hand, one lane)

All four rows come from one run of a throwaway test in the worktree of section 0. The
test writes `base.json` and a writable `user.json` into a temporary directory. The
`user.json` file has one misspelled key, `disabeld`. The test loads both files through
`loader.LayerSetComponent`, base first and then user.

The layer files the test writes:

```text
base.json:
[
  { "insert": [
    { "id": "good", "name": "EchoPlugin", "config": { "text": "good" } }
  ] }
]

user.json:
[
  { "insert": [
    { "id": "broken", "name": "EchoPlugin",
      "disabeld": true }
  ] }
]
```

**Steps:**

```bash
cp "$S/dhs3/zz_manual_layers_test.go" "$T/wt/src/go/loader/"
cd "$T/wt"
go -C src/go test -count=1 -v -run '^TestManualTwoHandComposedLayersOneBroken$' ./loader/
rm "$T/wt/src/go/loader/zz_manual_layers_test.go"      # then remove the worktree (section 0)
```

The test source is `zz_manual_layers_test.go` in the `dhs3` folder next to this file. It
uses the package's own helpers (`Quiet`, `Echo`, `Log`, and `listen` from
`nested_test.go`), so it must sit in `src/go/loader/` of the worktree. It is checked to
compile at `593ceaa`; it has not been run.

**What to record**, from the `t.Logf` lines. The historical record at `2026-11-20` saw
the lines below, with `<TMP>` standing for the temporary directory:

| Row | Record |
| --- | --- |
| 3.1 | The `error:` line and its `<file>:<line>:<col>`. Expected: `error: loader: <TMP>/user.json:4:7: [0].insert[0].disabeld: unknown key` (line 4, column 7: the misspelled key). |
| 3.4 | `echo: +good`: the entry of `base.json` still loads. |
| 3.5 | `layer: label=user writable=true ...` and `layer: label=base writable=false ...`: only `user.json` is writable. Also `unchanged on disk: base.json = true` and `user.json = true`: neither file changed. |
| 3.8 | `notices: 1` and the one `notice:` line with the issue it carries. Expected: `notice: created=[good] issues=[<TMP>/user.json:4:7: [0].insert[0].disabeld: unknown key]`. |

**Cleanup:** remove the test file and the worktree (section 0). The test's temporary
directory is removed by `t.TempDir`. Record that `git status --porcelain` of the main
tree is unchanged.

---

## DH-S-4

### Row 4.8: by hand, Windows and WSL. Search on a real repository matches Git

**Check:** the set that `search.List` reports equals the set Git reports
(`git ls-files --cached --others --exclude-standard`).

**The throwaway program.** Save it as `$T/wt/src/harness/internal/zzmanual/main.go`, in
the worktree only:

```go
// Throwaway program for the DH-S-4 search manual check: prints search.List of a
// directory, hidden names included, ignore files applied, no limits.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/search"
)

func main() {
	src, err := search.OpenDir(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer src.Close()
	paths, sum, err := search.List(context.Background(), search.FilesQuery{From: src, Hidden: true, Limits: search.Limits{MaxMatches: -1, MaxFiles: -1}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, p := range paths {
		fmt.Println(p)
	}
	fmt.Fprintf(os.Stderr, "summary: %+v\n", sum)
}
```

**Build it**, in the worktree:

```bash
mkdir -p "$T/wt/src/harness/internal/zzmanual"   # then save main.go there
(cd "$T/wt/src/harness" && go build -o "$T/dsh-manual-list" ./internal/zzmanual)   # Windows: -o "$T/dsh-manual-list.exe"
```

**Compare.** Define this function in the same shell, and run it once for each directory
below:

```bash
compare() {  # $1 = directory, $2.. = extra git -c options
  local dir=$1; shift
  (cd "$dir" && git "$@" ls-files --cached --others --exclude-standard | LC_ALL=C sort -u) > "$T/git.txt"
  "$T/dsh-manual-list" "$dir" 2> "$T/sum.txt" | LC_ALL=C sort -u > "$T/search.txt"
  echo "dir: $dir  git: $(wc -l < "$T/git.txt")  search: $(wc -l < "$T/search.txt")  $(cat "$T/sum.txt")"
  echo "only in git:";    LC_ALL=C comm -23 "$T/git.txt" "$T/search.txt"
  echo "only in search (by top two levels):"; LC_ALL=C comm -13 "$T/git.txt" "$T/search.txt" | cut -d/ -f1-2 | sort | uniq -c | sort -rn
}
```

- **Windows:** run it on the host checkout, `compare /e/work/github/ai/agent-plugin`.
  The checkout must have `core.autocrlf` unset or `false`.
- **WSL, ext4 clone:** run `compare ~/src/agent-plugin -c core.autocrlf=false`.
- **WSL, `/mnt/e`:** run `compare /mnt/e/work/github/ai/agent-plugin -c core.filemode=false`.
  The option ignores `core.filemode` noise.

**Record**, for each run:
- both counts;
- every path only in Git, and every path only in search, grouped as above;
- `uname -r`, the `PRETTY_NAME` of `/etc/os-release`, and `git --version`.

**Known difference:** at `b59ea4a`, search also listed the files inside the nested
`.worktree/*` Git worktrees (M1-G-FND-23.1), and Git listed those worktrees as two
gitlinks. Record what you see at `593ceaa`, not the old figures.

**Cleanup:** `rm -rf "$T/wt/src/harness/internal/zzmanual" "$T/dsh-manual-list"*`, then
section 0.

### Row 4.9: by hand, Windows and WSL. The watcher with a real editor gives one notice per save

**The throwaway program.** Save it as `$T/wt/src/harness/internal/zzwatch/main.go`, in
the worktree only:

```go
// Throwaway program for the DH-S-4 watcher manual check: watches one file with the
// default options in Auto mode for N seconds and prints the mode and every notice.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

func main() {
	secs, _ := strconv.Atoi(os.Args[2])
	s := watch.New(watch.Options{})
	defer s.Close()
	start := time.Now()
	notices := make(chan error, 64)
	w, err := s.Watch(context.Background(), watch.Target{Path: os.Args[1]}, func(err error) { notices <- err })
	if err != nil {
		fmt.Println("watch:", err)
		os.Exit(2)
	}
	fmt.Printf("watching %s mode=%v\n", os.Args[1], w.Mode())
	n := 0
	end := time.After(time.Duration(secs) * time.Second)
	for {
		select {
		case err := <-notices:
			n++
			fmt.Printf("notice %d at %.2fs err=%v mode=%v\n", n, time.Since(start).Seconds(), err, w.Mode())
		case <-end:
			w.Close()
			fmt.Printf("total notices %d mode=%v\n", n, w.Mode())
			return
		}
	}
}
```

**Build it:**

```bash
mkdir -p "$T/wt/src/harness/internal/zzwatch"   # then save main.go there
cd "$T/wt/src/harness"
go build -o "$T/dsh-manual-watch" ./internal/zzwatch                       # the lane's own binary
GOOS=windows go build -o "$T/dsh-manual-watch.exe" ./internal/zzwatch      # from WSL, for the Windows case
```

In each case, start the program, wait for its `watching ... mode=` line, then save the
file **three times, a few seconds apart**. Each save must be a real change: type one
character, then save.

| Case | Where | Editor and how to save | Expected mode |
| --- | --- | --- | --- |
| (a) Windows | a local NTFS path, e.g. `%TEMP%\dsh-watch\settings.json` (create it with one line) | **VS Code**: open the file, type a character, press Ctrl+S; repeat 3 times. This case is still open from 14.5.5; it needs an operator. | `notify` |
| (b) WSL, ext4 | `~/dsh-watch/settings.json` | vim, interactive (`vim file`, `A`, `x`, Esc, `:w`), or `vim -es -c 'normal Ax' -c wq file` 3 times, 2 s apart | `notify` (inotify) |
| (c) WSL, `/mnt/e` | the WSL program watches `/mnt/e/dsh-watch/settings.json` (outside the repository) | a **Windows** editor (VS Code or Notepad) saving `E:\dsh-watch\settings.json` 3 times | `poll` (9p is probed as remote by `fsx/remote_linux.go`) |

Run commands:
- (a): `"$T/dsh-manual-watch.exe" "$(cygpath -w "$TEMP")\\dsh-watch\\settings.json" 60` in
  Git Bash, or the same `.exe` from PowerShell.
- (b) and (c): `"$T/dsh-manual-watch" <file> 60` in WSL.

**Record**, for each case:
- the `watching ... mode=` line;
- each `notice n at ...` line (one per save);
- the `total notices N mode=...` line;
- the editor and its version, and the OS and file system.

Expect one notice per save, so `3` for three saves. More than one notice for a single
save fails the row.

**Cleanup:** delete the watched files and `dsh-watch` directories, then
`rm -rf "$T/wt/src/harness/internal/zzwatch" "$T"/dsh-manual-watch*`, then section 0.

---

## DH-S-6

### Row 6.8, by-hand part: one lane. `dsh --help` and one usage error

`run-register.sh` runs the composed part (`./internal/cli/ ./internal/cli/cmdline/`).
The by-hand part:

```bash
cd "$T/wt" && GOWORK=off go -C src/harness build -o "$T/dsh" ./cmd/dsh     # Windows: -o "$T/dsh.exe"
"$T/dsh" --help > "$T/help.out" 2> "$T/help.err"; echo "exit $?"
wc -l < "$T/help.out"; head -1 "$T/help.out"; wc -c < "$T/help.err"
"$T/dsh" --no-such-flag > "$T/bad.out" 2> "$T/bad.err"; echo "exit $?"
wc -c < "$T/bad.out"; cat "$T/bad.err"
```

**Record:**
- `--help`: the exit status (0 expected), the stdout line count, the first line
  (`Usage: dsh [options] [profile|command...]` expected), and empty stderr;
- `--no-such-flag`: the exit status (**64** expected), empty stdout, and stderr
  `dsh: cli/usage: unknown flag: --no-such-flag` followed by `Run 'dsh --help' for usage.`

At `2ee09dc`, `--help` printed 34 lines. Record the count you see at `593ceaa`.

### Rows 6.2 and 6.3: optional WSL variants (see the report's "ambiguous" list)

The script runs both rows once, in the shell it is started from, and records the
`owner` line and `stopped in` time. The 14.6.12 record also ran two variants.

1. **A delegated systemd scope**, which gives the `owner control-group` line:

   ```bash
   systemd-run --user --scope -p Delegate=yes -- bash "$S/run-register.sh" ~/src/agent-plugin --os wsl --only 6.2,6.3
   ```

   In that scope, `TestContextEndStopsDescendantsAfterChildExit` failed intermittently
   (M1-G-BOOT-36), so a failure there is recorded as seen.
2. **Without `systemd=true`**, which the register's 6.3 procedure asks for "while plan
   19.4 has not landed". This needs `/etc/wsl.conf` changed and `wsl --shutdown`, which
   stops the WSL lane. Run it only when no lane is running, and record whether you ran
   it.

---

## DH-S-9

### Row 9.3: the signed-in part

The script runs the composed tests
(`TestUsageFoldedFromSettlements|TestMeterMeasuresTheSession`). The second half of the
row is "the usage of the 9.1 settlement". Copy the `usage=in:..,out:..` field from the
`DH-S-9 claude signed-in:` line of the run in section S1.

---

## S. Signed-in rows: only the owner runs these

These rows need the owner's own signed-in Claude Code and GitHub Copilot CLI. Do not
type, set or export any key or token. Never run them in CI or from an agent session.
Every printed line is masked by the probe. A second person reviews the record before it
is committed (R-11). If a vendor cannot be reached, record `not run: <client>, <date>,
<host>, <error line>`; the row stays open. Claude must pass. Copilot is run and recorded
either way (M1-D-LOOP-6).

### S1. DH-S-9 rows 9.1 and 9.2: one run of `TestManualSignedIn`

Runbook: `docs/stories/harness-DSH-M1/evidence/DH-S-9-manual-runbook.md`.

```powershell
claude --version
copilot --version
$env:DSH_MANUAL_SIGNEDIN='1'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run TestManualSignedIn ./internal/llm/
```

Under WSL:
`DSH_MANUAL_SIGNEDIN=1 GOTOOLCHAIN=go1.27.4 go -C src/harness test -tags manual -count=1 -v -run TestManualSignedIn ./internal/llm/`

Optional switches: `DSH_MANUAL_COMMIT`, `DSH_MANUAL_CLAUDE_MODEL` (default `sonnet`),
`DSH_MANUAL_COPILOT_MODEL` (default `claude-sonnet-4.5`) and `DSH_MANUAL_HOME_ONLY=1`.
Record which ones you set.

| Row | Copy these lines |
| --- | --- |
| 9.1 | `DH-S-9 claude signed-in:` and `DH-S-9 copilot signed-in:`: one `assistant/message` with usage, the route id, the reported model id and the masked session id |
| 9.2 | `DH-S-9 claude signed-out:` and `DH-S-9 copilot signed-out:`: exactly one `assistant/attempt` with `llm/sign-in-required` (Copilot: code recorded as seen), class `permanent`, `llm-retry=0`, `retry-abandoned=not-retryable`, `other-route-prepares=0` and `other-route-attempts=0` |

**Cleanup:** the test removes its empty temporary homes. Check that `claude` and
`copilot` still work signed in.

### S2. DH-S-11 rows 11.1, 11.2, 11.3, 11.4 and 11.7: `TestManualM1OneTurn` and `TestManualM1CancelMidStream`, per route

Runbook: `docs/stories/harness-DSH-M1/evidence/DH-S-11-manual-runbook.md`. Windows
native is required; also run it under WSL if the client is installed there.

```powershell
claude --version
copilot --version
$env:DSH_M1_ROUTE='claude';  $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
$env:DSH_M1_ROUTE='copilot'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
```

Under WSL:
`DSH_M1_ROUTE=claude GOTOOLCHAIN=go1.27.4 go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/`

Optional switches: `DSH_M1_MODEL`, `DSH_M1_COMMIT` and `DSH_M1_ACCOUNT`. Do not record
the value of `DSH_M1_ACCOUNT`.

After each cancel run, list the client processes and record that the run left none:
`Get-Process claude,copilot -ErrorAction SilentlyContinue` on Windows, or
`pgrep -a 'claude|copilot'` under WSL.

| Row | From which run | Copy or record |
| --- | --- | --- |
| 11.1 | one turn, per route | from the `M1 bracketed log`: the one model selection logged per step; the route and model id (`M1 header:`) |
| 11.2 | one turn | the one `probe` call, its approval and its `tool/result` (`probed: ping`) |
| 11.3 | one turn **and** cancel | the brackets close in both runs: one `turn/end` completed and one aborted |
| 11.4 | cancel, per route | the `M1 cancelled log` and the three `M1 cancel:` lines: `turn/end` aborted with cause `user`, `step/end` cause `user`, one cancelled settlement |
| 11.7 | one turn | the `M1 request N:` lines (`sent` and `rebuilt` digests, `equal`, `invocation-equal`), `M1 turn/end:` and `M1 final reply:` |

**Cleanup:** the tests stop the client they started and remove their attempt
directories. Each Copilot attempt leaves one saved session under the Copilot home
(`session-state/<id>/`); dsh does not delete it (M1-D-PIPE-7 A).
