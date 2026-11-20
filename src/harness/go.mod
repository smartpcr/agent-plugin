module github.com/smartpcr/agent-plugin/src/harness

go 1.26.0

require (
	github.com/cucumber/godog v0.16.0
	// Schema inference and full JSON Schema validation for internal/schema
	// (DH-D-UTIL-1): chosen by docs/harness/libs/zod.md DH-ADR-SCHEMA-1; MIT, no
	// non-test dependencies (RM-10).
	github.com/google/jsonschema-go v0.4.3
	github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261119220503-a647d4984790
	github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790
)

require (
	// One semantic-version dialect for internal/util/version (DH-D-UTIL-4): chosen by
	// docs/harness/libs/semver.md DH-ADR-SEMVER-1 for npm-style ranges with
	// prereleases on request; MIT, no dependencies (RM-10).
	github.com/Masterminds/semver/v3 v3.5.0
	// One glob dialect for internal/util/glob (DH-D-UTIL-4): chosen by
	// docs/harness/libs/picomatch.md DH-ADR-PICOMATCH-1 for `**` and `{a,b}`; MIT,
	// no dependencies (RM-10).
	github.com/bmatcuk/doublestar/v4 v4.10.4
)

require (
	github.com/cucumber/gherkin/go/v42 v42.0.0 // indirect
	github.com/cucumber/messages/go/v34 v34.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
)
