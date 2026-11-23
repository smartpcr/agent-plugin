A seeded harness tree for the egress rules of `internal/archtest` (DH-T-UNDICI-8,
DH-T-HOST-19). The files are parsed, never built. `TestSeededTreeFailsTheEgressRules`
asserts the exact findings: every line marked `// seeded:` is reported, and nothing
else is.
