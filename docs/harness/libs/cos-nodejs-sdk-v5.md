# cos-nodejs-sdk-v5 and tencentcloud-sdk-nodejs-teo (release upload): Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference". Nothing here was built, run or uploaded.
>
> Revised on 2026-11-18 after the owner's decisions; see `design.md`. The owner confirmed
> that the vendor release publisher is dropped and that releases are published as GitHub
> release assets. With no desktop application, what is published is the `dsh` archives,
> the checksum file and the update manifest; there are no installers.

| | |
|---|---|
| **Library IDs** | `DH-LIB-COS` (object-storage upload), `DH-LIB-TEO` (cache purge) |
| **Upstream units** | `UP-DEP-cos-nodejs-sdk-v5`, `UP-DEP-tencentcloud-sdk-nodejs-teo` |
| **Upstream versions** | 3.0.0 and 4.1.330 (`apps/desktop-publisher/package.json:15`, `:16`) |
| **Kind** | Release tooling. Declared as runtime dependencies of a private tool that is never shipped (`apps/desktop-publisher/README.md:44`) |
| **Used by** | `apps/desktop-publisher`, and upload scripts under `apps/desktop/scripts` |
| **Go choice** | `DH-LIB-COS`: **toolchain** (release assets published by GoReleaser and the release workflow). `DH-LIB-TEO`: **dropped** by [DH-ADR-COS-1](#dh-adr-cos-1) |
| **Related areas** | [electron-builder.md](electron-builder.md) (what is published), [electron-updater.md](electron-updater.md) (the manifest clients read), [build-test-toolchain.md](build-test-toolchain.md) (`DH-LIB-RELEASER`). Owning area: the toolchain (F7) |

## 1. What it is and why upstream needs it

A built installer helps nobody until it sits at an address users and the updater can
reach. Upstream's address is the vendor's own download domain, served by one cloud
vendor's object storage (COS) behind that vendor's content-delivery network (EdgeOne,
whose API is named TEO). `cos-nodejs-sdk-v5` uploads files to the storage;
`tencentcloud-sdk-nodejs-teo` asks the network to forget its cached copy of a file that
was replaced.

This is upstream's own distribution channel, not part of the product. What the Go
project needs from it is the discipline around the upload, not the vendor.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `apps/desktop-publisher/src/desktop-cos.ts:36` to `:47` | One client with retries, redirects, host switching and clock correction all turned off, and a request deadline | `new COS({...})` |
| `apps/desktop-publisher/src/desktop-cos.ts:48`; `apps/desktop-publisher/src/cos-operation.ts:29` | Removes an empty `Cache-Control` header the SDK would add; per-operation deadline | `before-send` event |
| `apps/desktop-publisher/src/desktop-publish-cos.ts:52`, `:74`; `apps/desktop/scripts/desktop-upload-run.ts:107` | Upload installers, block maps and feed files; read back for verification | `putObject`, `getObject` |
| `apps/desktop-publisher/src/desktop-website-installer.ts:105` | Replace the fixed "latest installer" object | `putObject` |
| `apps/desktop/scripts/desktop-build-version-discovery.ts:108`; `apps/desktop/scripts/installed-update-cos.ts:49` | List published versions; check bucket versioning for qualification | `getBucket`, `getBucketVersioning` |
| `apps/desktop-publisher/src/publish-website-ci.ts:89`; `apps/desktop-publisher/src/desktop-purge-recovery.ts:48`, `:117`; `apps/desktop/scripts/desktop-edgeone.ts:57` | Purge one URL from the network cache after the fixed installer was replaced; query the task afterwards | `teo.v20220901.Client`, `CreatePurgeTask`, `DescribePurgeTasks` |

The process around those calls (`apps/desktop/publishing/README.md`):

| Upstream behaviour | Evidence | Verdict |
|---|---|---|
| Packaging needs no upload credential; upload is a separate, manually approved workflow | `:64`; `apps/desktop-publisher/README.md:11` | **Keep** (`R1`) |
| Before any credential is read, the upload checks the completion record, version, file names, sizes and SHA-512 digests | `:64` | **Keep** (`R2`) |
| Installers upload before the feed file that points at them; old objects are kept | `:64` | **Keep** (`R3`) |
| One streamed request per object with explicit length and digest; never retried; a timeout "does not prove that the object was not stored" | `:90` | **Keep** (`R4`) |
| An audit directory per upload: plan, events, result; a missing result means "not complete" | `:90` | **Keep the principle** (`R5`) |
| A fixed address for the newest installer of each platform, replaced in place, then purged from the cache | `:74`, `:76` | **Change**: the release host's "latest" address for the newest archive; nothing to purge |
| Separate test and production buckets; random release identifiers for test batches | `:34`, `:43` | **Change**: a draft release and a private feed serve as the test deployment |
| A staged rollout raised step by step by a second workflow | `apps/desktop/publishing/ci/README.md:86` | **Change**: an advisory field in the signed manifest ([electron-updater.md](electron-updater.md#dh-adr-electronupdater-4)) |

## 3. Tech debt upstream carries

Search: `patches/` and `pnpm-workspace.yaml` (nothing for either package), the
publisher's README limitations, and the call sites. No assigned `UP-DEBT` unit belongs
to these packages. Two workarounds are visible in the code: every automatic behaviour
of the storage SDK is switched off (`desktop-cos.ts:36` to `:47`), and an event hook
deletes a header the SDK adds by itself (`:48`). Both disappear with the SDK.

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-COS-R1` | Publishing is a separate, approved step with its own credential; no build or signing step can publish | `apps/desktop/publishing/README.md:64` |
| `DH-LIB-COS-R2` | Before anything is uploaded, every artifact's name, size and digest is compared with the signed checksum file of the build | `:64` |
| `DH-LIB-COS-R3` | Artifacts are uploaded before the update manifest; the release is visible to users only after the manifest and all artifacts it names are present and verified | `:64` |
| `DH-LIB-COS-R4` | An upload whose outcome is unknown is not repeated blindly: the remote asset is inspected first, and an asset that exists with the right digest counts as done | `:90` |
| `DH-LIB-COS-R5` | The published release is its own record: an asset list with digests that can be compared with the build's checksum file at any later time | `:90` |

## 5. Go choice

**Decision.** Releases are GitHub release assets. GoReleaser uploads the `dsh` archives
and the checksum file; the final job verifies them, attaches the signed update manifests
and publishes the draft
([DH-ADR-ELECTRONBUILDER-1](electron-builder.md#dh-adr-electronbuilder-1)). There are no
installers to attach. No storage or network SDK is linked.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **GitHub release assets through GoReleaser and the release workflow** (chosen) | Already how this repository releases (`.goreleaser.yaml:55` to `:58`). Draft releases give a private staging state. No vendor account | One host; slow or unreachable from some networks, which is the reason upstream runs its own domain. Inference | `github.com/goreleaser/goreleaser/v2` v2.20.2, MIT: `release` has `draft`, `replace_existing_draft`, `replace_existing_artifacts`, `extra_files` in `pkg/config/config.go`; read 2026-11-18 |
| The same, plus a mirror in object storage through GoReleaser's `blobs` | A second origin an operator controls; `blobs` has `endpoint`, `cache_control` and `content_disposition` | A second credential and a second place to verify. Inference: storage services that speak the S3 protocol can be addressed through `endpoint`; not checked for upstream's vendor | `blobs` fields read in the same file |
| Port the publisher with the vendor's Go SDK | Keeps upstream's channel | Ties the project to one vendor's accounts and network; nothing in the product needs it | `github.com/tencentyun/cos-go-sdk-v5` v0.7.77, MIT, released 2026-11-13 (`go list -m -json`, `LICENSE`); no Go module for the purge API was looked for |

<a id="dh-adr-cos-1"></a>
#### DH-ADR-COS-1 The vendor-specific publisher is not built; the release host is the distribution channel

- **Decision.** Confirmed by the owner on 2026-11-18: the vendor release publisher is dropped and releases are published as GitHub release assets. `apps/desktop-publisher` and the upload scripts are not ported. Object-storage upload is replaced by release assets. The cache purge, the website-installer refresh and the separate test and production buckets are dropped. The update client's source address is configuration, so an operator can point installations at a mirror without a code change.
- **Why.** The publisher exists to feed one vendor's download domain. Its durable content is five rules (R1 to R5), and they hold on any host.
- **Tradeoff.** A recorded exception to [DH-ADR-1](../design.md#dh-adr-1). Distribution depends on one hosting service; users who cannot reach it need a mirror someone operates. There is no purge because nothing is replaced in place: assets belong to a release, and a published release is never changed.
- **Serves.** [DH-O-14](../design.md#dh-o-14). **Verified by.** `DH-T-COS-1`; the release rehearsal `DH-E-TOOLCHAIN-2` in [../upstream/toolchain.md](../upstream/toolchain.md); `DH-E-ELECTRONUPDATER-1` and `DH-E-ELECTRONUPDATER-2` in [electron-updater.md](electron-updater.md) for the manifest and the archives clients read.

**Tradeoff of the whole choice.** Less control over caching, regional reach and
download statistics than an own domain gives.

## 6. Design notes

- **Order and failure.** Draft release created; archives and checksum file uploaded; every asset downloaded back and compared with the checksum file; manifests signed and attached; draft published. A failure at any step leaves a draft, which is invisible to the update check. Re-running replaces assets by name in the draft. Once published, nothing is replaced; a bad release is superseded.
- **Uncertain outcome.** If an upload times out, the job lists the release's assets and compares digests before deciding (`R4`). What stays uncertain is only whether a half-uploaded asset exists in the draft; the next run replaces it.
- **Ownership.** The release workflow owns the draft it created and may delete it. It does not own published releases: those are permanent records.
- **Fixed "latest" address.** Inference (hosting behaviour, not checked here): a "latest release" download address needs asset names without a version in them, so the archives and the manifest are attached under a version-less name as well. The pre-release channel needs its own fixed address ([electron-updater.md](electron-updater.md) section 7).

## 7. Risks and open questions

1. **Reachability** of the release host for the intended users; whether a mirror is operated. Owner decision.
2. **Dropping the publisher** was confirmed by the owner on 2026-11-18 (DH-ADR-COS-1); it is no longer open.
3. **Download counts and rollout telemetry** that an own domain provides are not replaced, and under the owner's opt-in rule for data leaving the machine none is collected.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-COS-1` | local | `DH-LIB-COS-R1` to `DH-LIB-COS-R5` | A test reads the release workflow: the publish job needs the protected environment and runs after verification; with a fake release host, a digest mismatch stops before upload, the manifest is attached last, a re-run after a simulated timeout skips assets that already match and replaces one that does not, and the final asset list equals the checksum file |

Publication against the real hosting service is exercised by the release rehearsal
(`DH-E-TOOLCHAIN-2`, snapshot mode, nothing published) and by the first real release
candidate. Nothing here has been run.

## 9. Units dispositioned by this spec

- `UP-DEP-cos-nodejs-sdk-v5`: replaced (`DH-LIB-COS`, `DH-ADR-COS-1`).
- `UP-DEP-tencentcloud-sdk-nodejs-teo`: dropped (`DH-LIB-TEO`, `DH-ADR-COS-1`).
