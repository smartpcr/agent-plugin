# Stage 7.4 e2e — open items

## Product defect: DH-E-VIEW-1 cannot pass on a client route (blocks the e2e gate)

`title.GeneratorConfig.withDefaults` (src/harness/internal/session/title/title.go)
always sets `MaxOutputTokens` (0 -> 4096), and every title request sends it
(service.go:473, events.go:130). The claude-code and copilot-cli routes refuse any
output cap with `llm/unsupported-option` (claudecode.go:164; DH-E-LLM-3 requires
this). So every title attempt on a client route fails before the client is run:

- "VIEW-1 generated title": attempt failed `llm/unsupported-option`, 0 client runs.
- "VIEW-1 failing title route": fails for the same reason, not `llm/sign-in-required`.

The live `TestModelTitlesSession` ("default title configuration") will fail the
same way on the S17 machine. Needs an impl decision, e.g. no output cap by default
for routes that cannot honour one, or a title route capability check. The e2e
assertions are left strict on purpose.

## Other gaps (open questions)

- The dsh catalogue registers no claude-code / copilot-cli route, so the S17 live
  scenarios cannot register the routes through a layer.
- `title.Attempt` records no client invocation, so the live VIEW-1
  `requireInvocation` check cannot hold.
