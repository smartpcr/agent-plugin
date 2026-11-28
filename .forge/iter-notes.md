# Stage 7.4 e2e — iteration 2 reflection

## State
- Scenario 2 (TOOLS-1 + LOADER-1) passes with no S17 inputs set. Each LOADER-1 "next turn"
  is now a real driver turn on the claude-code route. The probe component contributes a
  prompt section ("Log level: X."). The turn asserts that level in the logged system message
  and in the --system-prompt-file the stand-in client was started with.
- A new instance-selection Given replaces the no-op `thenLive`:
  - all S17 inputs set: provided signed-in clients; the live scenario tests run and are asserted
  - none set: ephemeral stand-ins, readiness-probed once per route
  - a partial set: fails and names the missing inputs
- Scenario 1 is still red, only on the two VIEW-1 legs, with llm/unsupported-option and 0 client runs.

## ESCALATION: operator pin needed (DH-E-VIEW-1 on client routes)
This is a spec conflict, not a test bug:
- session-views.md §6.2 (line 73, table line 302): the title request carries an output cap,
  and the default is 4096 (title.go:121, service.go:473, events.go:130).
- llm.md and DH-E-LLM-3: claude-code and copilot-cli refuse any output cap with
  llm/unsupported-option (claudecode.go:164).
- VIEW-1 needs a generated title on a client route under the default title config.
  GeneratorConfig maps 0 or below to 4096, so no configuration avoids the cap.

Fix options for the impl owner:
- (a) send no title cap on routes that take none (needs a route capability flag in llm.Route);
- (b) a 0/"none" sentinel in GeneratorConfig that the defaults keep;
- (c) amend VIEW-1 to exclude client routes.

The e2e assertions stay strict on purpose. They must not be loosened.

## Other open gaps
- The dsh catalogue registers no claude-code or copilot-cli route.
- title.Attempt records no client invocation, so the live TestModelTitlesSession requireInvocation check cannot hold.
- Step file is about 1,600 lines (one-file layout mandated by the brief).
