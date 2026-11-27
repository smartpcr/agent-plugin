# DH-S-16 manual checks at 12c2be8

Manual checks of [impl.md DH-S-16](../../../../../harness/impl.md#dh-s-16), run on 2026-11-27 by the
Forge engineer agent against a release build (no `testhook` tag) of `./cmd/dsh` at `12c2be8` (main, all ten
DH-S-16 steps merged). Only scratch data was used: one empty home under `%TEMP%`, removed afterwards.
Register: [../../DH-S-16.md](../../DH-S-16.md), section Manual checks register. The operator accepted
the 16.5 runs (Windows and WSL) and the scratch `dsh secret set` run (answer to question
`dh-s-16-manual-checks`: "Accept both").

| File | What it is |
| --- | --- |
| `check-16.5-windows.log` | step 16.5: a second `dsh web` beside a running host reuses it; `dsh status`; after a kill, the crash report and `--safe` at the next start |
| `check-16.5-wsl.log` | step 16.5 on WSL2 Ubuntu 24.04: a release linux/amd64 build at `0420164` (product source identical to `12c2be8`). One listener; the second `dsh web` returned `launch/no-channel` (no browser, host without a terminal); then the crash notice after `kill -9` |
| `check-16.3-secret-set-windows.log` | step 16.3, the separate half of M2-I-1: `dsh secret set` with a scratch value |

Not run here: the 16.3 headless prompt on the signed-in Claude Code route and the 16.4 Ctrl-C during a
tool call. The release build's catalog has no model route: no catalog item wraps
`llm/provider/claudecode`, and the only route a built binary can insert is `llm.testkit-route` behind the
`testhook` tag. Without a route, no turn can reach a model or a tool call, so neither check can be seen.
The operator deferred both (answer to `dh-s-16-claude-route`: "Pause until a stage adds the Claude Code
route to the release build"). They will be run on that build by a later workstream.
