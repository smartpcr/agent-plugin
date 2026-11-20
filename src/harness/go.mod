module github.com/smartpcr/agent-plugin/src/harness

go 1.26.0

require (
	// One semantic-version dialect for internal/util/version (DH-D-UTIL-4): chosen by
	// docs/harness/libs/semver.md DH-ADR-SEMVER-1 for npm-style ranges with
	// prereleases on request; MIT, no dependencies (RM-10).
	github.com/Masterminds/semver/v3 v3.5.0
	// Line diffs for internal/util/textdiff (DH-D-FS-2): chosen by
	// docs/harness/libs/diff.md; BSD-3-Clause and MIT, no requirements.
	github.com/aymanbagabas/go-udiff v0.4.2
	// One glob dialect, shared by internal/util/glob (DH-D-UTIL-4: chosen by
	// docs/harness/libs/picomatch.md DH-ADR-PICOMATCH-1 for `**` and `{a,b}`) and
	// internal/util/search (DH-D-FS-3: chosen by docs/harness/libs/vscode-ripgrep.md
	// and picomatch.md); MIT, no dependencies (RM-10), no cgo.
	github.com/bmatcuk/doublestar/v4 v4.10.4
	github.com/cucumber/godog v0.16.0
	// Change notifications for internal/util/watch (DH-D-UTIL-3): chosen by
	// docs/harness/libs/chokidar.md DH-ADR-CHOKIDAR-1; BSD-3-Clause, requires only
	// golang.org/x/sys.
	github.com/fsnotify/fsnotify v1.10.1
	// Schema inference and full JSON Schema validation for internal/schema
	// (DH-D-UTIL-1): chosen by docs/harness/libs/zod.md DH-ADR-SCHEMA-1; MIT, no
	// non-test dependencies (RM-10).
	github.com/google/jsonschema-go v0.4.3
	github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261120142542-a7caf0f79ab0
	github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261119220503-a647d4984790
	// File identity, locks, POSIX rename and filesystem type for internal/util/fsx
	// (DH-D-FS-1): docs/harness/libs/node-fs-path-os-zlib.md; BSD-3-Clause.
	golang.org/x/sys v0.50.0
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
