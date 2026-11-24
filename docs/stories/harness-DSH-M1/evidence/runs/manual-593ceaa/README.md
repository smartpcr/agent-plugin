# Manual checks at the M1 closure commit `593ceaa` (plan 18.1.5, 18.1.6)

These are the logs and tools of the manual checks run on 2026-11-23 at the frozen commit
`593ceaa3efd55d41bf0eeaced5ba7e26acc71966`.

**Who ran them.** The lead session ran them at the owner's instruction (decision
`manual-checks-run-by-lead` in [decisions.md](../../../decisions.md)). The owner did not
watch the runs.

**Start here.** The summary of every result is [byhand-results.md](byhand-results.md).

| File | What it is |
| --- | --- |
| `register-windows-593ceaa-20261123T2213Z.log` | `run-register.sh` on Windows: 87 rows. Under the amended procedure (`register-seeded-amendments`) all pass; the raw run marks rows 1.2 and 1.5 FAIL on case `coverage-child`, which is judged instead by `TestCoverageMergesChildProcesses` in `byhand-windows-593ceaa.log` |
| `register-wsl-593ceaa-20261123T2237Z.log` | `run-register.sh --os wsl`: 11 rows, all pass |
| `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log` | the by-hand rows 1.1, 3.1, 3.4, 3.5, 3.8, 4.8 and 6.8 on Windows, and the `coverage-child` judgement |
| `byhand-wsl-593ceaa.log`, `byhand-wsl.sh` | rows 4.8 (ext4 and `/mnt/e`) and 4.9 (b) under WSL |
| `byhand-4.9a-windows*.log` | four watcher windows for row 4.9 (a). All closed with 0 notices because no save was made; they are not a result |
| `signedin-dhs9-593ceaa.log` | `TestManualSignedIn`, the DH-S-9 check |
| `signedin-m1-claude-593ceaa.log`, `signedin-m1-copilot-593ceaa.log` | `TestManualM1OneTurn` and `TestManualM1CancelMidStream`, per route |
| `trace-tests-DH-S-11-593ceaa.txt` | `harness-gate trace-tests --stage DH-S-11` at `593ceaa` |
| `run-register.sh`, `rows.tsv`, `by-hand-checklist.md` | the runner, its row table and the by-hand steps |
| `dhs3/zz_manual_layers_test.go.txt` | the DH-S-3 throwaway test. It is renamed `.txt` so the `gofmt` gate does not scan it; copy it back as `zz_manual_layers_test.go` into a throwaway worktree's `src/go/loader/` to run it |
| `done_at_m1.py`, `closure_tables.py`, `done_at_m1.tsv` | how the "Done at M1" and "Built, not done" tables of DH-S-11 are derived |

The signed-in logs were searched with the token-shape patterns before they were committed,
and none matched.
