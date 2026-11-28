module github.com/smartpcr/agent-plugin/src/harness

go 1.27.4

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
	// The Gherkin runner of the harness's e2e suites in test/e2e/harness-DSH (the
	// Gherkin feature files of each story stage); MIT. Test code only: no package of dsh
	// imports it, so it is never linked into the binary. The same version as
	// src/go/test/e2e and tools/harness-gate, so the repository has one Gherkin dialect.
	github.com/cucumber/godog v0.16.0
	// The expression engine behind the loader's Evaluator seam, internal/boot/expr
	// (DH-D-LOADER-5): chosen by docs/harness/libs/cordis-loader.md (DH-LIB-LOADEREXPR)
	// for typed checking and no side effects; MIT, no dependencies (RM-10).
	github.com/expr-lang/expr v1.17.8
	// Change notifications for internal/util/watch (DH-D-UTIL-3): chosen by
	// docs/harness/libs/chokidar.md DH-ADR-CHOKIDAR-1; BSD-3-Clause, requires only
	// golang.org/x/sys.
	github.com/fsnotify/fsnotify v1.10.1
	// The filesystem the Stage 8.7 e2e suite's NFS export serves (go-nfs below);
	// Apache-2.0. Test code only: never linked into the binary.
	github.com/go-git/go-billy/v5 v5.9.2
	// Schema inference and full JSON Schema validation for internal/schema
	// (DH-D-UTIL-1): chosen by docs/harness/libs/zod.md DH-ADR-SCHEMA-1; MIT, no
	// non-test dependencies (RM-10).
	github.com/google/jsonschema-go v0.4.3
	// Opt-in response gzip of host.listen, internal/host/listen (DH-D-HOST-1): chosen by
	// docs/harness/libs/compression.md DH-ADR-COMPRESSION-1 (package gzhttp, zstd
	// disabled); BSD-3-Clause and Apache-2.0, no requirements (RM-10), no cgo.
	github.com/klauspost/compress v1.20.4
	// The Landlock ruleset of the launch stage, internal/world/sandbox/launch (DH-D-SANDBOX-4):
	// chosen by docs/harness/libs/node-addon-system.md (DH-LIB-GOLANDLOCK, DH-ADR-NATIVESYSTEM-1)
	// for its structure layouts and version table; only NewRulesetFD, PathAccess,
	// LandlockRestrictSelf and LandlockGetABIVersion are called, never Restrict*; MIT,
	// requires golang.org/x/sys and psx (RM-10).
	github.com/landlock-lsm/go-landlock v0.11.2
	// The cordis port (packages cordis, loader, timer) the harness is built on
	// (design.md sec. 4.2, DH-ADR-26, RM-4): pinned to a committed revision, never
	// replaced, so a GOWORK=off release build resolves it from this go.mod and go.sum.
	github.com/smartpcr/agent-plugin/src/go v0.1.5-0.20261120142542-a7caf0f79ab0
	// The SDK module (jsonrpc codec, protocol message types and schema) the harness
	// serves (design.md sec. 4.2, DH-ADR-26, RM-4): pinned the same way.
	github.com/smartpcr/agent-plugin/src/sdk v0.0.0-20261127083844-3527b3c66344
	// The fixed command tree, help routing and completion scripts of internal/cli
	// (DH-D-CLI-1): chosen by docs/harness/libs/commander.md section 5
	// (DH-LIB-COMMANDER); Apache-2.0, links pflag and, on Windows only, mousetrap.
	github.com/spf13/cobra v1.10.2
	// Option parsing of internal/cli/cmdline in both stages (DH-ADR-COMMANDER-1):
	// chosen by docs/harness/libs/commander.md section 5; BSD-3-Clause, no requirements.
	github.com/spf13/pflag v1.0.10
	// The NFSv3 server of the Stage 8.7 e2e suite's real NFS mount on Linux, served
	// as the run's own user (test/e2e/harness-DSH-M2, DH-E-CHOKIDAR-2); Apache-2.0,
	// requires go-billy, go-nfs-client (BSD-2-Clause), go-xdr (ISC), golang-lru/v2
	// (MPL-2.0) and filepath-securejoin (BSD-3-Clause, MPL-2.0), no cgo. Test code
	// only: no package of dsh imports it, so it is never linked into the binary.
	github.com/willscott/go-nfs v0.0.4
	// The one YAML dialect, imported only by internal/boot/yamlx (DH-D-BOOT-4,
	// DH-D-LOADER-2): chosen by docs/harness/libs/yaml.md DH-ADR-YAML-1 for comments
	// on nodes and positions; Apache-2.0 and MIT, no dependencies (RM-10).
	go.yaml.in/yaml/v4 v4.0.0
	// File identity, locks, POSIX rename and filesystem type for internal/util/fsx
	// (DH-D-FS-1): docs/harness/libs/node-fs-path-os-zlib.md; BSD-3-Clause.
	golang.org/x/sys v0.50.0
	// The session store's engine, imported only by internal/session/store/sqlite
	// (DH-D-STORE-2): chosen by docs/harness/libs/sqlite.md DH-ADR-SQLITE-1 as pure
	// Go, so the harness stays a single static binary with no cgo and no system
	// SQLite (NG-4); BSD-3-Clause, requires only its modernc.org runtime (RM-10).
	// Measured by TestBudgetStoreAppend before anything was built on it (fact F-2).
	modernc.org/sqlite v1.62.0
)

require (
	github.com/cucumber/gherkin/go/v42 v42.0.0 // indirect
	github.com/cucumber/messages/go/v34 v34.2.0 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-memdb v1.3.5 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/rasky/go-xdr v0.0.0-20170124162913-1a41d1a06c93 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/willscott/go-nfs-client v0.0.0-20240104095149-b44639837b00 // indirect
	// Required by go-landlock's all-threads path, which the harness never calls; without cgo it
	// uses syscall.AllThreadsSyscall and compiles no C (node-addon-system.md); BSD-3-Clause OR
	// GPL-2.0-only.
	kernel.org/pub/linux/libs/security/libcap/psx v1.2.78 // indirect
	// The pinned libc translation the SQLite driver is generated against
	// (DH-ADR-SQLITE-1): modernc.org/sqlite builds only with the exact libc it was
	// transpiled with, so the pair is pinned together and moves only together after a
	// new F-2 measurement (docs/stories/harness-DSH-M1/evidence/DH-S-7.md); BSD-3-Clause.
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
