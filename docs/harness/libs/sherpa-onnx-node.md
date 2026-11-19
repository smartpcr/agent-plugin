# sherpa-onnx-node: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Go module facts were checked on 2026-11-18 with Go 1.27.4
> (`go list -m -json`, `go mod download -json`, `LICENSE` and source in the module cache).
> The sherpa-onnx release was checked the same day through the GitHub releases API for
> tag `v1.13.8`, and one archive was downloaded and listed, not run. What the listed
> programs print, and which flags they take, was **not** established.

| | |
|---|---|
| **Library ID** | `DH-LIB-SHERPAONNX` |
| **Upstream units** | `UP-DEP-sherpa-onnx-node`, and the workaround `UP-DEBT-found.L12.3` |
| **Upstream version** | 1.13.8, with one native package per platform (`pnpm-lock.yaml:27507-27535`) |
| **Kind** | runtime, native (a Node binding that carries the ONNX Runtime inference library) |
| **Used by** | experimental (`packages/experimental/speech-to-text-sensevoice`) |
| **Go choice** | own code: a provider that runs an operator-installed recognition program (the documented example is the prebuilt sherpa-onnx runtime) as a supervised child process. The harness ships and downloads neither a runtime nor a model. No Go module, no cgo in the host. A remote provider on `net/http` is the second provider, off unless the operator configures it |
| **Related areas** | X4, which owns the provider registry, the command contract, the recording island and the scenarios ([../areas/x-cua-browser-voice.md](../areas/x-cua-browser-voice.md), `DH-ADR-XCUA-4`, `DH-D-XCUA-4`); A18 for the sharing gate a cloud provider must pass |

## 1. What it is and why upstream needs it

Voice input lets a person dictate a message instead of typing it. It is experimental
and off by default. The browser records, the host turns the recording into text, and
the text is inserted into the unsent draft; nothing is sent to the model until the
person submits (`docs/subsystems/voice-input.md:15-17`).

"Turning a recording into text" is **speech recognition**. Upstream does it on the
user's own machine so that speech never leaves it. That needs two things: a **model**
(SenseVoice, a multilingual recogniser, plus Silero, a small model that finds where
speech starts and stops, called voice activity detection or VAD), both stored in the
ONNX file format; and a **runtime** that executes ONNX models, the native library ONNX
Runtime. `sherpa-onnx-node` is the Node binding of the sherpa-onnx project, which
packages the runtime with recognisers built on it, so the user needs no Python or
compiler (`packages/experimental/speech-to-text-sensevoice/README.md:12`).

Recognition is behind a **provider registry**: providers register under an exact id and
say whether they process `host-local` or in the `cloud`; a failed local provider never
falls back to a cloud one (`docs/subsystems/voice-input.md:9`,
`.agents/notes/implemented/architecture/2026-09-16-experimental-voice-input.md:15`).

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/experimental/speech-to-text-sensevoice/src/inference.ts:41-47` | Load the SenseVoice model and its token table once, CPU only, with a configured thread count | `new OfflineRecognizer(config)` |
| `packages/experimental/speech-to-text-sensevoice/src/inference.ts:48-52` | Load the Silero VAD model with thresholds and a maximum segment of 30 seconds | `new Vad(config, bufferSeconds)` |
| `packages/experimental/speech-to-text-sensevoice/src/inference.ts:54-60` | Per recording: check the input, read 16-bit samples after the 44-byte WAV header, set the language hint, reset the detector | `recognizer.setConfig`, `detector.reset` |
| `packages/experimental/speech-to-text-sensevoice/src/inference.ts:65-92` | Feed 512-sample windows to the detector; recognise each speech segment and join the texts. The first segment is extended backwards by 0.3 seconds because a cold detector reports its start late | `detector.acceptWaveform`, `front`, `pop`, `flush`; `recognizer.createStream`, `decode`, `getResult` |
| `packages/experimental/speech-to-text-sensevoice/README.md:46` | The binding runs in one managed Node subprocess that serialises inference, has a bounded queue, separate deadlines for preparation and inference, and is released after an idle period | subprocess seam, not the library |
| `packages/experimental/speech-to-text-sensevoice/runtime/assets.json:3-28` | Model files pinned by revision, byte count and SHA-256: recogniser 239 MB (8-bit weights) or 938 MB (32-bit), tokens 0.3 MB, VAD 1.8 MB | download code, not the library |
| `packages/experimental/speech-to-text-sensevoice/src/model-sources.ts:13-41` | Choose between configured download origins by a short probe; an explicit origin disables fallback | `fetch` |

Limits upstream sets (`packages/experimental/speech-to-text-sensevoice/src/config.ts:58-81`):
4 MiB of WAV per recording, 4 pending recordings, 120 seconds per inference, one hour
for preparation, 5 minutes idle before the worker is stopped, 2 threads. Language hints
are `auto`, `zh`, `en`, `yue`, `ja`, `ko` (`src/input.ts:5`).

## 3. Tech debt upstream carries

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L12.3` | Buffers returned by the native binding must be copied because the Electron runtime forbids JavaScript views onto native memory | `packages/experimental/speech-to-text-sensevoice/src/inference.ts:67-68`, `README.md:46` | **Not applicable:** it exists only because the binding is loaded into an Electron-hosted Node. The Go design loads no binding into any JavaScript runtime. |
| Untyped binding | The package ships no type declarations; the code declares the four classes it uses by hand | `packages/experimental/speech-to-text-sensevoice/src/inference.ts:14-32`, `:40` | Not applicable: no binding. |
| Platform gaps (an inventory unit of area X4) | CPU only; Windows on ARM and Linux with musl are not verified | `packages/experimental/speech-to-text-sensevoice/README.md:74` | Partly changed: the sherpa-onnx release has a Windows ARM archive (§5). musl remains unverified. |

No patch, pin or install-script exemption names the package (`patches/` and
`pnpm-workspace.yaml` searched).

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-SHERPAONNX-R1` | One complete recording (mono, 16 kHz, 16-bit WAV, at most the configured bytes) and a language hint from the provider's list produce text, the audio duration and the recognition time. | `inference.ts:54-92`, `input.ts:5-21` |
| `DH-LIB-SHERPAONNX-R2` | A `host-local` provider performs recognition on this machine with no network access during recognition. | `voice-input.md:9`; note `2026-09-16…:15` |
| `DH-LIB-SHERPAONNX-R3` | Enabling the feature allocates nothing heavy and fetches nothing: activation only checks that the configured program and model files exist. The harness downloads neither a runtime nor a model; the operator installs both. Where either is missing the provider reports `unavailable` and names what to install. | `README.md:28`, `:36`; note `:19`; [DH-ADR-XCUA-4](../areas/x-cua-browser-voice.md#dh-adr-xcua-4) |
| `DH-LIB-SHERPAONNX-R4` | A bad WAV, an unknown language or an oversized recording is refused before any native code sees it. | `input.ts:16-21` |
| `DH-LIB-SHERPAONNX-R5` | Recognition is serialised, the waiting queue is bounded, one recognition has a deadline, and an idle runtime is released. | `README.md:46`, `config.ts:72-77` |
| `DH-LIB-SHERPAONNX-R6` | Cancelling a running recognition stops the process and waits for it; a cancelled waiting recording never runs. | `README.md:46` |
| `DH-LIB-SHERPAONNX-R7` | Long recordings are split at pauses into segments of bounded length, and the first words are not lost to late speech detection. | `inference.ts:48-52`, `:62-76` |
| `DH-LIB-SHERPAONNX-R8` | Audio is transient: never written to the session log or the application log; any temporary file is owner-only and removed before the result returns. | `voice-input.md:15-17`, `README.md:46` |

## 5. Go choice

**Decision.** Keep the provider seam. The local provider is harness code that starts an
**operator-installed recognition program as a child process**; the documented example
is the prebuilt sherpa-onnx runtime with the SenseVoice model. The harness ships no
recogniser and downloads neither a runtime nor a model. A second provider posts the
recording to an HTTP transcription service the operator configured. Neither is a
default, and a failed provider is never replaced by another.

<a id="dh-adr-sherpaonnx-1"></a>
### DH-ADR-SHERPAONNX-1 Local recognition is a supervised child process running a program the operator installed

- **Choice.** The host links no inference library. The provider's fiber owns a child
  process that runs the program named in configuration. Any program that reads one WAV
  file and prints text fits the command contract, which area X4 owns
  ([DH-ADR-XCUA-4](../areas/x-cua-browser-voice.md#dh-adr-xcua-4)); the program's
  flags are configuration, not code.
- **Why.** ONNX Runtime is a large native library; linking it needs cgo and a shared
  library next to the binary on every platform, which the pure-Go rule for `dsh`
  forbids ([DH-ADR-15](../design.md#dh-adr-15)). Not downloading follows the rule the
  design applies to every operator-installed program (LibreOffice, a browser, a
  computer-use server): nothing is bundled and nothing is fetched
  ([DH-O-10](../design.md#dh-o-10)).
- **Tradeoff.** No one-button set-up: the operator downloads a runtime (16 to 44 MB)
  and about 239 MB of model files by hand, and the harness cannot verify them. This
  replaces the first text of this decision, which made the runtime archive a pinned,
  checksum-verified download of a preparation task. The stock programs are driven
  through their command line, so the model is loaded per recording, and upstream's
  0.3-second onset repair is not available (`DH-LIB-SHERPAONNX-R7` then depends on the
  program's own VAD padding). A helper of our own (second row below) is not built: it
  would be a cgo artifact the project ships, and the design has none.
- **Serves.** [DH-O-10](../design.md#dh-o-10), [DH-O-14](../design.md#dh-o-14).
  **Verified by.** `DH-T-SHERPAONNX-2`, `DH-T-SHERPAONNX-3`, and X4's real-environment
  scenario.

| Option | Needs cgo | What the user downloads | Pros | Cons | Verified facts |
|---|---|---|---|---|---|
| **Chosen:** an operator-installed program (example: the prebuilt sherpa-onnx programs) as a child process | No | Nothing through the harness. By hand: the runtime archive, 16 to 44 MB per platform, plus the models (239 MB by default) | Host stays pure Go; crash and deadline isolation; nothing fetched or verified by the harness | Manual set-up; command-line contract of the example not yet confirmed; per-recording model load; no custom onset repair | Release `v1.13.8` (2026-09-10, Apache-2.0) has `…-linux-x64-shared…`, `…-linux-aarch64-shared-cpu…`, `…-osx-arm64-shared…`, `…-osx-x64-shared…`, `…-osx-universal2-shared…`, `…-win-x64-shared…` and `…-win-arm64-shared-MD-Release…` archives. The Linux x64 archive contains `bin/sherpa-onnx-vad-with-offline-asr`, `bin/sherpa-onnx-offline`, `bin/sherpa-onnx-offline-websocket-server`, `lib/libonnxruntime.so` and `lib/libsherpa-onnx-c-api.so`. GitHub releases API and archive listing, 2026-11-18 |
| Own helper binary with cgo, on `github.com/k2-fsa/sherpa-onnx-go`. **Not built** | Yes, in the helper only | The helper and its shared libraries per platform, plus the models | Exact upstream logic (warm model, VAD loop, onset repair) over a stdio protocol we define | A second, cgo artifact with a C toolchain per platform and shared libraries to package and sign; the project ships one pure-Go binary and no native helper | v1.13.8, 2026-09-11, Apache-2.0; its `go.mod` requires `sherpa-onnx-go-linux`, `-macos` and `-windows` at the same version, which carry the native libraries; `go list -m`, `LICENSE`, `go.mod` read |
| Link the binding into the host | Yes, in `dsh` | Shared libraries beside the binary | One process | Breaks the pure-Go release and puts native inference in the host. **Rejected** | same module |
| Remote service: post the WAV to an OpenAI-style transcription endpoint | No | Nothing | Standard library only (`net/http`, `mime/multipart`); works on every platform | Speech leaves the machine; needs a credential; must be labelled `cloud` and chosen explicitly, never a fallback | n/a (standard library) |
| Recognition in the browser (the Web Speech API, or a WebAssembly recogniser in an island) | No | Nothing, or the model into browser storage | No host work | The Web Speech API is not uniform across browsers and may send audio to the browser vendor; upstream rejected it because it "does not provide a uniform local-processing guarantee" (note `2026-09-16…:25`). A WebAssembly recogniser would be a heavy island that stores a 239 MB model per browser profile. **Rejected** | n/a |
| A generic ONNX binding, `github.com/yalue/onnxruntime_go` | Yes | Shared library plus models | General | Still cgo, and the feature extraction, decoding and VAD that sherpa-onnx provides would have to be rewritten. **Rejected** | v1.38.0, 2026-10-23, MIT; `go list -m`, `LICENSE` |

**Tradeoff.** Stated in the decision. The remote provider is cheap to add and is the
only option on a platform without a recognition program. It sends speech off the
machine, so it exists only when the operator configures it and sends only under area
A18's sharing gate ([DH-ADR-9](../design.md#dh-adr-9)).

**Upstream decisions judged** ([DH-ADR-2](../design.md#dh-adr-2)).

| Upstream decision | Verdict | Why |
|---|---|---|
| Providers are chosen by exact id, with no fallback between them (note `2026-09-16…:15`) | **Keep** | It is a privacy rule: a local failure must not upload speech. |
| Nothing is downloaded or loaded until the user asks; assets are pinned by size and SHA-256 (note `:19`, `runtime/assets.json`) | **Change** | Nothing is downloaded at all: the operator installs the runtime and the models, as for every other external program in the design. The harness has no pinned asset table. |
| Recognition runs in its own managed process with a queue, deadlines and idle release (`README.md:46`) | **Keep** | The Go design gets the same isolation without a Node worker. |
| The runtime arrives as an npm package per platform (`pnpm-lock.yaml:27507-27535`) | **Change** | A Go binary has no package install step; the operator installs the official release archive, or any program that meets the command contract. |
| Onset repair of 0.3 seconds in the recognition loop (`inference.ts:62-76`) | **Drop**, stated as a limit | Not available when a stock program is driven, and the own-helper option is not built. |
| Browser-native recognition rejected (note `:25`) | **Keep** | The reason, no uniform local-processing guarantee, is stronger when the engine is whatever browser the user has. |

## 6. Design notes

The provider lives in the experimental speech package (area X4) and registers in the
speech provider registry under an exact id.

- **Input.** WAV validation is harness code and runs first (`DH-LIB-SHERPAONNX-R4`).
  Inference: the stock programs read a file path, so the recording is written to an
  owner-only temporary file inside the provider's data directory and removed in the
  same function that created it (`DH-LIB-SHERPAONNX-R8`).
- **Preparation.** There is none in the harness. Activation checks that the configured
  program and the model files exist and reports `unavailable`, naming what is missing,
  when they do not. The set-up guide gives the example: which sherpa-onnx archive and
  which model files to place where.
- **Ownership.** The provider's fiber owns the child process, the queue and the
  temporary files. The runtime and the models are *shared* resources: the operator
  installed them, the harness has access and not ownership, and it never deletes or
  updates them.
- **Failure.** A recognition error or a malformed reply fails that recording and
  discards the process. A deadline kills the process tree. A missing program or model
  is `unavailable` with the path that was looked for. Nothing is retried automatically,
  and a failed local provider never hands the recording to another provider.
- **Sandbox.** The child reads the runtime, the models and its one input file, writes
  nothing, and has no network; it runs under the harness sandbox policy like any other
  spawned program.

## 7. Risks and open questions

1. **For X4.** Confirm on each platform that one of the listed programs accepts the
   SenseVoice model, a language hint and a VAD model, and prints the text in a form
   that can be parsed. Until then the documented example is unconfirmed; the command
   contract itself does not depend on it.
2. **For X4.** Per-recording model load time is unmeasured. If it is too slow, the
   option left is the runtime's server program on a loopback port, which then needs a
   per-start secret or a private socket, because any local process could otherwise
   submit audio. It is not designed here.
3. The Windows ARM archive exists but is as unverified as upstream says; musl Linux has
   no archive in the list checked.
4. The harness cannot verify software the operator installed
   ([../design.md](../design.md) §6, residual gaps); the set-up guide names the
   official release and its checksums for the operator to check.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-SHERPAONNX-1` | local | `DH-LIB-SHERPAONNX-R1`, `-R4` | Table of WAV inputs (wrong rate, stereo, truncated header, oversized, unknown language): each is refused before the fake runtime is started; a valid one reaches it with the expected arguments. |
| `DH-T-SHERPAONNX-2` | local | `DH-LIB-SHERPAONNX-R5`, `-R6`, `-R8` | A fake runtime (a test program that sleeps, crashes or prints garbage) drives the queue bound, the deadline, cancellation of running and waiting recordings and idle release; afterwards no process and no temporary file remains. |
| `DH-T-SHERPAONNX-3` | local | `DH-LIB-SHERPAONNX-R3` | With a recording HTTP server as the only reachable address: enabling the provider, activating it and transcribing with a fake program make no request. With the program path or a model file missing, the provider reports `unavailable`, names the missing path and starts no process. |
| `DH-T-SHERPAONNX-4` | local | `DH-LIB-SHERPAONNX-R2` | The remote provider, against a local test server, reports `cloud`, sends the recording once, and is never selected when the local provider fails. |

`DH-LIB-SHERPAONNX-R7` and real recognition quality need a real-environment scenario,
owned by X4 ([DH-E-XCUA-4](../areas/x-cua-browser-voice.md#dh-e-xcua-4)): Given a runtime and model the operator installed on the test machine and a committed
recording with a known sentence that begins at the first sample; When it is transcribed
through the built binary; Then the text contains the first word and the sentence;
Cleanup removes the temporary file. A skipped scenario leaves the requirement open.
Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-sherpa-onnx-node`, `UP-DEBT-found.L12.3`.
