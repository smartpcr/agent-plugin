#!/usr/bin/env bash
# run-register.sh -- plan step 18.1.5: run the scripted rows of the "Manual checks
# register" of docs/stories/harness-DSH-M1/evidence/DH-S-1.md .. DH-S-11.md at the
# frozen M1 closure commit, and log what was seen.
#
# Under M1-D-CLOSE-2 A a scripted composed test or probe counts as a manual check
# when a named person runs it at the recorded commit and watches it. This script is
# that run for the "composed test" and "seeded gate" rows. The "by hand" rows and
# the by-hand parts of mixed rows are in by-hand-checklist.md; the signed-in probe
# rows (DH-S-9 9.1, 9.2; DH-S-11 11.1-11.4, 11.7) are run only by the owner from
# the runbooks and are never started here.
#
# Usage:
#   run-register.sh <repo-root> [--os windows|wsl] [--dry-run] [--check-names]
#                   [--one-lane yes|no] [--only 1.2,6.13,...] [--allow-untracked]
#                   [--no-supplements] [--log-dir DIR]
#
#   --os            the lane; detected from uname when omitted, and must match it.
#   --dry-run       print every command and do not run anything (no worktree, no log).
#   --check-names   `go test -list` once per package: every named test exists.
#   --one-lane      run the "one lane" rows too. Default: yes on windows, no on wsl,
#                   so one Windows run plus one WSL run covers every row once.
#   --only          run only these impl.md steps (comma separated).
#   --allow-untracked  accept untracked files (they are listed in the log); tracked
#                   changes are always refused.
#   --no-supplements   do not run the gate's own tests that back the seeded rows the
#                   gate command cannot reach (see SEEDED_SUPPLEMENT).
#   --log-dir       where the log goes; default: this script's directory.
#
# Rows on "Windows, WSL" run on both lanes. The one row on "WSL (rehearsal)" (1.9)
# runs on WSL only. No secret is read or printed; nothing is masked.
#
# Exit status: 0 every run row passed; 1 a row failed; 2 usage; 3 a precondition
# (commit, tree, toolchain, user) is not met and nothing was run.

set -u -o pipefail

FROZEN=593ceaa3efd55d41bf0eeaced5ba7e26acc71966
FROZEN7=593ceaa
GO_FLOOR=go1.27.4

# ---------------------------------------------------------------------------
# The register rows, transcribed from the 11 records (not parsed at run time).
#
#   stage|step|kind|systems|check|units
#
# kind:    composed | seeded | byhand | probe   (the register's "Procedure" kind;
#          a mixed row is filed under its first scripted kind, the rest in "extra")
# systems: one | both | wsl | workstation
# units:   ';'-separated. A composed unit is  module:packages:tests[:flags]
#          (packages space-separated; tests comma-separated, empty = the whole
#          packages; flags as the record states them). A seeded unit is
#          gate:<case>[,<case>...] with the cases of SEEDED_EXPECT. Empty units:
#          not scripted (checklist or signed-in).
# ---------------------------------------------------------------------------
ROWS=(
# DH-S-1 (seeded gates: GOWORK=off; the gate is built from a throwaway worktree)
"DH-S-1|1.1|byhand|one|rule-3 default: three modules build together, the release build ignores go.work|"
"DH-S-1|1.2|seeded|one|seeded violation per gate: the runner names the gate; fake-go on PATH fails setup with exit 2 naming both versions|gate:race-seeded,budget-race,unformatted-file,untidy-module,staticcheck-seeded,coverage-40pct,coverage-blocked,coverage-child,layer-tagged,budget-exceeded,cross-broken,vuln-stdlib,fake-go"
"DH-S-1|1.3|seeded|one|seeded violation: a data race (race-seeded and budget-race fail at test)|gate:race-seeded,budget-race"
"DH-S-1|1.4|seeded|one|seeded violation: an own-analyzer finding (time.Sleep in a _test.go fails vet, nosleep)|gate:vet-sleep"
"DH-S-1|1.5|seeded|one|seeded violation: a package under the floor (coverage-40pct 40% < 85%; coverage-blocked names TestBlocked; coverage-child passes)|gate:coverage-40pct,coverage-blocked,coverage-child"
"DH-S-1|1.6|composed|one|seeded violation: a golden drift (drift reported; -update refused with CI=1); layers excluded (layer-tagged passes test and coverage)|src/harness:./internal/testkit/golden:;gate:layer-tagged"
"DH-S-1|1.7|seeded|one|seeded violation: a budget over its limit (budget-exceeded with DSH_BUDGET_RELAX=1 still fails at budget)|gate:budget-exceeded-relaxed"
"DH-S-1|1.8|byhand|one|rule-3 default: keyless CI lanes and a credentialed preflight|"
"DH-S-1|1.9|seeded|wsl|seeded violation: a target that does not cross-compile (cross-broken names windows/arm64); the rehearsal (by hand)|gate:cross-broken"
# DH-S-2 (src/go; the record's command, with -race)
"DH-S-2|2.1|composed|one|the example's output is unchanged (unload is complete and final)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.2|composed|one|the example's output is unchanged (a cancellation context per fiber)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.3|composed|one|the example's output is unchanged (context values, scoped dispatch)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.4|composed|one|the example's output is unchanged (contained Notify)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.5|composed|one|the example's output is unchanged (around key)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.6|composed|one|the example's output is unchanged (raw, effective and candidate config)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.7|composed|one|the example's output is unchanged (strict config decoding)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
"DH-S-2|2.8|composed|one|the example's output is unchanged (exporters chosen by the creator)|src/go:./cmd/cordis-sample/:TestOutputMatchesGolden:-race"
# DH-S-3 (src/go ./loader/)
"DH-S-3|3.1|byhand|one|two hand-composed layers, one broken: the diagnostic names file, line and column|"
"DH-S-3|3.2|composed|one|rule-3 default: every invalid entry is reported|src/go:./loader/:TestValidateReportsEveryInvalidEntryInDocumentOrder,TestAnEntryWithThreeFaultsReportsThemAllAndIsDisposed"
"DH-S-3|3.3|composed|one|rule-3 default: the expression seam calls the injected evaluator|src/go:./loader/:TestTheExpressionSeamCallsTheInjectedEvaluator,TestAnExpressionTheEvaluatorRejectsIsReportedAtItsPosition"
"DH-S-3|3.4|byhand|one|two hand-composed layers, one broken: the other layer's entry still loads|"
"DH-S-3|3.5|byhand|one|two hand-composed layers, one broken: the layered source with one writable layer|"
"DH-S-3|3.6|composed|one|rule-3 default: a nested loader runs under its owner's context|src/go:./loader/:TestNestedLoaderUnloadsWithItsOwner,TestNestedLoaderIsPrivateToItsOwnerAndReadOnly"
"DH-S-3|3.7|composed|one|rule-3 default: mutations are validated and logged in order|src/go:./loader/:TestLifecycleLogRecordsMutationsInOrder,TestMutationAndRestartErrorsChangeNothing"
"DH-S-3|3.8|byhand|one|two hand-composed layers, one broken: one settled-reload notice|"
"DH-S-3|3.9|composed|one|rule-3 default: the write guard merges a file changed while it waits, and refuses only a writer outside the lock|src/go:./loader/:TestLayerSetWriteGuardMergesAFileChangedWhileItWaits,TestLayerSetAWriterOutsideTheLockIsRefusedThenRetried,TestLayerSetWriteGuardWrapsEveryWrite"
# DH-S-4 (src/harness unless the row names src/sdk)
"DH-S-4|4.1|composed|one|rule-3 default: a bounded JSON-RPC codec with one error table (record an over-limit frame refused with its table code)|src/sdk:./jsonrpc/...:"
"DH-S-4|4.2|composed|one|rule-3 default: one package describes, checks and fingerprints shapes|src/harness:./internal/schema/...:"
"DH-S-4|4.3|composed|one|rule-3 default: the SDK protocol types, method table and derived schema (derived schema equals the committed file)|src/sdk:./protocol/:"
"DH-S-4|4.4|composed|one|rule-3 default: globs, versions, instants and recurrence rules|src/harness:./internal/util/glob/ ./internal/util/version/ ./internal/util/caltime/ ./internal/util/recur/:"
"DH-S-4|4.5|composed|one|rule-3 default: the model vocabulary is a leaf|src/harness:./internal/llm/vocab/:;src/harness:./internal/archtest/:TestImportDirection"
"DH-S-4|4.6|composed|both|rule-3 default: fsx replaces, creates, locks and identifies files|src/harness:./internal/util/fsx/:"
"DH-S-4|4.7|composed|one|rule-3 default: textdiff line diffs and patch format|src/harness:./internal/util/textdiff/:"
"DH-S-4|4.8|byhand|both|search on a real repository: the ignored set equals Git's|"
"DH-S-4|4.9|byhand|both|watcher with a real editor: one notice per save|"
"DH-S-4|4.10|composed|one|rule-3 default: the YAML codec in the harness module|src/harness:./internal/boot/config/:TestSameEntriesThroughJSONAndYAML,TestEncodeLayerKeepsComments"
"DH-S-4|4.11|composed|one|rule-3 default: one YAML dialect, edits keep comments|src/harness:./internal/boot/yamlx/:TestEditKeepsComments,TestEditRefusesUnverifiedText"
"DH-S-4|4.12|composed|one|rule-3 default: the expression engine has no side effects|src/harness:./internal/boot/expr/:TestNamesResolve,TestRejected"
"DH-S-4|4.13|composed|one|rule-3 default: logging writes to the configured destination only|src/harness:./internal/boot/logging/:TestFileDestinationKeepsStdoutClean,TestDebugLevelReachesTheDestination"
"DH-S-4|4.14|composed|one|rule-3 default: a build knows its components by name|src/harness:./internal/boot/catalog/:TestCatalogListsComponentsByName,TestLookupAndPlatforms"
# DH-S-5 (src/harness)
"DH-S-5|5.1|composed|one|rule-3 default: a typed, versioned event vocabulary|src/harness:./internal/session/event/:"
"DH-S-5|5.2|composed|one|rule-3 default: per-agent and per-preset registrations with one owner|src/harness:./internal/session/scope/:"
"DH-S-5|5.3|composed|one|rule-3 default: the store contract and its in-memory reference|src/harness:./internal/session/store/memstore/:TestConformance"
"DH-S-5|5.4|composed|one|rule-3 default: a persisted type cannot change unnoticed|src/harness:./internal/session/store/:TestInventory,TestInventoryCatchesUnbumpedChange,TestPersistedRowsDecode"
"DH-S-5|5.5|composed|one|a hand-written sequence on an in-memory session: accepted in order, delivered once (record the sequence and its seqs)|src/harness:./internal/session/:TestRequestRebuild"
"DH-S-5|5.6|composed|one|a hand-written sequence: history and the request header computed from the log (record messages and header)|src/harness:./internal/session/:TestRequestRebuild"
"DH-S-5|5.7|composed|one|a hand-written sequence: the rebuilt request equals the expected one (sent and rebuilt digests; digest and AsOf unchanged after more appends)|src/harness:./internal/session/:TestRequestRebuild"
"DH-S-5|5.8|composed|one|rule-3 default: brackets balance, checked on append and closed on failure (the bracket tests of DH-D-SESSION-5)|src/harness:./internal/session/:"
"DH-S-5|5.9|composed|one|rule-3 default: one runtime, one settlement per attempt|src/harness:./internal/llm/:TestStreamOneAttemptOneSettlement,TestCompleteHandsOneRecordToTheCaller"
"DH-S-5|5.10|composed|one|rule-3 default: agents found, addressed and owned through one contract|src/harness:./internal/agent/:TestRegistryCreateGetList,TestCompositionWithAStubFactory"
"DH-S-5|5.11|composed|one|rule-3 default: three typed steering points|src/harness:./internal/agent/:TestPreStepRejectAndPanic,TestTurnStoppingKeepOpenWinsInEveryOrder"
"DH-S-5|5.12|composed|one|rule-3 default: one registry for pending approvals and questions|src/harness:./internal/human/:TestPendingIsAddressableByAgent,TestConcurrentAnswersOneWins"
"DH-S-5|5.13|composed|one|rule-3 default: folds registered once and read at one cut|src/harness:./internal/session/views/:TestFoldsReadAtOneCut"
# DH-S-6 (src/harness)
"DH-S-6|6.1|composed|one|rule-3 default: leaf packages (errors, identifiers, home location, media types)|src/harness:./internal/util/herr/ ./internal/util/ident/ ./internal/util/homedir/ ./internal/util/mediatype/:"
"DH-S-6|6.2|composed|both|a child that ignores termination: started through the one spawn package (record the owner line)|src/harness:./internal/world/subprocess/:TestStopTreeStopsAndAwaitsTheTree,TestTreeOwnerIsReported"
"DH-S-6|6.3|composed|both|a child that ignores termination: the whole tree is gone within the bound (record stopped in <time>, no pid alive)|src/harness:./internal/world/subprocess/:TestStopTreeStopsAndAwaitsTheTree,TestContextEndStopsDescendantsAfterChildExit"
"DH-S-6|6.4|composed|one|rule-3 default: launch facts, launch environment and expression scope|src/harness:./internal/boot/launch/:"
"DH-S-6|6.5|composed|both|rule-3 default: the storage directories exist, are private and are checked at start (non-root, non-elevated)|src/harness:./internal/storage/home/:"
"DH-S-6|6.6|composed|one|rule-3 default: configuration names a secret; the value sits behind one store|src/harness:./internal/human/secret/:"
"DH-S-6|6.7|composed|one|rule-3 default: profiles, embedded bundles and the layer stack|src/harness:./internal/boot/profile/:"
"DH-S-6|6.8|composed|one|rule-3 default: one command tree, help and usage errors (by-hand part: dsh --help and one usage error, in the checklist)|src/harness:./internal/cli/ ./internal/cli/cmdline/:"
"DH-S-6|6.9|composed|one|rule-3 default: every command prints and exits by one contract|src/harness:./internal/cli/output/:"
"DH-S-6|6.10|composed|both|a broken required entry: one fatal message and one report file|src/harness:./internal/host/diag/:TestFatalPrintsOneMessageAndWritesOneReport"
"DH-S-6|6.11|composed|both|a broken required entry fails boot once (one message naming the entry, the exit status, one report)|src/harness:./internal/boot/:TestBrokenRequiredEntryFailsBootOnce"
"DH-S-6|6.12|composed|one|rule-3 default: the composed tree is previewed, explained and checked without starting it|src/harness:./internal/boot/config/:"
"DH-S-6|6.13|composed|both|a bad layer edit: the running tree is unchanged (rejection file:line:col, unchanged start count)|src/harness:./internal/boot/...:TestBadLayerEditLeavesRunningTreeUnchanged,TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers"
"DH-S-6|6.14|composed|both|a bad settings edit changes nothing|src/harness:./internal/storage/settings/:TestRefusedEditsChangeNothing,TestDescribeShowsEffectiveValueAfterRejection"
"DH-S-6|6.15|composed|one|rule-3 default: nothing leaves by default; one off-switch per kind|src/harness:./internal/host/sharing/:"
"DH-S-6|6.16|composed|one|rule-3 default: every outbound connection follows one policy|src/harness:./internal/util/egress/:TestNoPermitNoDial,TestWithdrawnPermitCancelsAndRefuses"
"DH-S-6|6.17|composed|one|rule-3 default: the test kit runs real components against scripted boundaries|src/harness:./internal/testkit/...:TestVirtualClockDrivesDeadlinesWithoutSleeping,TestFakeModelScriptsATurn,TestLaunchedProgramAndHomeTornDownByCleanup"
# DH-S-7 (src/harness ./internal/session/store/sqlite/, DSH_E2E_STORE_HOME = a new empty scratch dir)
"DH-S-7|7.1|composed|both|rule-3 default: one database per home, atomic appends, a flush barrier (record the F-2 lines)|src/harness:./internal/session/store/sqlite/:TestConformance,TestMultiEventAppendIsAtomic,TestBudgetStoreAppend"
"DH-S-7|7.2|composed|one|open one database from two test programs: the second writer is refused|src/harness:./internal/session/store/sqlite/:TestSecondWriterRefusedAcrossProcesses"
"DH-S-7|7.3|composed|one|kill a test program between an append and its flush: what the reopened store holds|src/harness:./internal/session/store/sqlite/:TestCrashBetweenAppendAndFlushRecoversHonestly"
"DH-S-7|7.4|composed|one|export a session, import it into an empty home and compare|src/harness:./internal/session/store/sqlite/:TestExportImportRoundTrip"
"DH-S-7|7.5|composed|one|rule-3 default: a process follows a session another process writes; once-per-home duties|src/harness:./internal/session/store/sqlite/:TestFollowerSeesAnotherProcessesCommits,TestDutyHasOneHolderAndMovesAfterAKill"
"DH-S-7|7.6|composed|one|import and delete on a scratch home only (nothing of the deleted session remains, Check clean)|src/harness:./internal/session/store/sqlite/:TestInterruptedDeletionFinishesOnOpen,TestImportRefusals"
# DH-S-8 (src/harness)
"DH-S-8|8.1|composed|one|rule-3 default: program state survives restarts in one typed store|src/harness:./internal/storage/workspace/:TestMachineStateSurvivesRestart;src/harness:./internal/storage/kv/:TestUpdateCommitsAtomicallyAndSurvivesRestart"
"DH-S-8|8.2|composed|one|change a fold's version: its stored state is rebuilt on open (version and seq before and after)|src/harness:./internal/session/views/:TestChangedFoldVersionRebuilds"
"DH-S-8|8.3|composed|one|rule-3 default: the built-in views equal a refold of the log|src/harness:./internal/session/views/:TestRefoldEqualsStoredForEveryBuiltinView"
"DH-S-8|8.4|composed|one|rule-3 default: projects group sessions; a hidden session never keeps working|src/harness:./internal/session/views/:TestListingGroupsByProject;src/harness:./internal/storage/workspace/:TestHiddenSessionStopsWorking"
"DH-S-8|8.5|composed|one|rule-3 default: sessions are listed, observed, traced and followed across forks (record the rows-per-page lines)|src/harness:./internal/session/query/:TestTraceAcrossForks,TestCallerSeesOnlyWhatItMay,TestListPagesAreBounded,TestListPagingIsStableWhileSessionsChange,TestListRowsPerPageDoNotGrow,TestListingReadsStoredSummaries"
"DH-S-8|8.6|composed|both|write a five-megabyte result: the bounded head and the recoverable rest|src/harness:./internal/storage/spill/local/:TestLargeResultSpilledAndRecoverable"
"DH-S-8|8.7|composed|one|rule-3 default: attachments are kept durably and read back verified|src/harness:./internal/storage/blob/:TestCorruptedObjectDetected;src/harness:./internal/storage/attachments/:TestCorruptedAttachmentDetected"
# DH-S-9 (9.1, 9.2: the owner's signed-in probe, never started here)
"DH-S-9|9.1|probe|workstation|one streamed request per signed-in client: one settlement with usage (signed-in, owner only)|"
"DH-S-9|9.2|probe|workstation|a signed-out or expired session: one settlement, no retry, no fallback (signed-in, owner only)|"
"DH-S-9|9.3|composed|one|rule-3 default: token usage and context occupancy are read from the log (and the usage of the 9.1 settlement, from the signed-in run)|src/harness:./internal/llm/meter/:TestUsageFoldedFromSettlements,TestMeterMeasuresTheSession"
# DH-S-10 (src/harness)
"DH-S-10|10.1|composed|one|rule-3 default: a tool is declared once and registered in a scope|src/harness:./internal/tools/:"
"DH-S-10|10.2|composed|one|rule-3 default: the system prompt and tool list are assembled deterministically|src/harness:./internal/tools/prompt/:"
"DH-S-10|10.3|composed|one|an approval with no answerer is denied, with an audit record (record the outcome and the approval code)|src/harness:./internal/tools/core/:TestNoAnswererMeansNoThroughTheExecutor"
"DH-S-10|10.4|composed|one|arguments that break the schema: the body never runs (validation code, body counter 0)|src/harness:./internal/tools/core/:TestSchemaBreakingArgumentsNeverReachTheBody,TestDenialEnforcedInTheExecutor"
"DH-S-10|10.5|composed|one|rule-3 default: enforced deadlines that never hold a turn|src/harness:./internal/tools/core/:TestDeadlineDoesNotHoldTheTurn,TestDeadlineGraceEndsAtExpiryPlusGrace"
"DH-S-10|10.6|composed|one|rule-3 default: no result larger than the budget reaches a model|src/harness:./internal/tools/core/:TestOversizedResultSpilledBeforeReachingTheModel;src/harness:./internal/storage/spill/policy/:TestPolicyNeverOverBudget"
"DH-S-10|10.7|composed|one|rule-3 default: input is durable at once and runs at most once|src/harness:./internal/agent/:TestInboxSendIsDurableBeforeItReturns,TestClaimedItemRunsAtMostOnceAcrossACrash"
"DH-S-10|10.8|composed|one|rule-3 default: the model asks the user a question, waits or moves on|src/harness:./internal/human/question/:TestQuestionAnsweredThroughTheInbox,TestTimedQuestionReturnsPendingAndTheLateAnswerIsOneUserMessage"
"DH-S-10|10.9|composed|one|rule-3 default: a person runs a command against an agent from any client|src/harness:./internal/human/command/:TestCommandRunsAttributedAndDeliversThroughTheInbox"
"DH-S-10|10.10|composed|one|give a grant, end the session, resume it: the grant is gone (empty grant list, grant/ended{session-ended}, the call asks again)|src/harness:./internal/human/approval/:TestGrantDoesNotSurviveResume"
# DH-S-11 (11.1-11.4, 11.7: the owner's signed-in M1 probe, never started here)
"DH-S-11|11.1|probe|workstation|M1, one prompt with one test tool: each step uses one logged model selection (signed-in, owner only)|"
"DH-S-11|11.2|probe|workstation|M1, one prompt with one test tool: the step's tools are fixed once, its calls run in an ordered batch (signed-in, owner only)|"
"DH-S-11|11.3|probe|workstation|M1, one prompt with one test tool: every turn and step is bracketed and closed (signed-in, owner only)|"
"DH-S-11|11.4|probe|workstation|M1, a second run cancelled mid-stream: the cause on turn/end (signed-in, owner only)|"
"DH-S-11|11.5|composed|one|rule-3 default: agents are created, resumed and torn down as one transaction (record create, resume and teardown)|src/harness:./internal/agent/:"
"DH-S-11|11.6|composed|one|rule-3 default: one delegation seam; every child has one owner (the M1 legs of DH-T-SUBAGENT-1, -2, -3, -9)|src/harness:./internal/delegate/...:"
"DH-S-11|11.7|probe|workstation|M1, one prompt with one test tool: the request rebuilt from the log equals the one sent (signed-in, owner only)|"
)

# The part of a mixed row that is not scripted, for the summary.
declare -A EXTRA=(
  [1.6]="" [1.9]="by hand: the -rehearse.txt of the WSL lane run (checklist)"
  [6.8]="by hand: dsh --help and one usage error (checklist)"
  [9.3]="the usage of the 9.1 settlement (signed-in run)"
)

# Seeded-gate cases: what the runner must print. kind|gate|detail|env
#   fail  : "harness-gate: FAILED at gate <gate>", exit 1, no later gate started,
#           and <detail> in the output. gate "*" accepts any gate.
#   pass  : "harness-gate: PASS <gate> (" for each gate of the comma list. On a
#           single-module root the run then stops at `trace` (no tools/harness-trace),
#           which is recorded and does not fail the case.
#   setup : exit 2 and <detail> in the output, no gate started.
declare -A SEEDED_EXPECT=(
  [race-seeded]="fail|test|data race detected|"
  [budget-race]="fail|test|data race detected|"
  [unformatted-file]="fail|gofmt|add.go|"
  [untidy-module]="fail|modules|golang.org/x/mod|"
  [staticcheck-seeded]="fail|staticcheck|SA4000|"
  [coverage-40pct]="fail|coverage|40% < 85%|"
  [coverage-blocked]="fail|*|TestBlocked|"
  [coverage-child]="pass|coverage||"
  [layer-tagged]="pass|test,coverage||"
  [budget-exceeded]="fail|budget||"
  [budget-exceeded-relaxed]="fail|budget||DSH_BUDGET_RELAX=1"
  [cross-broken]="fail|cross-compile|windows/arm64|"
  [vuln-stdlib]="fail|vulncheck|GO-2099-0001|"
  [vet-sleep]="fail|vet|time.Sleep in a test file|"
  [fake-go]="setup|-|toolchain go1.99.0 is not the go floor go1.27.4|"
)
# The testdata module each case copies (vet-sleep adds a seeded _test.go file).
declare -A SEEDED_MODULE=(
  [budget-exceeded-relaxed]=budget-exceeded [vet-sleep]=coverage-40pct [fake-go]=unformatted-file
)
# The gate's own tests that prove a case at the gate-function level, for the cases
# the gate command cannot reach on a single-module root (it stops at `trace`, gate 7,
# before budget, cross-compile and vulncheck; coverage-blocked fails at `test` after
# go test's 10-minute timeout, before `coverage`). Logged as SUPPLEMENT; they do not
# decide the row's RESULT.
declare -A SEEDED_SUPPLEMENT=(
  [coverage-blocked]=TestCoverageBinaryIsBounded
  [budget-exceeded]=TestBudgetGateClearsOverrides
  [budget-exceeded-relaxed]=TestBudgetGateClearsOverrides
  [cross-broken]=TestCrossCompileNamesFailingTarget
  [vuln-stdlib]=TestVulncheckGateUsesTheDatabase
)
GATE_ORDER=(gofmt vet staticcheck modules test coverage trace scenario-index budget cross-compile vulncheck self)

# ---------------------------------------------------------------------------
usage() { sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

ROOT="" OS="" DRY=0 CHECK=0 ONE_LANE="" ONLY="" ALLOW_UNTRACKED=0 SUPPLEMENTS=1
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
LOG_DIR="$SCRIPT_DIR"
while (($#)); do
  case $1 in
    --os) OS=${2:-}; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    --check-names) CHECK=1; shift ;;
    --one-lane) ONE_LANE=${2:-}; shift 2 ;;
    --only) ONLY=${2:-}; shift 2 ;;
    --allow-untracked) ALLOW_UNTRACKED=1; shift ;;
    --no-supplements) SUPPLEMENTS=0; shift ;;
    --log-dir) LOG_DIR=${2:-}; shift 2 ;;
    -h|--help) usage ;;
    -*) echo "run-register: unknown option $1" >&2; usage ;;
    *) [[ -z $ROOT ]] || { echo "run-register: one repository root only" >&2; exit 2; }; ROOT=$1; shift ;;
  esac
done
[[ -n $ROOT ]] || usage
ROOT=$(cd "$ROOT" 2>/dev/null && pwd) || { echo "run-register: no such directory" >&2; exit 2; }

detect_os() {
  case $(uname -s) in
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    Linux) if grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null; then echo wsl; else echo linux; fi ;;
    *) echo unknown ;;
  esac
}
HOST_OS=$(detect_os)
[[ -n $OS ]] || OS=$HOST_OS
case $OS in windows|wsl) ;; *) echo "run-register: --os must be windows or wsl (host: $HOST_OS)" >&2; exit 2 ;; esac
if [[ $OS != "$HOST_OS" ]]; then
  echo "run-register: --os $OS does not match this host ($HOST_OS); run it on that lane" >&2; exit 2
fi
if [[ -z $ONE_LANE ]]; then [[ $OS == windows ]] && ONE_LANE=yes || ONE_LANE=no; fi
case $ONE_LANE in yes|no) ;; *) echo "run-register: --one-lane yes|no" >&2; exit 2 ;; esac
EXE=""; [[ $OS == windows ]] && EXE=.exe

# A path as the native go and gate binaries see it.
native() { if [[ $OS == windows ]]; then cygpath -w "$1"; else printf '%s\n' "$1"; fi; }

export GOTOOLCHAIN=$GO_FLOOR GOWORK=off
export GIT_OPTIONAL_LOCKS=0   # git status must not refresh the index of the tree it checks
unset GOFLAGS
if [[ $OS == wsl && -x /usr/local/go/bin/go ]]; then PATH=/usr/local/go/bin:$PATH; fi

STAMP=$(date -u +%Y%m%dT%H%MZ)
LOG=/dev/null
if ((DRY == 0 && CHECK == 0)); then
  mkdir -p "$LOG_DIR" || exit 2
  LOG="$LOG_DIR/register-$OS-$FROZEN7-$STAMP.log"
  : > "$LOG" || exit 2
fi
out() { printf '%s\n' "$*" | tee -a "$LOG"; }

# ---------------------------------------------------------------------------
# Preconditions (checked, never changed). A dry run or a name check reports them.
precheck() {
  local bad=0 head status tracked untracked
  head=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null)
  if [[ $head != "$FROZEN" ]]; then out "PRECONDITION: HEAD is ${head:-unknown}, not $FROZEN"; bad=1; fi
  status=$(git -C "$ROOT" status --porcelain --untracked-files=all 2>&1)
  tracked=$(printf '%s\n' "$status" | grep -v '^??' | grep -v '^$' || true)
  untracked=$(printf '%s\n' "$status" | grep '^??' || true)
  if [[ -n $tracked ]]; then out "PRECONDITION: tracked changes:"; out "$tracked"; bad=1; fi
  if [[ -n $untracked ]]; then
    if ((ALLOW_UNTRACKED)); then out "untracked files (accepted with --allow-untracked):"; out "$untracked"
    else out "PRECONDITION: untracked files (pass --allow-untracked to accept them):"; out "$untracked"; bad=1; fi
  fi
  local v; v=$(go version 2>&1)
  if [[ $v != *"$GO_FLOOR "* ]]; then out "PRECONDITION: go version is '$v', not $GO_FLOOR"; bad=1; fi
  if [[ $OS == wsl ]]; then
    if [[ $(id -u) == 0 ]]; then out "PRECONDITION: running as root (row 6.5 needs a non-root user)"; bad=1; fi
  else
    if net session >/dev/null 2>&1; then out "PRECONDITION: the shell is elevated (row 6.5 needs a non-elevated user)"; bad=1; fi
  fi
  return $bad
}

header() {
  out "== dsh M1 manual-check register run (plan step 18.1.5) =="
  out "script        : $0"
  out "script sha256 : $(sha256sum "$0" | cut -d' ' -f1)"
  out "repository    : $ROOT"
  out "commit        : $(git -C "$ROOT" rev-parse HEAD 2>/dev/null) (frozen: $FROZEN)"
  out "branch        : $(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null)"
  out "os (lane)     : $OS"
  out "go version    : $(go version 2>&1)"
  out "env           : GOTOOLCHAIN=$GOTOOLCHAIN GOWORK=$GOWORK GOFLAGS=<unset> CGO_ENABLED(go env)=$(go env CGO_ENABLED 2>/dev/null) CC=$(go env CC 2>/dev/null)"
  if [[ $OS == windows ]]; then
    out "windows       : $(cmd //c ver 2>/dev/null | tr -d '\r' | grep -v '^$')"
    out "uname         : $(uname -srvm)"
  else
    out "kernel        : $(uname -srvm)"
    out "os-release    : $(. /etc/os-release 2>/dev/null; echo "${PRETTY_NAME:-unknown}")"
    out "wsl distro    : ${WSL_DISTRO_NAME:-none}"
    out "pid 1         : $(ps -p 1 -o comm= 2>/dev/null) (systemd in /etc/wsl.conf: $(grep -i '^\s*systemd' /etc/wsl.conf 2>/dev/null | tr -d ' ' || echo unset))"
    out "checkout fs   : $(stat -f -c %T "$ROOT" 2>/dev/null)"
  fi
  out "user          : $(id -un 2>/dev/null || echo "$USERNAME") (uid $(id -u))"
  out "one-lane rows : $ONE_LANE; only: ${ONLY:-all}; supplements: $SUPPLEMENTS"
  out "started (UTC) : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  out "log           : $LOG"
  out ""
}

# ---------------------------------------------------------------------------
TMPBASE="" WT="" GATE=""
cleanup() {
  [[ -n $TMPBASE ]] || return 0
  if ((DRY)); then out "cleanup: git worktree remove --force $TMPBASE/wt; git worktree prune; rm -rf $TMPBASE"; TMPBASE=""; return 0; fi
  if [[ -n $WT && -d $WT ]]; then
    out "cleanup: git worktree remove --force $WT"
    git -C "$ROOT" worktree remove --force "$WT" 2>&1 | tee -a "$LOG"
    git -C "$ROOT" worktree prune 2>&1 | tee -a "$LOG"
  fi
  rm -rf "$TMPBASE"
  out "cleanup: removed $TMPBASE"
  TMPBASE=""
}
trap 'cleanup' EXIT
trap 'out "interrupted"; exit 130' INT TERM

ensure_tmp() {
  [[ -n $TMPBASE ]] && return 0
  if ((DRY)); then TMPBASE="${TMPDIR:-/tmp}/dsh-register-XXXXXX"; return 0; fi
  TMPBASE=$(mktemp -d "${TMPDIR:-/tmp}/dsh-register-XXXXXX") || { out "cannot make a temporary directory"; exit 3; }
}

# The throwaway worktree of the frozen commit and the gate built from it (built once
# so its own exit status is seen: `go run` reports every failure as exit 1).
ensure_worktree() {
  [[ -n $GATE ]] && return 0
  ensure_tmp
  WT="$TMPBASE/wt"
  out "setup: git -C $ROOT worktree add --detach $WT $FROZEN"
  if ((DRY)); then GATE="$TMPBASE/bin/harness-gate$EXE"; out "setup: GOWORK=off go -C $WT/tools/harness-gate build -o $GATE ."; return 0; fi
  git -C "$ROOT" worktree add --detach "$WT" "$FROZEN" 2>&1 | tee -a "$LOG" || { out "setup: worktree add failed"; return 1; }
  mkdir -p "$TMPBASE/bin" "$TMPBASE/mods"
  GATE="$TMPBASE/bin/harness-gate$EXE"
  out "setup: GOWORK=off go -C $WT/tools/harness-gate build -o $GATE .  (same program as: GOWORK=off go -C tools/harness-gate run . --root <copy>)"
  (cd "$WT" && go -C tools/harness-gate build -o "$(native "$GATE")" .) 2>&1 | tee -a "$LOG"
  [[ -x $GATE ]] || { out "setup: gate build failed"; GATE=""; return 1; }
}

declare -A GATE_OUT GATE_RC
# gate_case <case>: run the gate on a fresh copy of the case's module, once per run.
gate_case() {
  local c=$1 mod=${SEEDED_MODULE[$1]:-$1} spec=${SEEDED_EXPECT[$1]}
  local env=${spec##*|} copy outf rc cmd
  [[ -n ${GATE_RC[$c]:-} ]] && { out "  (case $c already run in this log; its output is above, re-evaluated here)"; return 0; }
  ensure_worktree || { GATE_RC[$c]=99; return 0; }
  copy="$TMPBASE/mods/$c"
  outf="$TMPBASE/gate-$c.out"
  out "  CASE $c (module testdata/$mod)"
  out "  CMD : cp -r $WT/tools/harness-gate/testdata/$mod $copy"
  if [[ $c == vet-sleep ]]; then
    out "  CMD : write $copy/p/sleep_test.go (a gofmt-formatted test calling time.Sleep)"
  fi
  if [[ $c == fake-go ]]; then
    out "  CMD : GOWORK=off go -C $WT/tools/harness-gate/testdata/fake-go build -o $TMPBASE/fakebin/go$EXE ."
    cmd="PATH=$TMPBASE/fakebin:\$PATH $GATE --root $copy"
  else
    cmd="${env:+$env }$GATE --root $copy"
  fi
  out "  CMD : (cd $WT/tools/harness-gate &&) $cmd"
  if ((DRY)); then GATE_RC[$c]=dry; return 0; fi
  rm -rf "$copy"; cp -r "$WT/tools/harness-gate/testdata/$mod" "$copy"
  if [[ $c == vet-sleep ]]; then
    printf 'package p\n\nimport (\n\t"testing"\n\t"time"\n)\n\nfunc TestSeededSleep(t *testing.T) {\n\ttime.Sleep(time.Millisecond)\n}\n' > "$copy/p/sleep_test.go"
  fi
  if [[ $c == fake-go ]]; then
    mkdir -p "$TMPBASE/fakebin"
    (cd "$WT/tools/harness-gate/testdata/fake-go" && go build -o "$(native "$TMPBASE/fakebin/go$EXE")" .) 2>&1 | tee -a "$LOG"
    (cd "$WT/tools/harness-gate" && PATH="$TMPBASE/fakebin:$PATH" "$GATE" --root "$(native "$copy")") > "$outf" 2>&1
    rc=$?
  elif [[ -n $env ]]; then
    (cd "$WT/tools/harness-gate" && env "$env" "$GATE" --root "$(native "$copy")") > "$outf" 2>&1
    rc=$?
  else
    (cd "$WT/tools/harness-gate" && "$GATE" --root "$(native "$copy")") > "$outf" 2>&1
    rc=$?
  fi
  sed 's/^/    | /' "$outf" | tee -a "$LOG"
  out "  exit: $rc"
  GATE_OUT[$c]=$outf GATE_RC[$c]=$rc
}

# eval_case <case>: prints the verdict line, returns 0 on PASS.
eval_case() {
  local c=$1 spec=${SEEDED_EXPECT[$1]} kind gate detail outf rc why=()
  IFS='|' read -r kind gate detail _ <<<"$spec"
  rc=${GATE_RC[$c]:-}; outf=${GATE_OUT[$c]:-}
  if [[ $rc == dry ]]; then out "  case $c: DRY (expect $kind ${gate} ${detail:+\"$detail\"})"; return 0; fi
  if [[ $rc == 99 || -z $outf ]]; then out "  case $c: FAIL (setup failed)"; return 1; fi
  case $kind in
    fail)
      [[ $rc == 1 ]] || why+=("exit $rc, want 1")
      local named; named=$(grep -o 'harness-gate: FAILED at gate [a-z-]*' "$outf" | head -1 | awk '{print $NF}')
      if [[ $gate == '*' ]]; then [[ -n $named ]] || why+=("no gate named as failed")
      elif [[ $named != "$gate" ]]; then why+=("failed at gate '${named:-none}', want '$gate'"); fi
      if [[ -n $named ]]; then
        local later=0 g
        for g in "${GATE_ORDER[@]}"; do
          ((later)) && grep -q " $g -- " "$outf" && why+=("gate $g started after $named failed")
          [[ $g == "$named" ]] && later=1
        done
      fi
      [[ -z $detail ]] || grep -qF -- "$detail" "$outf" || why+=("output lacks \"$detail\"") ;;
    pass)
      local g; IFS=',' read -ra gs <<<"$gate"
      for g in "${gs[@]}"; do grep -q "harness-gate: PASS $g (" "$outf" || why+=("no 'PASS $g'"); done
      local stop; stop=$(grep -o 'harness-gate: FAILED at gate [a-z-]*' "$outf" | awk '{print $NF}')
      [[ -z $stop ]] || out "  case $c: note: the run then stopped at gate $stop (a single-module root has no tools/harness-trace)" ;;
    setup)
      [[ $rc == 2 ]] || why+=("exit $rc, want 2")
      grep -qF -- "$detail" "$outf" || why+=("output lacks \"$detail\"")
      grep -q 'harness-gate: gate 1/' "$outf" && why+=("a gate started") ;;
  esac
  if ((${#why[@]})); then out "  case $c: FAIL ($(IFS=';'; echo "${why[*]}"))"; return 1; fi
  out "  case $c: PASS"; return 0
}

declare -A SUPP_DONE
# Cases whose register procedure is amended (lead settlement, Phase 18): judged by
# their SEEDED_SUPPLEMENT test, with supplements forced on for them.
declare -A REGISTER_AMENDED=([budget-exceeded]=1 [budget-exceeded-relaxed]=1 [cross-broken]=1 [vuln-stdlib]=1)
supplement() {
  local c=$1 t=${SEEDED_SUPPLEMENT[$1]:-}
  [[ -n $t ]] || return 0
  [[ $SUPPLEMENTS == 1 || -n ${REGISTER_AMENDED[$c]:-} ]] || return 0
  [[ -n ${SUPP_DONE[$t]:-} ]] && { out "  SUPPLEMENT $t: see above (${SUPP_DONE[$t]})"; return 0; }
  ensure_worktree || return 0
  local cmd="(cd $WT &&) GOWORK=off go -C tools/harness-gate test -count=1 -v -run '^($t)\$' ."
  out "  SUPPLEMENT (not the register procedure; the gate-level test of case $c): $cmd"
  if ((DRY)); then SUPP_DONE[$t]=dry; return 0; fi
  local f="$TMPBASE/supp-$t.out" rc
  (cd "$WT" && go -C tools/harness-gate test -count=1 -v -run "^($t)\$" .) 2>&1 | tee "$f" | sed 's/^/    | /' | tee -a "$LOG"
  rc=${PIPESTATUS[0]}
  if [[ $rc == 0 ]] && grep -q "^--- PASS: $t (" "$f"; then SUPP_DONE[$t]=PASS; else SUPP_DONE[$t]=FAIL; fi
  out "  SUPPLEMENT RESULT $t: ${SUPP_DONE[$t]}"
}

# ---------------------------------------------------------------------------
# run_unit module packages tests flags env... ; prints the block, returns 0 on PASS.
run_unit() {
  local mod=$1 pkgs=$2 tests=$3 flags=$4; shift 4
  local envs=("$@") runargs=() pat="" disp f rc why=() n
  read -ra pkgarr <<<"$pkgs"
  read -ra flagarr <<<"$flags"
  if [[ -n $tests ]]; then pat="^(${tests//,/|})\$"; runargs=(-run "$pat"); fi
  local pre=""; [[ " $flags " == *" -race "* ]] && { envs+=(CGO_ENABLED=1); }
  ((${#envs[@]})) && pre="${envs[*]} "
  disp="${pre}GOTOOLCHAIN=$GOTOOLCHAIN GOWORK=off go -C $mod test${flags:+ $flags} -count=1 -v${pat:+ -run '$pat'} $pkgs"
  out "CMD : (cd $ROOT &&) $disp"
  if ((DRY)); then return 0; fi
  ensure_tmp
  f="$TMPBASE/unit.out"
  (cd "$ROOT" && env ${envs[@]+"${envs[@]}"} go -C "$mod" test ${flagarr[@]+"${flagarr[@]}"} -count=1 -v ${runargs[@]+"${runargs[@]}"} "${pkgarr[@]}") 2>&1 | tee "$f" | tee -a "$LOG"
  rc=${PIPESTATUS[0]}
  ((rc == 0)) || why+=("go test exit $rc")
  grep -qE '^(--- FAIL|FAIL)' "$f" && why+=("a test failed")
  if [[ -n $tests ]]; then
    IFS=',' read -ra names <<<"$tests"
    for n in "${names[@]}"; do
      if grep -qE "^--- PASS: $n \(" "$f"; then :
      elif grep -qE "^--- SKIP: $n \(" "$f"; then why+=("$n skipped")
      else why+=("$n not found or not run"); fi
    done
    if ! grep -qE '^--- PASS: ' "$f" && grep -q 'no tests to run' "$f"; then why+=("no tests to run"); fi
  else
    grep -qE '^--- PASS: ' "$f" || why+=("no test passed")
    grep -q 'testing: warning: no tests to run' "$f" && why+=("no tests to run")
    local p
    for p in "${pkgarr[@]}"; do
      [[ $p == *... ]] && continue
      local base=${p#./}; base=${base%/}
      grep -qE "^\?[[:space:]]+\S*/${base}[[:space:]]+\[no test files\]" "$f" && why+=("$p has no test files")
    done
  fi
  local skips; skips=$(grep -cE '^[[:space:]]*--- SKIP: ' "$f")
  ((skips)) && out "NOTE: $skips skipped test or subtest line(s):" && grep -E '^[[:space:]]*--- SKIP: ' "$f" | tee -a "$LOG"
  if ((${#why[@]})); then out "UNIT: FAIL ($(IFS=';'; echo "${why[*]}"))"; return 1; fi
  out "UNIT: PASS"; return 0
}

# Checks after a row's tests, by step.
post_row() {
  local step=$1 stage=$2 fail=0
  case $stage in
    DH-S-2)
      out "POST: git -C $ROOT diff --exit-code -- src/go/cmd/cordis-sample/testdata/output.golden"
      ((DRY)) && return 0
      if git -C "$ROOT" diff --exit-code -- src/go/cmd/cordis-sample/testdata/output.golden | tee -a "$LOG"; then out "POST: golden unchanged"; else out "POST: FAIL the golden changed"; fail=1; fi ;;
  esac
  case $step in
    6.2|6.3)
      if [[ $OS == windows ]]; then
        out "POST: tasklist //FI \"IMAGENAME eq subprocess.test.exe\""
        ((DRY)) && return 0
        local l; l=$(tasklist //FI "IMAGENAME eq subprocess.test.exe" 2>&1 | tr -d '\r'); out "$l"
        grep -qi 'subprocess.test' <<<"$l" && { out "POST: FAIL a subprocess.test process is alive"; fail=1; }
      else
        out "POST: pgrep -a subprocess.test; systemctl --user list-units 'run-*' --no-legend"
        ((DRY)) && return 0
        local l; l=$(pgrep -a subprocess.test 2>&1); out "pgrep: ${l:-none}"
        [[ -n $l ]] && { out "POST: FAIL a subprocess.test process is alive"; fail=1; }
        out "scopes: $(systemctl --user list-units 'run-*' --no-legend 2>&1 | tr '\n' ' ')"
      fi ;;
  esac
  return $fail
}

declare -A SUMMARY
selected() {
  local step=$1 systems=$2
  if [[ -n $ONLY ]]; then [[ ",$ONLY," == *",$step,"* ]] || { echo only; return; }; fi
  case $systems in
    one) [[ $ONE_LANE == yes ]] && echo run || echo other-lane ;;
    both) echo run ;;
    wsl) [[ $OS == wsl ]] && echo run || echo wsl-only ;;
    workstation) echo signed-in ;;
  esac
}

run_row() {
  local stage step kind systems check units sel result=PASS
  IFS='|' read -r stage step kind systems check units <<<"$1"
  if [[ $kind == byhand ]]; then SUMMARY[$step]="$stage|$kind|$systems|CHECKLIST"; return; fi
  if [[ $kind == probe ]]; then SUMMARY[$step]="$stage|$kind|$systems|SIGNED-IN (owner; not started here)"; return; fi
  sel=$(selected "$step" "$systems")
  case $sel in
    only) SUMMARY[$step]="$stage|$kind|$systems|not selected (--only)"; return ;;
    other-lane) SUMMARY[$step]="$stage|$kind|$systems|not run here (one-lane row; --one-lane no)"; return ;;
    wsl-only) SUMMARY[$step]="$stage|$kind|$systems|not run here (WSL row)"; return ;;
  esac
  out "------------------------------------------------------------------------"
  out "ROW $stage step $step: $check"
  out "kind: $kind; systems: $systems; lane: $OS; commit: $FROZEN7"
  local u
  IFS=';' read -ra ulist <<<"$units"
  for u in "${ulist[@]}"; do
    if [[ $u == gate:* ]]; then
      local cases c
      IFS=',' read -ra cases <<<"${u#gate:}"
      for c in "${cases[@]}"; do gate_case "$c"; done
      for c in "${cases[@]}"; do
        if [[ -n ${REGISTER_AMENDED[$c]:-} ]]; then
          # Register amended by the lead (option a): a single-module root stops at the
          # trace gate, so this case is judged by the gate's own test of that gate.
          eval_case "$c" >/dev/null 2>&1 || true
          out "  case $c: judged by ${SEEDED_SUPPLEMENT[$c]} (register amended: a single-module root cannot reach this gate)"
          supplement "$c"
          ((DRY)) || [[ ${SUPP_DONE[${SEEDED_SUPPLEMENT[$c]}]:-} == PASS ]] || result=FAIL
        else
          eval_case "$c" || result=FAIL; supplement "$c"
        fi
      done
    else
      local mod pkgs tests flags envs=()
      IFS=':' read -r mod pkgs tests flags <<<"$u"
      if [[ $stage == DH-S-7 ]]; then
        ensure_tmp; local sh="$TMPBASE/store-home-$step"; ((DRY)) || mkdir -p "$sh"
        envs=("DSH_E2E_STORE_HOME=$(native "$sh")")
      fi
      run_unit "$mod" "$pkgs" "$tests" "${flags:-}" ${envs[@]+"${envs[@]}"} || result=FAIL
      if [[ $stage == DH-S-7 ]]; then
        out "POST: ls -A $sh (want empty), then remove it"
        if ((DRY == 0)); then
          local left; left=$(ls -A "$sh" 2>&1); out "scratch home after the run: ${left:-empty}"
          [[ -z $left ]] || { out "POST: FAIL the scratch home is not empty"; result=FAIL; }
          rm -rf "$sh"
        fi
      fi
    fi
  done
  post_row "$step" "$stage" || result=FAIL
  [[ -n ${EXTRA[$step]:-} ]] && out "NOT SCRIPTED: ${EXTRA[$step]}"
  if [[ $step == 1.9 && -d "$ROOT/docs/stories/harness-DSH-M1/evidence/runs" ]]; then
    local r; r=$(ls "$ROOT"/docs/stories/harness-DSH-M1/evidence/runs/wsl-$FROZEN7-*-rehearse.txt 2>/dev/null | tail -1)
    out "AID (by-hand part): rehearse file at $FROZEN7: ${r:-none found}"
    [[ -n $r ]] && out "AID: lines with vcs.modified=false: $(grep -c 'vcs.modified=false' "$r"); with vcs.modified=true: $(grep -c 'vcs.modified=true' "$r")"
  fi
  ((DRY)) && result=DRY
  out "RESULT $result"
  SUMMARY[$step]="$stage|$kind|$systems|$result${EXTRA[$step]:+ (+ ${EXTRA[$step]})}"
}

# ---------------------------------------------------------------------------
# --check-names: every named test exists (one `go test -list` per package group).
check_names() {
  declare -A group_names
  local row stage step kind systems check units u mod pkgs tests flags key
  for row in "${ROWS[@]}"; do
    IFS='|' read -r stage step kind systems check units <<<"$row"
    IFS=';' read -ra ulist <<<"$units"
    for u in "${ulist[@]}"; do
      [[ -z $u || $u == gate:* ]] && continue
      IFS=':' read -r mod pkgs tests flags <<<"$u"
      key="$mod|$pkgs"

      group_names[$key]+="${tests:+$tests,}"
      [[ -z $tests ]] && group_names[$key]+="<whole>,"
    done
  done
  local missing=0 k
  for k in $(printf '%s\n' "${!group_names[@]}" | sort | tr ' ' '#'); do
    k=${k//#/ }
    mod=${k%%|*}; pkgs=${k#*|}
    local names; names=$(tr ',' '\n' <<<"${group_names[$k]}" | grep -v '^$' | grep -v '<whole>' | sort -u | paste -sd'|')
    local whole=0; [[ ${group_names[$k]} == *"<whole>"* ]] && whole=1
    local pat="^(${names:-Test.*})\$"; ((whole)) && pat='^Test'
    [[ -n $names && $whole == 1 ]] && pat="^(Test.*)\$"
    read -ra pkgarr <<<"$pkgs"
    echo "LIST: GOWORK=off go -C $mod test -p 2 -list '$pat' $pkgs"
    local f; f=$(mktemp)
    (cd "$ROOT" && go -C "$mod" test -p 2 -list "$pat" "${pkgarr[@]}") > "$f" 2>&1
    local rc=$?
    ((rc == 0)) || { echo "  go test -list exit $rc:"; sed 's/^/    /' "$f"; missing=1; }
    if [[ -n $names ]]; then
      local n; IFS='|' read -ra nl <<<"$names"
      for n in "${nl[@]}"; do
        grep -qx "$n" "$f" || { echo "  MISSING: $n in $mod $pkgs"; missing=1; }
      done
    fi
    if ((whole)); then
      local p
      for p in "${pkgarr[@]}"; do
        [[ $p == *... ]] && { grep -q '^Test' "$f" || { echo "  NO TESTS under $mod $p"; missing=1; }; continue; }
        local b=${p#./}; b=${b%/}
        grep -qE "^\?[[:space:]]+\S*/${b}[[:space:]]+\[no test files\]" "$f" && { echo "  NO TEST FILES: $mod $p"; missing=1; }
      done
    fi
    echo "  listed: $(grep -cE '^(Test|Fuzz|Example|Benchmark)' "$f") names"
    rm -f "$f"
  done
  # The seeded modules and the supplement tests.
  local c m
  for c in "${!SEEDED_EXPECT[@]}"; do
    m=${SEEDED_MODULE[$c]:-$c}
    [[ -d $ROOT/tools/harness-gate/testdata/$m ]] || { echo "  MISSING seeded module testdata/$m (case $c)"; missing=1; }
  done
  local supp; supp=$(printf '%s\n' "${SEEDED_SUPPLEMENT[@]}" | sort -u | paste -sd'|')
  echo "LIST: GOWORK=off go -C tools/harness-gate test -p 2 -list '^($supp)\$' ."
  local f; f=$(mktemp)
  (cd "$ROOT" && go -C tools/harness-gate test -p 2 -list "^($supp)\$" .) > "$f" 2>&1
  for c in ${supp//|/ }; do grep -qx "$c" "$f" || { echo "  MISSING supplement $c"; missing=1; }; done
  rm -f "$f"
  ((missing)) && { echo "check-names: some names do not resolve"; return 1; }
  echo "check-names: every named test, package and seeded module resolves"
}

# ---------------------------------------------------------------------------
summary() {
  out ""
  out "======================================================================"
  out "SUMMARY ($OS, commit $FROZEN7)"
  printf '%-8s %-6s %-9s %-12s %s\n' STAGE STEP KIND SYSTEMS STATUS | tee -a "$LOG"
  local row stage step kind systems s pass=0 fail=0 other=0 hand=0 signed=0 dry=0
  for row in "${ROWS[@]}"; do
    IFS='|' read -r stage step _ _ _ _ <<<"$row"
    IFS='|' read -r stage kind systems s <<<"${SUMMARY[$step]}"
    printf '%-8s %-6s %-9s %-12s %s\n' "$stage" "$step" "$kind" "$systems" "$s" | tee -a "$LOG"
    case $s in PASS*) ((pass++)) ;; FAIL*) ((fail++)) ;; DRY*) ((dry++)) ;; CHECKLIST*) ((hand++)) ;; SIGNED-IN*) ((signed++)) ;; *) ((other++)) ;; esac
  done
  out "rows: ${#ROWS[@]}; PASS $pass; FAIL $fail; DRY $dry; not run on this lane $other; checklist (by hand) $hand; signed-in (owner) $signed"
  if ((SUPPLEMENTS)) && ((${#SUPP_DONE[@]})); then
    local t; for t in "${!SUPP_DONE[@]}"; do out "supplement $t: ${SUPP_DONE[$t]}"; done
  fi
  out "ended (UTC)   : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  ((fail == 0))
}

# ---------------------------------------------------------------------------
if ((CHECK)); then
  echo "check-names at $(git -C "$ROOT" rev-parse --short HEAD) with $(go version)"
  check_names; exit $?
fi

header
if ! precheck; then
  if ((DRY)); then out "(dry run: the preconditions above would refuse a real run)"
  else out "refused: preconditions not met; nothing was run"; exit 3; fi
fi
((DRY)) && out "DRY RUN: commands are printed, nothing is run"
for row in "${ROWS[@]}"; do run_row "$row"; done
cleanup
out "after the run: git status --porcelain --untracked-files=all"
git -C "$ROOT" status --porcelain --untracked-files=all 2>&1 | tee -a "$LOG"
out "after the run: git worktree list"
git -C "$ROOT" worktree list 2>&1 | tee -a "$LOG"
summary; rc=$?
((DRY)) || out "log: $LOG"
exit $rc
