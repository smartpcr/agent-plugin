# resolve.exports: Go specification

> **Status: design, not built.** Upstream facts were read at the pinned commit recorded in
> [../README.md](../README.md). Statements that are reasoning and not read from source
> open with "Inference".

| | |
|---|---|
| **Library ID** | `DH-LIB-RESOLVEEXPORTS` |
| **Upstream unit** | `UP-DEP-resolve.exports` |
| **Upstream version** | 2.0.3 (`pnpm-lock.yaml`) |
| **Kind** | runtime; bundled into one build of `packages/boot/app-boot` (`packages/boot/app-boot/tsdown.config.ts:31`) |
| **Used by** | apps (`plugin-market-generator`), boot (`app-boot`), experimental (`creation-recommendations`) |
| **Go choice** | standard library (`os.Root`, `encoding/json`); the library's own job does not exist in Go |
| **Related areas** | boot (plugin manifest and installation), the plugin protocol host |

## 1. What it is and why upstream needs it

An npm package's `package.json` may contain two maps. `exports` says which file a name
such as `my-plugin/client` stands for, and `imports` does the same for the package's
private `#name` aliases. Each entry can depend on *conditions* (for example "when loaded
by `import`" or "in Node"). Node applies these maps when it loads code.
`resolve.exports` is a small library that applies the same rules without loading
anything, so a program can ask "which file would this name load?".

Upstream needs it because a plugin is an npm package and its entry points, client code
and icon are named through these maps. The harness has to answer the question itself in
three places: when it resolves modules for a profile, when it builds a market index from
package contents, and when it checks a plugin the model has just written.

## 2. How upstream uses it

| Use site (`path:line`) | What it is used for | API used |
|---|---|---|
| `packages/boot/app-boot/src/profile-resolution/resolver.ts:317-345` | Part of the harness's own module resolver for profiles: apply a package's `imports` map to a `#name` request under the active conditions | `imports(manifest, request, { conditions, unsafe: true })` |
| `apps/plugin-market-generator/src/metadata.ts:63-72` | Find the file behind a package subpath such as `./icon`, so the generator can read it from the package's files; a target that leaves the package has no path | `exports(manifest, subpath)` |
| `packages/experimental/creation-recommendations/src/generated-bundle.ts:38`, `:46`, `:66-73` | Before a model-written plugin is installed: every config row's module name, and `./client` when the package has client code, must map to a built file that exists inside the plugin directory | `resolve(manifest, specifier)` |

## 4. Requirements for the Go implementation

What each upstream need becomes once plugins are processes, not npm packages
([DH-ADR-4](../design.md#dh-adr-4)):

| Upstream need | Under DH-ADR-4 |
|---|---|
| Resolve `#name` imports for profile modules | **Disappears.** Go resolves component names in a table compiled into the binary ([AR-ADR-16](../../architecture.md#ar-adr-16)); there is no module resolution at run time. |
| Apply `exports` conditions | **Disappears.** There is one way to start a plugin (its command) and no "import" versus "require" distinction. |
| Find a plugin's icon or other asset | **Stays**, simpler: the manifest names the file by relative path (R1). |
| Check that a generated plugin's entry exists before installing it | **Stays** (R1, R3). |
| A named file must not lead outside the plugin directory | **Stays** and becomes a rule for every manifest path (R2). Upstream checks it with `realpath` in `containedFile` (`packages/experimental/creation-recommendations/src/generated-bundle.ts:87-91`). |

| Req ID | Requirement | Comes from |
|---|---|---|
| `DH-LIB-RESOLVEEXPORTS-R1` | A plugin manifest names its executable (or command) and each asset file directly, as a relative path. There is no name-to-file map and there are no conditions. | the three use sites |
| `DH-LIB-RESOLVEEXPORTS-R2` | Every manifest path is opened relative to the plugin directory in a way that cannot leave it: `..`, an absolute path, and a symbolic link that points outside are all refused. | `apps/plugin-market-generator/src/metadata.ts:60-62`; `packages/experimental/creation-recommendations/src/generated-bundle.ts:76-91` |
| `DH-LIB-RESOLVEEXPORTS-R3` | A path that is missing, is not a regular file, or (for the executable) cannot be executed is an installation error that names the manifest field. It is found before the plugin is installed or started, not when it is first used. | `packages/experimental/creation-recommendations/src/generated-bundle.ts:41-48` |

## 5. Go choice

**Decision.** The standard library. `encoding/json` reads the manifest; `os.Root` (in the
standard library since Go 1.24) opens the plugin directory and refuses any path that
would leave it.

**Why.** The library's job is to interpret Node's `exports` and `imports` maps. Those
maps exist only because a plugin is loaded as a module. A plugin process is started, not
loaded, so a path is enough, and the one hard part that remains, staying inside a
directory, is what `os.Root` is for.

| Option | Pros | Cons | Verified facts |
|---|---|---|---|
| **Manifest paths with `os.Root` (chosen)** | No dependency; containment is enforced by the operating-system calls, not by comparing path strings | Requires the harness to keep one open root per plugin directory while it reads | `go doc os.Root` on Go 1.27.4, 2026-11-18: "Methods on Root can only access files and directories beneath a root directory" |
| Port the `exports` rules | Upstream plugin manifests would be readable | Keeps a Node convention for code that is no longer loaded as modules; compatibility is not required ([DH-ADR-2](../design.md#dh-adr-2)) | n/a |
| `filepath.Clean` and a prefix check | Works on older Go | Comparing strings does not stop a symbolic link that points outside | n/a |

**Tradeoff.** An upstream plugin package cannot be installed as it is; it needs a harness
manifest.

## 7. Risks and open questions

- The manifest format and the installation steps belong to areas A21 and A22
  ([../areas/extensions.md](../areas/extensions.md)); they have no deliverable here. A
  plugin directory arrives by the operator placing it; the harness downloads none
  ([DH-ADR-13](../design.md#dh-adr-13)).
- `os.Root` behaviour for links on Windows should be covered by the test below on each
  supported platform.

## 8. Verification

Local Go test on a temporary directory. Nothing here has been run.

| Test ID | Kind | Proves | How |
|---|---|---|---|
| `DH-T-RESOLVEEXPORTS-1` | local | `DH-LIB-RESOLVEEXPORTS-R1`, `DH-LIB-RESOLVEEXPORTS-R2`, `DH-LIB-RESOLVEEXPORTS-R3` | A plugin directory with a manifest: a valid executable and icon resolve; `../x`, an absolute path, a link to a file outside, a missing file and a directory in place of a file are each refused with an error naming the manifest field; nothing outside the directory is opened |

## 9. Units dispositioned by this spec

`UP-DEP-resolve.exports` (replaced).
