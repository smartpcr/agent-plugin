# Stage 7.4 e2e — iteration 4 reflection

## Done
- whenSaved now publishes the user layer with fsx.WriteFile, which stages the file beside the
  target and retries a publication that something else blocked on Windows. It replaces the
  single os.Rename, which hit "Access is denied" while the config watcher held the file.
- Verified with -count=3: TOOLS-1/LOADER-1 passed on all 3 runs, with no "Access is denied".

## Gate state
- Scenario 2 is green.
- Scenario 1 is red only on the two VIEW-1 legs: llm/unsupported-option, 0 client runs.

## Blocked outside QA scope (operator pin needed)
- VIEW-1 title output cap (default 4096) vs. client routes that refuse any cap. Options:
  - (a) a route capability flag;
  - (b) a "no cap" sentinel in title.GeneratorConfig;
  - (c) amend VIEW-1.
- Signed-in service acceptance needs the operator's S17 clients (DSH_SCENARIO_* inputs).
- Still open: no dsh catalogue entries for the client routes, and title.Attempt does not
  record that the client was run.
- Possible future flake: whenSavedBad still writes in place with os.WriteFile. It has not failed.
