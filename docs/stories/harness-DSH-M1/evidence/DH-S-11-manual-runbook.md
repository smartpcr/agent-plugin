# DH-S-11 manual check (M1): the operator's runbook

Status: **probe built (plan steps 17.4.1 and 17.4.2), not run.** The operator runs it at
plan step 17.4.3. Nothing in this file is evidence until the record below is filled from a
real run.

This is the M1 check of impl.md DH-S-11, run as
[plan Stage 17.4](../gap-closure-plan.md#stage-174-m1-on-the-signed-in-route-and-dh-s-11-closure)
describes. The record it produces fills rows 11.1 to 11.4 and 11.7 of the manual checks
register in [DH-S-11.md](DH-S-11.md#manual-checks-register). The probe is
`src/harness/internal/agent/m1_manual_test.go` (build tag `manual`).

## What the check proves, and what it does not

M1 is the first time the whole agent loop runs against a real model: the session, the
inbox, the model selection, the guarded tool pipeline with one approval, and the driver.
These are the same components the keyless F2 golden test runs against a scripted model;
only the model differs.

Under OWNER-2 there is no API key. The real model is reached through a **signed-in client
route**: dsh starts your own installed Claude Code (`claude`) or GitHub Copilot CLI
(`copilot`) as a child process, and that client uses your own sign-in. dsh never reads,
stores or forwards a token, and removes every credential variable from the client's
environment.

There are two runs per route:

- **One turn** (`TestManualM1OneTurn`). One prompt asks the model to call the test tool
  `probe` once with the text `ping` and then reply with what it returned. The client
  returns the tool call unexecuted; the harness runs it through its own guarded pipeline,
  with one approval that the test's scripted answerer grants. The run passes when the
  brackets close, the answerer was asked once, and the shared oracle `f2Completed` holds:
  one completed turn, no failed attempt, one successful `tool/result` carrying
  `probed: ping`, and a final text reply. For every request sent, the request rebuilt
  from the log must have the same digest, and the **client invocation** rebuilt from the
  log (the argument list and the standard input dsh wrote, by digest) must equal the one
  the settlement recorded.
- **Cancel** (`TestManualM1CancelMidStream`). A prompt with a long answer is cancelled by
  the user once answer text is streaming. The run passes when the brackets close,
  `turn/end` is `aborted` with cause `user`, the one `step/end` has cause `user`, and the
  one settlement is either an interrupted `assistant/message` keeping the streamed text
  with no tool call, or an `assistant/attempt` of class `cancelled`; either must record
  the invocation of the client that was stopped.

**Claude must pass. Copilot is run and recorded either way**, and a Copilot failure is
recorded as seen, never as a pass (M1-D-LOOP-6 A).

The guarantee holds at the **client-invocation boundary** (M1-D-PIPE-4 A, M1-D-CLOSE-3 A):
the record proves what dsh started and handed the client, and what the client reported,
for this build, these client versions and this workstation. It does not prove what the
client sent to its vendor; that is the client's own and is declared opaque.

## Who, where, and with what

- **Who.** The owner or operator, signed in to the client as themselves. Never CI and
  never an agent session.
- **Where.** The operator's workstation, from the repository root. Windows native is
  required; run it under WSL too if the client is installed there, and the header line
  records which (`host=windows`, `host=linux/wsl`).
- **Credential.** Your existing sign-in (`claude auth login`, `copilot login`). Do not
  type, set or export any key or token. The routes remove `ANTHROPIC_API_KEY`,
  `ANTHROPIC_AUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, `COPILOT_GITHUB_TOKEN` and the
  provider overrides from the client's environment anyway.
- **Floors.** Claude Code 2.1.259 or later; GitHub Copilot CLI 1.0.115 or later.
- **Go.** `GOTOOLCHAIN=go1.27.4` (the gate floor).

## Steps

1. **Record the client versions.** Paste each first line into the record:

   ```powershell
   claude --version
   copilot --version
   ```

2. **Run the Claude route** from the repository root (Windows PowerShell):

   ```powershell
   $env:DSH_M1_ROUTE='claude'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
   ```

3. **Run the Copilot route** in the same way:

   ```powershell
   $env:DSH_M1_ROUTE='copilot'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
   ```

   Under WSL, from the repository root in bash:

   ```bash
   DSH_M1_ROUTE=claude GOTOOLCHAIN=go1.27.4 go -C src/harness test -tags manual -count=1 -v -run 'TestManualM1OneTurn|TestManualM1CancelMidStream' ./internal/agent/
   ```

   Optional switches. Set them in the same shell before the command. None of them reaches
   a client, because every `DSH_*` variable is removed from the child's environment:

   | Variable | Effect |
   |---|---|
   | `DSH_M1_ROUTE` | Required: `claude` (route `claude-code`) or `copilot` (route `copilot-cli`). Unset, both tests skip |
   | `DSH_M1_MODEL` | The model (default `sonnet` for Claude, `claude-sonnet-4.5` for Copilot); set it if your account does not offer the default |
   | `DSH_M1_COMMIT` | The commit printed in the header; unset, it is read from `.git` |
   | `DSH_M1_ACCOUNT` | Your signed-in account names and emails, comma separated, so the filter masks them by value as well as by shape (for example `$env:DSH_M1_ACCOUNT='octocat'`). The machine's user name is always masked |

4. **Copy the lines.** In the `-v` output, under each test, copy every line that begins
   with `M1 ` (go test prefixes each with the file name and line number; keep the prefix
   or drop it, but keep the rest unchanged). The tests print exactly these:

   | Line | Printed by | What it holds |
   |---|---|---|
   | `M1 header:` | both tests | `run`, `commit`, `route` (route id), `client` and `version` (the probed client), `program` (its file name), `model` (model id), `go` (Go version), `os` and `host` |
   | `M1 bracketed log` | one turn | every event of the turn, one per line, ids and digests numbered as in the F2 golden |
   | `M1 request N:` | one turn | per request sent: `asOf`, the `sent` and `rebuilt` request digests and `equal`; the settlement's sequence number and client; `args-logged` and `args-rebuilt`, `stdin-logged` and `stdin-rebuilt` (the client-invocation digests) and `invocation-equal`; the client session id's first eight characters; the rebuilt messages |
   | `M1 turn/end:` | one turn | the turn's end reason |
   | `M1 final reply:` | one turn | the model's final text |
   | `M1 cancelled log` | cancel | every event of the cancelled turn |
   | `M1 cancel:` | cancel | three lines: the `turn/end` reason; whether the prefix was kept (`interrupted prefix kept (<n> bytes)`) or the attempt settled cancelled (with its code); the stopped client, its version and session id prefix |

   Every line has already passed `m1Redact` (plan 17.4.2): it masks the account name and
   email, `Authorization` and cookie values, the `sk-ant-`, `gho_`, `ghu_`, `ghp_`,
   `ghs_`, `ghr_` and `github_pat_` token shapes, bearer strings and any token file path,
   each replaced by `[redacted]`. If a pattern survived the masks, the test fails with
   `m1Redact: line <n> still matches <pattern>` and the line is not printed: record that
   failure as seen, and do not try to work around it.

5. **Review for secrets (R-11).** A second person reads the record before it is
   committed. If any token is found: revoke that sign-in at the vendor, purge the record
   from history, and add a regression case to `TestM1RedactionCatchesIdentity`
   (`src/harness/internal/agent/m1_redact_test.go`).

6. **Clean up and check.** The test removes its attempt directories and stops the client
   it started, also on the cancel. Right after each cancel run, list the client processes
   (`Get-Process claude,copilot -ErrorAction SilentlyContinue` on Windows, `pgrep -a
   'claude|copilot'` under WSL) and record that none started by the run is left; a client
   you run yourself in another window is not the run's. Check that `claude` and `copilot`
   still work signed in afterwards. Each Copilot attempt leaves one saved session under
   your Copilot home (`session-state/<id>/`); the client cannot be told not to save it,
   and dsh does not delete what it does not own (M1-D-PIPE-7 A). The `session=` field
   names its first eight characters if you want to find and delete it.

**If a vendor is unreachable**, record `not run: <client>, <date>, <host>, <error line>` in
the record, never a fixture, and retry from another network. The row stays open.

## Record

| Field | Value |
|---|---|
| Date | |
| Operator | |
| Second reviewer (R-11) | |
| Host (Windows; WSL if run) | |
| Commit | |
| `claude --version` | |
| `copilot --version` | |
| Switches set (`DSH_M1_*`, values of `DSH_M1_ACCOUNT` not recorded) | |
| Client processes after each cancel run | |

| Route | Run | Expected | Printed lines (paste unchanged) | Result |
|---|---|---|---|---|
| `claude-code` | one turn | brackets closed, one approval, every rebuilt request digest and client invocation equal to the sent and logged ones, one completed turn, probe succeeded, final text reply | | must pass |
| `claude-code` | cancel | brackets closed, `turn/end` aborted with cause `user`, one cancelled settlement recording the stopped client's invocation | | must pass |
| `copilot-cli` | one turn | as for Claude | | recorded either way |
| `copilot-cli` | cancel | as for Claude | | recorded either way |

## Without the route selector, or without a client

With `DSH_M1_ROUTE` unset both tests skip and nothing is started:

```text
=== RUN   TestManualM1OneTurn
    m1_manual_test.go:<n>: manual: set DSH_M1_ROUTE=claude or DSH_M1_ROUTE=copilot, with that client installed and signed in as yourself (DH-S-11 runbook)
--- SKIP: TestManualM1OneTurn
=== RUN   TestManualM1CancelMidStream
    ...
--- SKIP: TestManualM1CancelMidStream
```

With the selector set, the skip check looks the client up on the child's `PATH` and runs
its version probe only (`claude --version`, `copilot --version --no-auto-update`); it never
sends a prompt. A missing client, or one below its floor, skips with
`manual: no <client> client found on the PATH` or `manual: the <client> client cannot be
used`. A client that is installed but signed out is **not** skipped: the one-turn run then
fails with `llm/sign-in-required` in its log, and that failure is what you record.
