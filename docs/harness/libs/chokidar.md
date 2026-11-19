# chokidar: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-CHOKIDAR` |
| **Upstream unit** | `UP-DEP-chokidar` |
| **Upstream version** | 5.0.0 and 4.0.3, both resolved in `pnpm-lock.yaml` |
| **Kind** | runtime (five packages), test-only (two packages) |
| **Used by** | boot, credentials, fs, skill, vendor; tests in boot and experimental |
| **Go choice** | own code (a harness watch service) over the named Go module `github.com/fsnotify/fsnotify`, with stat polling as the fallback |
| **Related areas** | filesystem, skills, credentials, workspace files in the UI, boot; [cordis-plugins.md](cordis-plugins.md) for config reload |

## 1. What it is and why upstream needs it

A **file watcher** tells a program that something on disk changed, so the program does
not have to keep re-reading it. chokidar is the usual JavaScript wrapper around the
operating system's change notifications. It adds the things raw notifications lack:
watching a path that does not exist yet, waiting until a file has stopped growing before
reporting it (**write stabilisation**), treating "delete then create" within a short
window as one change (how editors that **save by rename** look from outside), and an
optional mode that ignores notifications and compares file metadata on a timer
(**polling**).

Upstream needs it for four product behaviours: a credentials file edited by hand takes
effect without a restart; a skill added to a skills folder appears in the model's skill
list; the file tree and open-file views in the UI follow the disk; and edits to profile
configuration (and, for developers, to source code) are applied to the running host.

The existing Go cordis loader does not watch. It polls its config file every 100 ms so
that the framework module needs no third-party code
([AR-ADR-15](../../architecture.md#ar-adr-15)). The harness module may use third-party
modules ([DH-ADR-5](../design.md#dh-adr-5)), so this specification decides how the
harness itself observes the disk.

## 2. How upstream uses it

"Settle" is how long the consumer waits after the last change before it reacts.

| Use site (`path:line`) | What is watched | Events used | Settle and options | Who reacts, and how fast it must be |
|---|---|---|---|---|
| `packages/credentials/credentials-local/src/index.ts:596-613` | One file, the credentials document; the path is first canonicalised so Windows short names match (`packages/util/home-paths/src/index.ts:42`) | every event, plus one reconcile when the watcher becomes ready | write stabilisation 100 ms (`debounceMs`, `:536`), checked every 10 ms or less (`:600`) | Re-reads the file and publishes one event per changed secret. Sub-second: a key saved by hand should work on the next request. |
| `packages/fs/fs-local/src/index.ts:76-97` | One file (through a filtered watch on its parent directory) or the direct entries of one directory; depth 0, never recursive, never polled (`packages/fs/fs-local/README.md:59`) | every event | none | `packages/api/workspace-files/src/changes.ts:79` turns each event into a fresh `stat` and a change frame for the UI file tree and open-file view. Interactive: a fraction of a second. |
| `packages/skill/skill-filesystem/src/index.ts:499-513` | Each skill root, depth 1 (the root and each skill folder's direct entries), up to 128 projects (`:43`) | add, addDir, change, unlink, unlinkDir (`:547`) | write stabilisation 200 ms (`:41`), rename handling on (`atomic: true`), polling optional (`watchUsePolling`, `:83`) | Invalidates the skill catalog "for the next model step" (`packages/skill/skill-filesystem/README.md:81`). Seconds are acceptable. |
| `packages/skill/skill-filesystem/src/index.ts:463-470` | A skill root that does not exist yet: the next missing path segment under its nearest existing ancestor | Node's `fs.watchFile` (stat polling), not chokidar | every 100 ms (`:42`) | Attaches the real watcher when the folder appears (`README.md:151`). |
| `packages/boot/hmr/src/watch-config.ts:47-54`, called at `packages/boot/hmr/src/index.ts:413-414` | One exact file per profile patch file and the profile manifest, including parents that do not exist yet | add, change, unlink (`:76-78`) | write stabilisation on, chokidar's default 2 s (`packages/boot/hmr/README.md:47`); refreshes run one at a time | Recomposes the running profile. Upstream accepts two seconds. |
| `packages/boot/hmr/src/index.ts:436-441`, `:489` | Source-module roots, recursive, filtered by ignore globs. The shipped default is no roots (`root: []`, `packages/boot/hmr/README.md:27`) | change only | 100 ms debounce (`:224`) | Reloads changed code in the running process. Developer feature. |
| `vendor/hmr/src/index.ts:120-129` | The same recursive module watch in the vendored original | change only | 100 ms debounce (`:421`) | No package depends on it ([cordis-plugins.md](cordis-plugins.md) section 2). |
| `packages/boot/plugin-manager/tests/package-reload.spec.ts:15`, `packages/experimental/webworker-runtime/tests/node/chokidar.spec.ts:14` | Tests only: one mocks chokidar's options, the other proves chokidar loads inside the experimental browser worker | not applicable | not applicable | not applicable |

Three facts shape the Go design:

1. Every consumer treats an event as "something may have changed" and then re-reads the
   disk. No consumer uses the event's content.
2. The only recursive tree watch is the code-reload watch, and the Go design does not
   reload code in process ([DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)). What
   remains is single files, one directory level, or two.
3. Three consumers each re-implement "watch a path whose parents do not exist yet"
   (`watch-config.ts:9-25`, `skill-filesystem/src/index.ts:447-461`,
   `home-paths/src/index.ts:42`).

## 3. Tech debt upstream carries

Search performed: every import of `chokidar` in source and tests; `patches/` (no patch);
`pnpm-workspace.yaml` (no pin or exemption); the "Known Limitations" sections of the five
consuming READMEs; `TODO` markers beside the watch code. The README bullets and the
`TODO(file-watch-service)` marker are separate ledger units owned by the area agents; they
are listed here because this library causes them.

| Debt ID | What it is | Evidence (`path:line`) | Resolution in Go |
|---|---|---|---|
| `UP-DEBT-found.L9.1` | There is no shared watch service. Each consumer owns its own watcher and copies the same workaround: after `close()` a stray timer can still report an error, and an error with no listener crashes the process, so every consumer re-attaches an error listener across the close. Two major versions of the library are installed side by side. | `packages/boot/hmr/src/watch-config.ts:92-97`; `packages/credentials/credentials-local/src/index.ts:626-633`; `packages/skill/skill-filesystem/src/index.ts:527-533`; `packages/boot/plugin-manager/package.json:106` against `packages/fs/fs-local/package.json:40` | Resolved by [DH-ADR-CHOKIDAR-1](#dh-adr-chokidar-1): one service, one close contract (`DH-LIB-CHOKIDAR-R6`), one module version. |
| (owned by the skills area) `UP-DEBT-marker.packages/skill/skill-filesystem/src/index.ts:445` | `TODO(file-watch-service)`: extract the watcher and the missing-root logic into a service | `packages/skill/skill-filesystem/src/index.ts:445` | The service in section 6 is that extraction. |
| (owned by the filesystem area) | A file watch is not restored on Linux after its parent directory is deleted and recreated | `packages/fs/fs-local/README.md:134` | `DH-LIB-CHOKIDAR-R3` covers it: a lost parent moves the watch to the nearest existing ancestor. |
| (owned by the boot area) | On macOS a write within milliseconds of registering a watch can be missed | `packages/boot/hmr/README.md:123` | `DH-LIB-CHOKIDAR-R4`: the service reconciles once when a watch becomes ready, as upstream's credentials store already does (`credentials-local/src/index.ts:607-613`). |

## 4. Requirements for the Go implementation

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-CHOKIDAR-R1` | A caller can watch one file path and is told when it is rewritten in place, replaced by a rename (an editor's save), deleted, or created after being absent. | `fs-local/src/index.ts:76-97`, `fs-local/README.md:59`, `credentials-local/src/index.ts:596` |
| `DH-LIB-CHOKIDAR-R2` | A caller can watch the entries of a directory to depth 0 or 1. Recursive tree watching is not offered. | `fs-local/src/index.ts:83`, `skill-filesystem/src/index.ts:504`; section 2, fact 2 |
| `DH-LIB-CHOKIDAR-R3` | A watch on a path whose parent directories do not exist, or stop existing, keeps working and attaches when they appear. | `watch-config.ts:9-25`, `skill-filesystem/README.md:151`, `fs-local/README.md:134` |
| `DH-LIB-CHOKIDAR-R4` | A notification says only "re-read this target". Notifications within the caller's settle window become one. A change made after `Watch` returns is never missed: the service delivers one notification when the watch becomes ready. | section 2, fact 1; `credentials-local/src/index.ts:607-613`; `boot/hmr/README.md:123` |
| `DH-LIB-CHOKIDAR-R5` | Where operating-system notifications are missing or unreliable, the watch falls back to polling by itself, reports which mode it is in, and never stops silently. | `skill-filesystem/src/index.ts:83` (`watchUsePolling`); section 5 |
| `DH-LIB-CHOKIDAR-R6` | Closing a watch returns only after its callback can no longer run. Errors that arrive after close are discarded inside the service. | `UP-DEBT-found.L9.1` |
| `DH-LIB-CHOKIDAR-R7` | Resource use is bounded: a cap on the number of watches and on the entries of one watched directory. Past a cap the watch polls or is refused with a reason; it does not exhaust file descriptors. | `skill-filesystem/src/index.ts:43` (`watchMaxProjects`); section 5 |
| `DH-LIB-CHOKIDAR-R8` | Watch targets are compared by file identity, not by path text, so a symbolic link, a Windows short name or a different letter case names the same watch. | `packages/util/home-paths/src/index.ts:29-42`, `packages/boot/hmr/README.md:81` |

## 5. Go choice

<a id="dh-adr-chokidar-1"></a>
### DH-ADR-CHOKIDAR-1 One watch service: notifications where they work, polling where they do not

- **Decision.** The harness has one watch service, written in the harness module. It
  uses `github.com/fsnotify/fsnotify` for operating-system notifications and its own stat
  polling as the fallback. Every notification is a hint that makes the service re-check
  the target; the caller is told only that the target should be re-read.
- **Why.** The UI file tree needs a reaction within a fraction of a second on directories
  that can hold thousands of entries; polling those every few hundred milliseconds costs
  a directory listing each time. Notifications alone are not enough either: they do not
  arrive on network drives, and they can overflow. Treating them as hints and keeping a
  polling path gives low latency where the operating system helps and correctness where
  it does not.
- **Tradeoff.** One third-party module, and two code paths that must behave the same.
- **Serves.** `DH-LIB-CHOKIDAR-R1` to `R8`.
- **Verified by.** `DH-T-CHOKIDAR-1` to `DH-T-CHOKIDAR-6`, `DH-E-CHOKIDAR-1`,
  `DH-E-CHOKIDAR-2`.

<a id="dh-adr-chokidar-2"></a>
### DH-ADR-CHOKIDAR-2 The cordis loader keeps polling; the harness does not watch trees

- **Decision.** The config-file watch in `src/go` stays a 100 ms poll
  ([AR-ADR-15](../../architecture.md#ar-adr-15)) and is not moved onto the harness watch
  service. The harness watch service offers depth 0 and depth 1 only.
- **Why.** The framework module must stay free of third-party code
  ([DH-ADR-5](../design.md#dh-adr-5)); it watches a handful of small files, for which a
  poll is cheap and already sees rename saves. A poll at 100 ms is also faster than
  upstream's two-second stabilisation window for the same files. Recursive watching was
  needed only for in-process code reload, which the Go design replaces with a process
  restart ([DH-ADR-LOADER-8](cordis-plugins.md#dh-adr-loader-8)).
- **Tradeoff.** Two mechanisms observe the disk. A future feature that needs a whole-tree
  watch (for example a live index of a workspace) has to add it deliberately and face the
  limits in the table below.
- **Serves.** `DH-LIB-CHOKIDAR-R2`, `DH-LIB-CHOKIDAR-R7`.
- **Verified by.** Review: no harness package asks the watch service for a recursive
  watch; `DH-T-CHOKIDAR-5`.

### Options compared

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Chosen:** `github.com/fsnotify/fsnotify` plus own polling fallback | Millisecond latency; no cost while idle; pure Go on every release platform | Not recursive; no polling of its own; one file descriptor per watched file on macOS | v1.10.1, released 2026-05-04, BSD 3-Clause (`LICENSE`), requires only `golang.org/x/sys`, no cgo. Its README lists the backends (inotify on Linux, kqueue on macOS and BSD, `ReadDirectoryChangesW` on Windows), states that polling and recursive watching are not available, that NFS, SMB and FUSE deliver no events, and that kqueue opens one descriptor per file in a watched directory. Checked with `go list -m -json`, `go mod download -json` and by reading `LICENSE`, `go.mod` and `README.md` in the module cache, 2026-11-18. |
| Polling only (standard library) | No dependency; identical on every filesystem; sees rename saves by construction | Latency is the interval; cost grows with interval and entry count: a UI directory of 10,000 entries polled at 250 ms is 40,000 name reads a second while nothing changes | Used today by `src/go/loader/configfile.go` |
| `github.com/rjeczalik/notify` | Recursive watching on macOS and Windows | Needs cgo on macOS (two files import `C`); last release 2023 | v0.9.3, 2023-01-12, MIT; same method, 2026-11-18 |
| `github.com/radovskyb/watcher` | A ready-made polling watcher | Last release 2019; polling is small enough to own | v1.0.7, 2019-08-17; existence and date only, licence not read |

### How the two mechanisms behave where it matters

| Situation | Operating-system notifications (fsnotify) | Polling |
|---|---|---|
| Latency | Milliseconds | Up to one interval |
| Cost while nothing changes | None | One `stat` per watched file, or one directory read per watched directory, every interval |
| Network drives (NFS, SMB), FUSE, the Windows drive seen from WSL, container bind mounts | The module's README says NFS, SMB and FUSE deliver nothing. Inference: the WSL and bind-mount cases behave the same way, because the change is made by a different kernel. | Works wherever `stat` works. Inference: attribute caching on NFS and coarse timestamps on FAT can delay a detection by a few seconds. |
| Editor saves by rename (write a temporary file, rename it over the target) | A watch on the file itself is lost, because the file it referred to is gone. The module's README says to watch the parent directory and filter by name. | Seen by construction: the path now has a different identity, size or time. |
| Very large trees | Linux needs one watch per directory and has a per-user limit (`fs.inotify.max_user_watches`). macOS needs one open file per watched file. Windows can overflow its buffer and drop events. | One `stat` per file per interval: not affordable for a tree. |
| Bursts | The event queue can overflow; the module reports `ErrEventOverflow` | Nothing is lost; changes inside one interval collapse into one |

The harness avoids the large-tree row by not watching trees
([DH-ADR-CHOKIDAR-2](#dh-adr-chokidar-2)).

## 6. Design notes

**Owning package.** `watch`, a harness package that provides a cordis service. Consumers
never import fsnotify.

**Shape.**

```go
type Target struct {
    Path   string        // file or directory; need not exist yet
    Depth  int           // 0: the path itself or a directory's entries; 1: one level below
    Settle time.Duration // quiet period before the callback runs
    Mode   Mode          // Auto (default), Notify, Poll
}

// Watch returns after the watch is active. changed runs on the service's goroutine,
// never concurrently with itself for one watch; err is non-nil when the watch degraded.
func (s *Service) Watch(ctx context.Context, t Target, changed func(err error)) (Watch, error)

type Watch interface {
    Mode() Mode   // what is in effect now
    Close() error // returns after the callback can no longer run
}
```

**How a watch works.**

1. The target path is resolved to its nearest existing ancestor and canonicalised. The
   service adds an fsnotify watch on the *directory* that holds the target (for a file)
   or on the target directory and, at depth 1, on each child directory. It never watches
   a file directly, which is what makes rename saves visible.
2. Each event whose name matches the target marks the watch dirty and restarts its settle
   timer. When the timer fires the service compares the target's identity, size and
   modification time (for a directory: its entry names) with what it last saw, and calls
   back only if something differs. This removes duplicate and self-inflicted events.
3. When the watch becomes active, and after any overflow error, the service runs the same
   comparison once without waiting for an event (`DH-LIB-CHOKIDAR-R4`).
4. A watch is in polling mode when the caller asked for it, when adding the notification
   watch failed (watch limit, descriptor limit), when a watched directory has more
   entries than the cap, or when the filesystem is a network or virtual one. Polling runs
   the comparison of step 2 on a timer: 250 ms for a single file, 1 s for a directory.
   The filesystem type comes from `statfs` on Linux and macOS and `GetDriveType` on
   Windows, through `golang.org/x/sys`
   ([node-fs-path-os-zlib.md](node-fs-path-os-zlib.md)).
5. In `Auto` mode a notification watch on a single file also gets a slow verification
   poll (every 5 s, one `stat`). It catches a filesystem that silently delivers nothing.

**Failure, retry and cleanup.**

- A target that disappears is not an error. The watch moves up to the nearest existing
  ancestor and reports one change; it moves back down when the path reappears.
- A notification failure after start switches the watch to polling and calls back once
  with an error that names the reason, so the consumer can log that it is degraded. The
  watch keeps working.
- Nothing is written to disk by the service, so there is nothing to roll back.

**Ownership.** The service's fiber owns the fsnotify watcher, its goroutine and its
timers; they are released when that fiber unloads. Each `Watch` is registered as an
effect on the *caller's* fiber, so it is closed when the consumer unloads even if the
consumer forgets. Access is separate from ownership: any component may ask for a watch on
a path it is allowed to read; only the fiber that asked may close it.

**Verdict on upstream's design.** Keep the behaviours (stabilisation, rename handling,
missing parents, a readiness reconcile). Change where they live: one service instead of
five private watchers. Drop the recursive code watch and the per-consumer polling switch.

## 7. Risks and open questions

- **macOS descriptors.** A watched directory with many entries costs one descriptor each.
  The per-directory cap (proposed: 2,000 entries, then polling) is a guess that needs a
  measurement on macOS.
- **Filesystem detection is a heuristic.** The verification poll is the safety net for a
  filesystem the detection does not know.
- **Which areas adopt the service** is for their specifications: credentials, skills,
  workspace files and the plugin-directory watch mentioned in
  [cordis-plugins.md](cordis-plugins.md) section 7. This specification creates no
  deliverable; the `watch` package needs an owning area (filesystem is the natural one).
- **A remote workspace** (SSH) cannot be watched by a local service. Whether the remote
  helper polls or the UI refreshes on demand is for the SSH area.

## 8. Verification

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-CHOKIDAR-1` | local | `DH-LIB-CHOKIDAR-R1` | In a temporary directory: write in place, replace by rename, delete and recreate a watched file; each yields exactly one callback after the settle window. Run in `Notify` and in `Poll` mode with the same assertions. |
| `DH-T-CHOKIDAR-2` | local | `DH-LIB-CHOKIDAR-R2`, `R4` | Depth 0 and depth 1 directory watches report added, removed and changed entries; a burst of 100 writes yields one callback; a change two levels down yields none. |
| `DH-T-CHOKIDAR-3` | local | `DH-LIB-CHOKIDAR-R3` | Watch `a/b/c.txt` when `a` does not exist; create the chain; delete `a`; recreate it. A callback follows each transition. |
| `DH-T-CHOKIDAR-4` | local | `DH-LIB-CHOKIDAR-R4`, `R6` | A write racing `Watch` is reported by the readiness reconcile. After `Close` returns, further writes produce no callback, under the race detector. |
| `DH-T-CHOKIDAR-5` | local | `DH-LIB-CHOKIDAR-R5`, `R7` | With an injected notifier that fails on add, overflows, or stays silent, the watch reports `Poll` mode, calls back once with the reason, and still detects changes. A directory above the entry cap starts in `Poll` mode. |
| `DH-T-CHOKIDAR-6` | local | `DH-LIB-CHOKIDAR-R8` | The same file reached through a symbolic link, and on Windows through its short name and a different letter case, resolves to one watch. |
| `DH-E-CHOKIDAR-1` | real environment | `DH-LIB-CHOKIDAR-R1` | **Given** a harness running on Linux, macOS and Windows with a watched credentials file and a watched workspace file. **When** the file is saved from real editors that use different strategies (rename, write in place, backup-then-write: for example Vim, VS Code and Notepad). **Then** each save is reported once within one second and the content read back is the saved content. **Cleanup:** delete the temporary home and workspace. |
| `DH-E-CHOKIDAR-2` | real environment | `DH-LIB-CHOKIDAR-R5` | **Given** a workspace on a real SMB share and on a real NFS mount, and a Windows directory opened from WSL. **When** a file is changed from another machine or from the other side. **Then** the watch reports `Poll` mode and the change is reported within two poll intervals. **Cleanup:** unmount and remove the test files; keep the service log if the mode was wrong. |

Local tests and real-environment scenarios are separate kinds. Nothing here has been run.

## 9. Units dispositioned by this spec

`UP-DEP-chokidar`, `UP-DEBT-found.L9.1`.
