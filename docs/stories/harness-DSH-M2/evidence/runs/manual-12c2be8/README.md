# DH-S-16 manual checks at 12c2be8

Manual checks of [impl.md DH-S-16](../../../../../harness/impl.md#dh-s-16), run on 2026-11-27 by the
Forge engineer agent against a release build (no `testhook` tag) of `./cmd/dsh` at `12c2be8` (main, all ten
DH-S-16 steps merged). Only scratch data was used: one empty home under `%TEMP%`, removed afterwards.
Register: [../../DH-S-16.md](../../DH-S-16.md), section Manual checks register. Operator acceptance is
pending (question `dh-s-16-manual-checks`).

| File | What it is |
| --- | --- |
| `check-16.5-windows.log` | step 16.5: a second `dsh web` beside a running host reuses it; `dsh status`; after a kill, the crash report and `--safe` at the next start |
| `check-16.3-secret-set-windows.log` | step 16.3, the separate half of M2-I-1: `dsh secret set` with a scratch value |

Not run here: the 16.3 headless prompt on the signed-in Claude Code route and the 16.4 Ctrl-C during a
tool call. The release build's catalog has no model route: no catalog item wraps
`llm/provider/claudecode`, and the only route a built binary can insert is `llm.testkit-route` behind the
`testhook` tag. Without a route, no turn can reach a model or a tool call, so neither check can be seen.
The WSL leg of 16.5 has not been run yet.
