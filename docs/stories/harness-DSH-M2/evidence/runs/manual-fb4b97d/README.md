# DH-S-14 manual checks at fb4b97d

The two manual checks of [impl.md DH-S-14](../../../../../harness/impl.md#dh-s-14), run once on
2026-11-26 at `fb4b97d` (main, all five DH-S-14 steps merged) in a throwaway worktree that was
removed afterwards. Scratch data only: every home, workspace, preset file and SQLite database
was a `t.TempDir()`. Register: [../../DH-S-14.md](../../DH-S-14.md), section Manual checks register.

| File | What it is |
| --- | --- |
| `dhs14/zz_manual_s14_profile_test.go.txt` | step 14.2: copied into `src/harness/internal/boot/profile/`, run with `-v`; never committed as Go source |
| `dhs14/zz_manual_s14_agentpreset_test.go.txt` | step 14.3: copied into `src/harness/internal/boot/agentpreset/`, run with `-v`; never committed as Go source |
| `check-14.2-windows.log` | Windows 11 (10.0.28000), go1.27.4 windows/amd64: the composed tree and ownership index of each profile and the three chosen values |
| `check-14.3-windows.log` | Windows, same toolchain: the preset edited while session `s1` is open |
| `checks-14.2-14.3-wsl.log` | WSL2 Ubuntu (Linux 6.18.58.1-microsoft-standard-WSL2), go1.27.4 linux/amd64: both checks |

Command, from the worktree root (WSL: `GOTOOLCHAIN=local`, `GOFLAGS=-buildvcs=false`, since the
worktree's `.git` file names a Windows path):

```
go -C src/harness test -count=1 -v -run TestManualS14 ./internal/boot/profile/ ./internal/boot/agentpreset/
```

Both runs passed on both platforms; the revision digests are the same on both
(`sha256:1b86830f...` for the shipped `standard.yml`, `sha256:ab4e5427...` after the edit).
