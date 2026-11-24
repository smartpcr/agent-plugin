# DH-S-16 evidence (fixture)

Closure status: closed

## Steps

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 16.1 | DH-D-BUNDLE-3 | 0a1b2c3 | |
| 16.8 | guarded scenarios | 0a1b2c3d4e | |

## Scenarios

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-STORE-1 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | run-1 |
| DH-E-STORE-1 | macOS | guarded | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-STORE-6 | Linux (WSL) | guarded | required | scratch home | 0a1b2c3 | passed | |

## Manual checks register

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 16.4 | Ctrl-C during a tool call | the turn stopped and the session reopened | operator | 0a1b2c3 | `manual` |

## Owner defaults (M2-C-4)

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| `dsh headless` progress | U-K2-4 | `dsh headless` progress | yes | owner | 0a1b2c3 |
