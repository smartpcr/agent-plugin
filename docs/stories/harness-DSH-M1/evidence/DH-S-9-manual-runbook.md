# DH-S-9 manual check: the operator's runbook

Status: **probe built (plan step 16.5.6), not run.** The owner runs it at plan step
16.5.9. Nothing in this file is evidence until the table below is filled from a real run.

This is the procedure of the "DH-S-9 manual-check runbook" in
[plan Stage 16.5](../gap-closure-plan.md#stage-165-signed-in-client-routes-and-the-dh-s-9-manual-check),
made concrete. The record it produces fills rows 9.1 and 9.2 of the manual checks
register in [DH-S-9.md](DH-S-9.md#manual-checks-register). It is also the run that
records `DH-E-CLAUDECODECLI-1` ([claude-code-cli.md](../../../harness/libs/claude-code-cli.md#dh-e-claudecodecli-1))
and `DH-E-COPILOTCLI-1` ([github-copilot-cli.md](../../../harness/libs/github-copilot-cli.md)
sec. 8).

## What the check proves, and what it does not

The check streams one model request through each **signed-in client route**: the harness
starts the user's own installed Claude Code (`claude`) or GitHub Copilot CLI (`copilot`)
as a child process, and that client uses the user's own sign-in. dsh never reads,
stores or forwards a token.

There are four cases. Each client runs once **signed in** and once **signed out**. For
the signed-out case, the client's configuration home is pointed at an empty temporary
directory, so it finds no sign-in. Each case prints one line, `DH-S-9 <client> <case>:
...`.

- **Signed in** passes when exactly one `assistant/message` is settled, with no
  `llm/retry` and nothing attempted on the second route the test registers. The request
  declares one test tool, `probe`, and asks the model to call it. A tool call that comes
  back unexecuted (`finish=tool_calls`) and a plain text answer (`finish=stop`) both
  pass.
- **Signed out** passes when exactly one `assistant/attempt` is settled with class
  `permanent`, the retry component logs `llm/retry-abandoned` with reason
  `not-retryable` and no `llm/retry`, and the second route has 0 prepares and 0
  attempts. For Claude the code must be `llm/sign-in-required`. For Copilot the code is
  recorded as seen: until this run pins the client's signed-out output, the route
  settles a signed-out run from its exit status (`llm/client-contract`). The check that
  it is `llm/sign-in-required` therefore stays open (github-copilot-cli.md sec. 5.4).

Claude must pass. Copilot is run and recorded either way, and a Copilot failure is
recorded as seen, never as a pass (M1-D-LOOP-6).

The guarantee holds at the **client-invocation boundary** (M1-D-PIPE-4 A). The record
proves what dsh started and what the client reported for this build, these client
versions and this workstation. It does not prove what the client sent to its vendor.

## Who, where, and with what

- **Who.** The owner or operator, signed in to both clients as themselves. Never CI and
  never an agent session.
- **Where.** The operator's workstation, from the repository root. The example below is
  Windows PowerShell; under WSL use the same variables in bash. The line records the
  host.
- **Credential.** The existing sign-ins (`claude auth login`, `copilot login`). Do not
  type, set or export any key or token. The routes remove `ANTHROPIC_API_KEY`,
  `GH_TOKEN`, `GITHUB_TOKEN`, `COPILOT_GITHUB_TOKEN` and the provider overrides from the
  client's environment anyway.
- **Floors.** Claude Code 2.1.259 or later; GitHub Copilot CLI 1.0.115 or later. An older
  client is refused before any request is sent (`prepare=failed:llm/client-upgrade-required`).

## Steps

1. **Record the client versions.** Run both commands and paste each first line into the
   table:

   ```powershell
   claude --version
   copilot --version
   ```

   The expected forms are `2.1.312 (Claude Code)` and `GitHub Copilot CLI 1.0.115-7.`;
   Copilot's second line, the update hint, is not recorded.

2. **Run the probe** from the repository root:

   ```powershell
   $env:DSH_MANUAL_SIGNEDIN='1'; $env:GOTOOLCHAIN='go1.27.4'; go -C src/harness test -tags manual -count=1 -v -run TestManualSignedIn ./internal/llm/
   ```

   Optional switches. Set them in the same PowerShell session before the command. None of
   them reaches a client, because every `DSH_*` variable is removed from the child's
   environment:

   | Variable | Effect |
   |---|---|
   | `DSH_MANUAL_COMMIT` | The commit printed in the line; unset, it is read from `.git` |
   | `DSH_MANUAL_CLAUDE_MODEL` | The Claude model (default `sonnet`) |
   | `DSH_MANUAL_COPILOT_MODEL` | The Copilot model (default `claude-sonnet-4.5`); set it if your account does not offer that model |
   | `DSH_MANUAL_HOME_ONLY` | `1` points only the client's own home (`CLAUDE_CONFIG_DIR`, `COPILOT_HOME`) at the empty directory in the signed-out cases. By default `HOME` and `USERPROFILE` are pointed there too. Use it when the default makes the probe fail (`prepare=failed:llm/client-upgrade-required`) because the installed build lives under your home, and record that you used it |

3. **Copy the four lines.** Each starts with `DH-S-9` at the start of a line in the
   output. Paste them into the table below unchanged.

4. **Review for secrets (R-11).** Every line has already passed `secret.Store.Mask` and
   the token-shape filter (`sk-ant-`, `gho_`, `ghu_`, `ghp_`, `ghs_`, `ghr_`,
   `github_pat_`, `Bearer <token>`). A second person still reads the record before it is
   committed. If any token is found: revoke that sign-in at the vendor, purge the record
   from history, and add a regression case to `TestSignedInRedaction`.

5. **Clean up.** The empty temporary homes are removed by the test. Nothing of your own
   sign-in or settings is touched; check that `claude` and `copilot` still work signed in
   afterwards. Each Copilot run leaves one saved session under your Copilot home
   (`session-state/<id>/`). The client cannot be told not to save it, and dsh does not
   delete what it does not own (M1-D-PIPE-7 A). The line's `session=` field holds its id's
   first eight characters if you want to find and delete it.

**If a vendor is unreachable**, record `not run: <client>, <date>, <host>, <error line>`
in the table, never a fixture, and retry from another network. The row stays open.

## What each line holds

A line looks like this (one line in the output, wrapped here):

```text
DH-S-9 claude signed-in: commit=<sha> host=windows client=claude-code version=2.1.312
program=claude.exe route=claude-code model=sonnet reported-model=<model> home=own
prepare=ok settlements=1 seq=<n> type=assistant/message finish=stop code=- class=-
llm-retry=0 retry-abandoned=- other-route-prepares=0 other-route-attempts=0
session=<8 chars>… client-retries=0 denials=0 tool-calls=0 usage=in:..,out:..
api-key-source=<value> init-tools=[] message-starts=1 num-turns=1 lines=<n>
```

| Field | Meaning (runbook "Recorded per case") |
|---|---|
| `commit`, `host` | The harness build and the workstation (`windows`, `linux`, `linux/wsl`) |
| `client`, `version`, `program` | The client, its probed version and the started program's file name |
| `route`, `model`, `reported-model` | The route id, the model asked for and the model the client reported (Claude's init line; `-` for Copilot, whose report is not pinned yet) |
| `home` | `own` (signed in) or `empty:<variables>` (signed out) |
| `prepare` | `ok`, or `failed:<code>` when the client was refused before any request |
| `settlements`, `seq`, `type`, `finish`, `code`, `class` | The settled attempt: how many, its log sequence number, `assistant/message` or `assistant/attempt`, its finish and failure |
| `llm-retry`, `retry-abandoned` | The number of `llm/retry` events (0 expected) and the reason of `llm/retry-abandoned` (`not-retryable` expected when signed out) |
| `other-route-prepares`, `other-route-attempts` | The second registered route; both 0 |
| `session` | The client session id the harness chose, masked to its first eight characters |
| `client-retries`, `denials`, `tool-calls`, `usage` | From the invocation record and the message |
| Claude only: `api-key-source`, `init-tools`, `message-starts`, `num-turns` | What the open inferences need (claude-code-cli.md sec. 5.4 and 7). `api-key-source` is the value a signed-in run reports. `init-tools` must be only `mcp__dsh__probe` when signed in. `message-starts` greater than 1 would mean `--max-turns 1` let a second model request through (the route then fails the attempt as `llm/client-contract`) |
| Copilot only: `first-event`, `last-event` | The first and last JSONL line types the client printed. They pin the undocumented schema (github-copilot-cli.md sec. 5.2) and show what a signed-out run prints |
| `lines` | How many output lines the client printed |

When this run is recorded, the synthetic fixtures under
`src/harness/internal/llm/provider/claudecode/testdata` and `.../copilot/testdata` are
replaced by scrubbed recordings (plan 16.5.9). That is a separate step from this table.

## Record

| Field | Value |
|---|---|
| Date | |
| Operator | |
| Second reviewer (R-11) | |
| Host | |
| Commit | |
| `claude --version` | |
| `copilot --version` | |
| Switches set (`DSH_MANUAL_*`) | |

| Case | Printed line (paste unchanged) | Pass? |
|---|---|---|
| `DH-S-9 claude signed-in` | | |
| `DH-S-9 claude signed-out` | | |
| `DH-S-9 copilot signed-in` | | recorded either way |
| `DH-S-9 copilot signed-out` | | recorded either way; `llm/sign-in-required` check open |

## Without the variable

Run without `DSH_MANUAL_SIGNEDIN=1`, every case skips and no client is started:

```text
=== RUN   TestManualSignedIn
=== RUN   TestManualSignedIn/claude/signed-in
    manual_signedin_test.go:192: manual: set DSH_MANUAL_SIGNEDIN=1, with claude-code installed and signed in as yourself (DH-S-9 runbook)
=== RUN   TestManualSignedIn/claude/signed-out
    ...
=== RUN   TestManualSignedIn/copilot/signed-in
    ...
=== RUN   TestManualSignedIn/copilot/signed-out
    ...
--- PASS: TestManualSignedIn
    --- SKIP: TestManualSignedIn/claude/signed-in
    --- SKIP: TestManualSignedIn/claude/signed-out
    --- SKIP: TestManualSignedIn/copilot/signed-in
    --- SKIP: TestManualSignedIn/copilot/signed-out
```
