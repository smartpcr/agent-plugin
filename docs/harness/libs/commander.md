# commander: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". No harness code exists.
>
> **Unverified research notes.** Where this document says "probe", it reports an
> observation made during library research on 2026-11-18 by running a throwaway program
> against the candidate modules outside this repository. That was against the project's
> rule that nothing is built or run in the design task, and the program was not kept, so
> the observation cannot be repeated from this repository. Every such statement is
> therefore **unverified and to be measured** by `DH-T-COMMANDER-1` (section 8), which
> turns the probe table into a test; if the test contradicts an observation, the choice
> is reopened.

| | |
|---|---|
| **Library ID** | `DH-LIB-COMMANDER` (command tree), `DH-LIB-PFLAG` (flag parsing) |
| **Upstream unit** | `UP-DEP-commander` |
| **Upstream version** | 15.0.0 (`pnpm-lock.yaml`) |
| **Kind** | runtime |
| **Used by** | apps, bundle, experimental; boot (types only) |
| **Go choice** | named Go modules `github.com/spf13/cobra` and `github.com/spf13/pflag`, behind a small harness package |
| **Related areas** | A24, which owns the packages `internal/cli/cmdline` and `internal/cli/tree` and confirms the grammar ([../areas/app-cli.md](../areas/app-cli.md), `DH-ADR-CLI-1`, `DH-D-CLI-1`); A21, boot (launch data, app command lines); each carrier (web, headless, SDK, ACP) |

## 1. What it is and why upstream needs it

`commander` is a JavaScript library that turns the words typed after a program name into
options and arguments, prints help generated from the declarations, and reports mistakes.

The harness command `dsh` is parsed in **two stages**, and this is the fact that decides
the Go choice.

1. **The launcher** parses only what it owns: which *profile* (named composition) to
   boot, extra layer files, and three "print the configuration and exit" switches. It
   hands every remaining word, unchanged, to the composed tree (`apps/cli/src/args.ts:1-16`).
2. **The app** is whichever component the profile mounts to own the command line, for
   example the web server or the headless runner. It declares its own options, prints its
   own `--help`, and publishes the parsed values as a service that other entries read
   (`packages/boot/cmdline/src/index.ts:1-17`). So `dsh web --port 8080` and
   `dsh headless "run the tests"` are the same launcher with different apps, and a custom
   profile brings its own options without the launcher knowing them.

One command, `dsh plugin`, parses nothing: it forwards its arguments to the pnpm package
manager inside the profile directory (`apps/cli/src/args.ts:187-202`).

The surface is small (`docs/cli-help.md`): seven launcher options, seven for `web`, five
plus a free-text task for `headless`, and help only for `acp`, `sdk` and `sdk-minimal`.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/cli/src/args.ts:151-209` | The launcher: `--profile`, `--from-default-profile`, repeatable `--patch`, three dump switches, `-V`; stops at the first word it does not know and passes the rest on; `dsh <name>` is rewritten to `dsh --profile <name>` (`:206-208`) | `Command`, `option` with a collector function, `allowUnknownOption`, `passThroughOptions`, `enablePositionalOptions`, `helpOption(false)`, `exitOverride`, `error`, `addHelpText` |
| `apps/cli/src/args.ts:119-127` | Rules between options: the three dumps exclude each other; a dump takes no app arguments | hand-written checks in the action |
| `packages/boot/cmdline/src/index.ts:165-186`, `:213-221` | The adapter every app uses: parse the launcher's leftover words with the app's own program; turn help, version and errors into an exit request through the launcher instead of `process.exit` | `Command.parse(args, { from: 'user' })`, `exitOverride`, `configureOutput` |
| `packages/bundle/web-app/src/startup.ts:61-111` | Web options: `--host`, `--port`, `--no-open` (a negated switch), `--public-url`, `--trusted-host <authority...>` (takes several values), `--tls-cert` and `--tls-key` (required together, checked by hand at `:105`) | `option`, `helpOption`, `addHelpText`, `error` |
| `packages/bundle/headless/src/startup.ts:55-66`, `:100-116` | Headless options: `--json`, `--session-id`, `--permission-mode`, `--approval-policy` limited to listed choices, repeatable `--add-dir`, and the task as free words; with `--json` a usage error must be a JSON event on stdout, so the app replaces commander's error function and scans the raw words for `--json` first | `Option.choices`, `argument('[task...]')`, overriding `program.error`, `CommanderError` |
| `packages/bundle/acp-app/src/index.ts:26-43`, `packages/bundle/sdk-app/src/index.ts:39-57`, `packages/experimental/cua-mcp/src/index.ts:75-84` | Carriers with no options: help text and "start now" | `Command`, `addHelpText`, `action` |
| `apps/plugin-market-generator/src/cli.ts:47-61`, `:104` | A separate tool: required options, integer options with bounds and defaults, exit status 2 for bad arguments | `requiredOption`, `Option.argParser`, `default`, `showHelpAfterError` |
| `scripts/gen-cli-help.ts` (`docs/cli-help.md:1-8`, `package.json:168-169`) | Generates the reference `docs/cli-help.md` from the help of the launcher, the subcommands and the shipped profiles; a check mode reports a stale file | the help output |

## 3. Tech debt upstream carries

Search performed: every import of `commander`; `patches/` and `pnpm-workspace.yaml` (no
patch or pin); the "Known Limitations" of `packages/boot/cmdline` and `apps/cli`.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L1.4` | **The app contract is the library.** An app hands the adapter a commander object, so the adapter reads a private field (`_actionHandler`) to find out whether an action exists, and recognises commander's errors by the shape of their fields, because an installed plugin "brings its own commander copy" whose error class is a different one. The headless app replaces the library's error function to get a JSON error. | `packages/boot/cmdline/src/index.ts:199-201`, `:223-238`; `packages/bundle/headless/src/startup.ts:102-116` | Resolved by [DH-ADR-COMMANDER-1](#dh-adr-commander-1): apps declare options through a harness type and get errors back as values; the library is an implementation detail of one package, and one binary has one copy of it. |
| A limitation unit owned by the boot area (`packages/boot/cmdline/README.md`, "Launcher flags must precede app arguments") | The split between launcher and app is "the first word the launcher does not know". A launcher option written after an app option goes to the app, a new launcher option can capture a word that used to reach an app, and the launcher consumes one `--` | `apps/cli/src/args.ts:7-11`, `:159-161` | Addressed by [DH-ADR-COMMANDER-2](#dh-adr-commander-2); the boot area writes the row |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-COMMANDER-R1` | Parsing has two stages. The launcher reads its own options and the profile name, and hands every later word to the app unchanged, including `--help`. The launcher never rejects a word that belongs to the app. | `apps/cli/src/args.ts:1-16` |
| `DH-LIB-COMMANDER-R2` | An app is a component chosen by composition, so its options are declared at run time, after the tree is composed, and parsed from a given word list. Nothing is registered globally. | `packages/boot/cmdline/src/index.ts:165-186` |
| `DH-LIB-COMMANDER-R3` | Options use the common double-dash form: `--name value` and `--name=value`, single-letter short forms, on/off switches including a `--no-` form, options that repeat, options limited to listed choices, numbers with bounds, and free words. Options may come after free words: in `dsh headless "run the tests" --json`, `--json` is an option. `--` ends the options. | the web, headless and generator use sites |
| `DH-LIB-COMMANDER-R4` | Parsing never ends the process and never prints by itself. It returns the values, a help request or a usage error. The caller prints to the writers it was given and asks the launcher for a bounded exit, so a JSON-mode app can report a usage error as a JSON event. | `packages/boot/cmdline/src/index.ts:213-221`; `packages/bundle/headless/src/startup.ts:102-116` |
| `DH-LIB-COMMANDER-R5` | Rules between options are declared, not hand-written: "at most one of", "all or none of". A violation is a usage error that names the options. | `apps/cli/src/args.ts:119-121`; `packages/bundle/web-app/src/startup.ts:105` |
| `DH-LIB-COMMANDER-R6` | Help is generated from the declarations: usage line, description, arguments, options, examples. `dsh --help` shows the launcher's help; `dsh <profile> --help` shows the app's. Help exits with status 0; a usage error exits non-zero after one line that says what was wrong. | `docs/cli-help.md:25-59`, `:178-211` |
| `DH-LIB-COMMANDER-R7` | A reference of every command's help is generated from the running program and a gate fails when it is stale. | `docs/cli-help.md:1-8`; [DH-ADR-TOOLCHAIN-10](../upstream/toolchain.md#dh-adr-toolchain-10) |
| `DH-LIB-COMMANDER-R8` | Fixed subcommands can nest (`dsh plugin install ...`), and a subcommand can take the rest of the line unparsed when it forwards it to another program. | `apps/cli/src/args.ts:187-202` |
| `DH-LIB-COMMANDER-R9` | A parsed option overrides the value written in configuration without rewriting the configuration. | `packages/boot/cmdline/src/index.ts:9-15`; in Go the `launch` and `use` names of [DH-ADR-LOADER-3](cordis-loader.md#dh-adr-loader-3) |

## 5. Go choice

**Decision.** `github.com/spf13/pflag` parses options in both stages.
`github.com/spf13/cobra` provides the fixed command tree, help and shell completion in
`cmd/dsh`. Components see neither: they declare an app command line through a harness
package.

**Why.** The standard `flag` package fails R3 in a way that is unsafe, not just
inconvenient. It stops at the first free word (its documented behaviour; the probe
observation, unverified and to be measured by `DH-T-COMMANDER-1`, is that
`dsh headless "run the tests" --json` gives `json=false` and the task
`run the tests --json`, with no error). For an option such as `--permission-mode` that
means a run with different permissions than the user asked for. Repairing this by hand
means writing argument reordering, which is what `pflag` already is.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **`pflag` for options, `cobra` for the command tree (chosen)** | Options after free words; `--name=value`; short forms; repeatable and custom-validated options; `SetInterspersed(false)` gives the launcher split exactly; cobra adds nested commands, generated help, declared option rules (`MarkFlagsMutuallyExclusive`, `MarkFlagsRequiredTogether`), unparsed pass-through (`DisableFlagParsing`) and completion scripts for four shells; neither prints or exits unless asked | Three modules are linked: cobra, pflag and, on Windows only, a 2022 helper that detects a double-click start. cobra adds `help` and `completion` commands unless told not to. | cobra v1.10.2, released 2025-12-03, default branch last commit 2026-07-11, Apache-2.0. pflag v1.1.0, released 2026-10-29, BSD-3-Clause, no requirements. `github.com/inconshreveable/mousetrap` v1.1.0, 2022-11-27, Apache-2.0. All pure Go. cobra's `go.mod` also lists a manual-page generator and `go.yaml.in/yaml/v3`; the probe observation (unverified, to be measured by the first build of `src/harness`) is that neither is linked unless `cobra/doc` is imported, and that cobra v1.10.2 builds against pflag v1.1.0. Checked 2026-11-18 with `go list -m -json`, `go mod download -json` and the licence files in the module cache. |
| Standard library `flag` | No dependency | Options after a free word are silently treated as text (documented; probe unverified); help prints single-dash names; no short and long pairs, no option rules, no command tree. All of it would be own code. | Go 1.27.4 |
| `pflag` alone | One small module | The command tree, help layout, option rules and completion become own code | as above |
| `github.com/alecthomas/kong` | Options declared as a struct with tags; choices, negated switches and option groups built in; no run-time dependencies | A second way of describing a struct next to the schema package's ([DH-ADR-SCHEMA-1](zod.md#dh-adr-schema-1)); no completion; less widely known | v1.17.0, released 2026-10-30, MIT (`COPYING`). Same check, no probe. |
| `github.com/urfave/cli/v3` | Command tree and help in one module | Own flag parser with its own rules for option order; not evaluated in the probe | v3.14.0, released 2026-10-02, MIT. Same check, no probe. |

**What the probe was reported to show** (unverified, to be measured by
`DH-T-COMMANDER-1`; cobra root with `Flags().SetInterspersed(false)` and free arguments
allowed; pflag for the app):

| Input | Result |
|---|---|
| `dsh web --port 8080` | launcher runs with `web --port 8080` untouched |
| `dsh --patch a.yml --patch b.yml tui --resume x --patch inner` | launcher gets two patches; the app gets `--resume x --patch inner` |
| `dsh web --help` | reaches the app; the launcher prints nothing |
| `dsh --nope web` | error `unknown flag: --nope`, returned, not printed |
| `dsh plugin --profile tui add x --flag` (subcommand with `DisableFlagParsing`) | the subcommand receives all five words |
| app: `"run the tests" --json` | `json=true`, free words `["run the tests"]` |
| app: `-- --json` | `json=false`, free words `["--json"]` |
| app: `-h` | returns `pflag.ErrHelp`; prints nothing |

**Tradeoff.** Two third-party modules for a small surface. cobra's help layout replaces
commander's, so help text is not the same as upstream's.

<a id="dh-adr-commander-1"></a>
### DH-ADR-COMMANDER-1 Apps declare options through a harness type, not a library type

- **Decision.** The package `internal/cli/cmdline` (area A24 owns it) defines `Spec`: name,
  description, examples, options (name, short form, kind, default, choices, repeatable,
  help), option rules and free-argument rule. An app component passes a `Spec` and a
  target struct to `Parse` and receives the values, a help request or a `*UsageError`.
  The package builds a `pflag.FlagSet` internally. `cmd/dsh` builds its cobra tree from
  the same `Spec` type for fixed commands.
- **Why.** `UP-DEBT-found.L1.4` and R2, R4: upstream made the library's object the
  contract and then had to reach into its private fields and replace its functions.
  Verdict on upstream: keep app-owned command lines; drop the library as their contract.
- **Tradeoff.** A feature of the library is unavailable to apps until `Spec` exposes it.
- **Serves.** DH-O-13, DH-O-14. **Verified by.** DH-T-COMMANDER-2, DH-T-COMMANDER-3.

<a id="dh-adr-commander-2"></a>
### DH-ADR-COMMANDER-2 Launcher options come before the profile name

- **Decision.** The grammar is `dsh [launcher options] <profile> [app words...]` and
  `dsh <command> ...` for fixed commands. Everything after the profile name belongs to
  the app. A profile may not have the name of a fixed command.
- **Why.** Upstream splits at "the first word the launcher does not know", which makes
  the meaning of a line depend on the full list of launcher options and lets a new
  launcher option take a word from an app. A positional split is what the launcher stage
  of `pflag` does natively, and it is how `git` and `docker` treat global options.
  Verdict on upstream: change.
- **Tradeoff.** `dsh tui --patch ./extra.yml` (documented upstream at
  `docs/cli-help.md:54`) becomes `dsh --patch ./extra.yml tui`. Because the app would
  otherwise receive `--patch` and reject it as unknown, the mistake is reported, not
  silent.
- **Serves.** DH-O-14. **Verified by.** DH-T-COMMANDER-1.

`dsh plugin`, which forwards to pnpm, has nothing to forward to once plugins are
processes ([DH-ADR-4](../design.md#dh-adr-4)) installed from local directories
([DH-ADR-13](../design.md#dh-adr-13)). It becomes ordinary nested commands, which area
A24 lists (`list`, `inspect`, `add`, `remove` with `--purge`, `grant`, `revoke`,
`enable`, `disable`, `restart`; [../areas/app-cli.md](../areas/app-cli.md) §6.2.5).
There is no `install` and no `update`: none downloads or updates a plugin, and none
forwards its words to another program.

## 6. Design notes

```go
type Spec struct {
    Name, Summary string
    Options  []Option            // Kind: Bool, String, Int, Strings (repeatable), Choice
    Rules    []Rule              // OneOf(names...), Together(names...)
    Args     ArgRule             // None, Rest(name, help)
    Examples []Example
}
func Parse(spec Spec, words []string, into any) (Outcome, error) // Outcome: Parsed | HelpRequested
func Help(spec Spec, w io.Writer)
type UsageError struct{ Option, Message string }
```

- **Flow.** `cmd/dsh` runs stage one, resolves the profile and passes the leftover words
  to boot as launch data. The component that owns the command line calls `Parse` with
  those words when it starts. On `HelpRequested` it writes help and requests exit 0; on
  a `*UsageError` it writes one line (or a JSON event) and requests a non-zero exit; on
  success it publishes the values as its service's data view, which config reads through
  `use.<service>` (R9).
- **Dump commands** do not start the tree, so they cannot run an app's parser. They
  reject leftover app words, as upstream does (`apps/cli/src/args.ts:122-127`).
- **Failure and cleanup.** Parsing allocates memory only. Nothing is owned by a fiber.
  The exit request goes through the launcher's bounded shutdown, which unloads the tree;
  the parser never calls `os.Exit`. What `dsh` does when it is started by a
  double-click on Windows was raised by area A24
  ([../areas/app-cli.md](../areas/app-cli.md) §10). Recommended default, not confirmed
  by the owner: nothing special. cobra's notice keeps its default, which tells the
  person to run the program from a terminal; the alternative, starting `dsh web`, is
  not built. The first text of this document
  turned the notice off because a desktop shell would start the same binary; there is
  no desktop shell ([DH-ADR-18](../design.md#dh-adr-18)).
- **Ownership and access.** The launcher owns the argument list and provides it
  read-only. Any component may read it; one component per profile parses it.

## 7. Risks and open questions

1. **Owner of the package.** `internal/cli/cmdline` and the command tree are delivered
   by area A24 ([DH-D-CLI-1](../areas/app-cli.md#dh-d-cli-1)); this specification
   declares no deliverable.
2. **The grammar change of [DH-ADR-COMMANDER-2](#dh-adr-commander-2)** alters documented
   upstream command lines. Area A24 confirmed it and added an explicit
   `--profile <name>` selector and a closed reserved-word list
   ([DH-ADR-CLI-1](../areas/app-cli.md#dh-adr-cli-1)).
3. **External plugins as apps.** A plugin process cannot own the command line unless the
   plugin protocol carries the words and a declared `Spec`. Not designed here.
4. **Shell completion** is new compared with upstream. Completing profile names needs a
   hidden command that lists profiles; optional.

## 8. Verification

Local Go tests that call the package with word lists and buffers. Nothing here has been
run. The probe observations of section 5 are unverified research notes; `DH-T-COMMANDER-1`
is what establishes them.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COMMANDER-1` | local | `DH-LIB-COMMANDER-R1`, `DH-LIB-COMMANDER-R8` | The probe table above as a test: leftover words reach the app byte for byte, `--help` after a profile is not consumed, an unknown launcher option is an error, a forwarding subcommand receives its words unparsed, a profile named like a fixed command is refused |
| `DH-T-COMMANDER-2` | local | `DH-LIB-COMMANDER-R2`, `DH-LIB-COMMANDER-R3`, `DH-LIB-COMMANDER-R5` | A `Spec` built at run time parses `=` and space forms, a `--no-` switch, a repeated option, a choice and a bounded number; an option after free words is an option; `--` ends options; "one of" and "together" rules produce a `*UsageError` naming the options |
| `DH-T-COMMANDER-3` | local | `DH-LIB-COMMANDER-R4`, `DH-LIB-COMMANDER-R6`, `DH-LIB-COMMANDER-R9` | With buffers as writers: `Parse` writes nothing and does not exit; help contains usage, every option and the examples; a usage error is one line; a parsed value is visible to a config expression while the layer file is unchanged |
| `DH-T-COMMANDER-4` | local | `DH-LIB-COMMANDER-R7` | A test renders the help of every fixed command and every shipped profile's app and compares it with the committed reference file; one update switch rewrites the file |

A scenario that starts the built binary with real arguments belongs to the command-line
area; it is a different kind of evidence from these tests.

## 9. Units dispositioned by this spec

`UP-DEP-commander` (replaced), `UP-DEBT-found.L1.4` (resolved).
