# nestgo Architecture

> **Status:** Implemented. nestgo drives the project's own TypeScript 7 compiler as a
> subprocess; the embedded compiler and its `go:linkname` shims are gone. Remaining work
> is listed under Roadmap.

## 1. What nestgo is

nestgo is a drop-in replacement for `nest build` and `nest start` for NestJS projects
using the standard-mode `tsc` builder. It reads your existing `nest-cli.json` and
`tsconfig.json` and requires no configuration changes.

**In scope:**

- `nest build` / `nest start` parity for the `tsc` builder: compile, path-alias
  resolution, asset copying, watch mode, process supervision.
- TypeScript 7 (the native Go compiler) as the compilation engine.

**Out of scope, deliberately:**

- The `swc`, `webpack`, and (NestJS 12) `rspack` builders.
- `nest generate` / `new` / `info` / `add` — nestgo is a build tool, not a scaffolder.

**CLI plugins** (`@nestjs/swagger`, `@nestjs/graphql`) are supported, through a Node
sidecar — see §6. They cannot work as transformers under TypeScript 7 in any tool,
including the official Nest CLI.

## 2. Why nestgo exists

The Nest CLI's `tsc` builder is not "run `tsc`". It calls the TypeScript **JavaScript
compiler API** — `ts.createIncrementalProgram(...)` then `program.emit(..., { before,
after, afterDeclarations })` — and injects custom transformers into the emit pipeline:
one that rewrites `tsconfig` path aliases, plus the CLI plugin hooks.
(`@nestjs/cli@11.0.24`, `lib/compiler/compiler.js`.)

TypeScript 7 is a native Go binary. It has no in-process JavaScript compiler API and no
custom transformer support. The consequences are not a temporary gap:

| Fact | Source |
| --- | --- |
| `@nestjs/cli` hard-depends on `typescript@5.9.3`; v12 alpha moves to `~6.0.2` — never 7 | `@nestjs/cli` `package.json` (11.0.24, 12.0.0-alpha.6) |
| All Nest CLI compile commands fail on TypeScript 7 | [nest-cli#3479](https://github.com/nestjs/nest-cli/issues/3479) (open) |
| The CLI ships a capability gate: *"TypeScript 7.0 ships the `tsc` executable only; the compiler API is expected to return in 7.1. Please install TypeScript 6..."* | `@nestjs/cli` `lib/ui/errors.js` |
| Nest's official position is to wait: *"I'd rather wait for the programmatic API to become stable."* — Kamil Myśliwiec | [nest-cli#3479](https://github.com/nestjs/nest-cli/issues/3479) |
| TypeScript 7.1 (~Q4 2026) restores programmatic **emit** but **not** transformers | [typescript-go#4699](https://github.com/microsoft/typescript-go/pull/4699); 7.1 nightly `.d.ts` has zero `transformer` matches |
| Transformer support is architecturally excluded: *"we can't actually load random code into a Go binary"* | [typescript-go#455](https://github.com/microsoft/typescript-go/discussions/455); [#516](https://github.com/microsoft/typescript-go/issues/516) open, unanswered since 2025-03 |
| NestJS 12 (~Q3 2026) replaces webpack with Rspack for monorepos; `tsc` stays the default; TypeScript-Go support is aspirational with no implementation | [trilon.io/blog/nestjs-12-is-coming](https://trilon.io/blog/nestjs-12-is-coming) |

So: NestJS users have no first-party path to the native compiler, and the blocker is
structural rather than a matter of scheduling. That is the gap nestgo fills.

## 3. Architecture decision: subprocess-first

**nestgo locates the user's official TypeScript 7 native binary and drives it as a
subprocess.** It does not embed, link against, or reimplement the compiler.

```
nestgo (single Go binary)
│
├─ toolchain    locate the native tsc binary
├─ config       nest-cli.json + `tsc --showConfig` for resolved tsconfig
├─ preflight    TypeScript 7 config migration checks
├─ compile      spawn tsc directly; parse diagnostics + emitted-file list
├─ watch        wrap `tsc --watch`; drive rebuild/restart from its output
├─ rewrite      post-emit path-alias resolution over changed files only
├─ assets       glob copy + watch (nest-cli parity)
└─ runner       node process supervision, restart, tree-kill
```

### Why not embed the compiler

nestgo used to statically embed `microsoft/typescript-go`, declaring fake modules named
`github.com/microsoft/typescript-go/shim/*` and reaching into the upstream `internal/`
packages with `//go:linkname`. This is a real technique — typescript-eslint's `tsgolint`
uses the identical pattern — but it was the wrong trade for nestgo:

- **`go:linkname` has no type checking, and its failure mode is silent.** A linkname
  declaration whose signature no longer matches upstream compiles cleanly, passes
  `go vet`, runs, and returns garbage. (Measured on Go 1.26.2.) It broke twice on version
  bumps here — both times as a runtime segfault while the full unit-test suite passed.
- **No tooling can fix this from the consuming side.** Within our module the local
  declaration *is* the signature; there is no second source of truth to check against. A
  survey of every third-party linkname consumer in the Go ecosystem found zero projects
  with signature or layout assertions.
- **The Go team intends to restrict it.** Third-party pull-mode linkname is currently
  unrestricted, but [golang/go#67401](https://github.com/golang/go/issues/67401) states:
  *"We'd like to change that eventually too, insisting on Handshakes everywhere."*
- **We don't need what it buys.** `tsgolint` needs in-process, per-node type queries —
  there is no CLI for that. nestgo needs compile, emit, and a list of what changed. All
  three are on the stable CLI surface.
- **The CLI is faster than what we had.** The embedded engine had no incremental
  compilation at all. `tsc --incremental` re-emits only affected files, and in watch mode
  the compiler keeps its program in memory between edits.

The migration bore this out: both binaries went from ~19 MB to ~3 MB, and neither Go
module now depends on anything beyond the standard library plus cobra, fsnotify and
doublestar.

Embedding remains the only route to *native* transformer support (`samchon/ttsc` obtains
tsgo's builtin transformer chain via `//go:linkname internal/compiler.getScriptTransformers`).
We do not take that route because Nest's own SWC builder demonstrates that plugin parity
does not require transformers — see §6.

### Why the CLI surface is sufficient

Except where noted, the table below comes from experiments against
`typescript@7.0.2` conducted while writing this document, **not** from this
repository's test suite. Phase 2 must reproduce the load-bearing ones in-repo
before the embedded engine is deleted.

| Capability | Finding |
| --- | --- |
| **Decorator emit** | **Verified in-repo** (`scripts/verify-decorator-emit.sh`, run in CI). Every `design:paramtypes` / `design:type` / `design:returntype` value and every `__param` position is identical to `tsc@5.9.3` — the version `@nestjs/cli` 11.x depends on. NestJS DI is safe. Note the output is **not** byte-identical, contrary to the external benchmark: tsgo preserves per-parameter comments in a multi-line parameter list where tsc collapses them. That difference is cosmetic. |
| **Diagnostics** | `file(line,col): error TSxxxx: msg` on stdout, unchanged. Exit codes: 0 clean, 2 errors-with-emit, 1 errors-without-emit. |
| **Watch** | `--watch` emits the classic sentinels (`File change detected...`, `Found N errors. Watching for file changes.`). `--preserveWatchOutput` suppresses screen clears. SIGTERM exits cleanly. |
| **Incremental** | `--incremental` / `.tsbuildinfo` fully supported, including in watch mode. |
| **Changed-file list** | `--listEmittedFiles` prints `TSFILE: <abs path>` per emitted file, per watch cycle. |
| **Resolved config** | `--showConfig` returns fully-resolved JSON with `extends` flattened and `include` expanded. |
| **Module resolution** | `--traceResolution` gives the compiler's own `(specifier, importer) → source file` answers. |

The last three matter most: they let nestgo delete code rather than write it. `--showConfig`
replaces our hand-rolled JSONC parser and `extends` resolver. `--listEmittedFiles` turns
alias rewriting from "re-scan the whole output tree" into "rewrite the files that changed".
`--traceResolution` removes the need to reimplement `paths` matching, which is the source
of essentially every `tsc-alias` bug.

### Rejected alternatives

| Alternative | Why not |
| --- | --- |
| **Embedded compiler via `go:linkname`** (what nestgo used to do) | Silent-failure ABI, unfixable from our side, announced deprecation intent, and unnecessary for our use case. See above. |
| **`tsc --api` JSON-RPC mode** | Undocumented; self-describes as *"The protocol is unversioned; both sides must be built from the same tree"*; **has no emit method**; being redesigned for 7.1. Same ABI coupling as linkname, different wire format. |
| **`typescript/unstable/*` JS API** | Every entry point is namespaced `unstable/`; 7.0 has no emit at all. Would also force a Node process into every build. Revisit if 7.1's API stabilizes. |
| **Shell out to `tsc-alias`** | Measured defects: it rewrites alias-looking text **inside string literals and comments** (silent data corruption), and never updates `.js.map` mappings. It also re-scans every output file on every run (118 ms vs. a 48 ms warm rebuild). |
| **`package.json` "imports" instead of rewriting** | Node requires the `#` prefix — a non-`#` key is silently ignored, producing exactly the `MODULE_NOT_FOUND` we set out to prevent. Multi-element `paths` arrays and bare directory targets are unrepresentable. Possible opt-in for greenfield projects later; not the default. |
| **Wait for first-party Nest support** | Nest's stated plan is to wait for TypeScript 7.1's API — which will not restore transformers, so the CLI's architecture still won't port. No timeline exists. |

### Revisit triggers

This decision should be re-examined if any of the following happens:

1. TypeScript ships transformer support in the programmatic API (currently excluded by design).
2. `microsoft/typescript-go` publishes a public, non-`internal` Go API
   ([discussion #481](https://github.com/microsoft/typescript-go/discussions/481) — "thinking
   about it", no commitment).
3. The Nest CLI ships first-party tsgo support — at which point nestgo either repositions
   or upstreams.

## 4. Components

### toolchain — locating the compiler

The native binary ships as a per-platform package (`@typescript/typescript-<os>-<arch>`)
that `typescript@7`'s launcher resolves at runtime. Resolution order:

1. `NESTGO_TSC_BIN` environment override.
2. The user's `typescript` package: resolve `typescript/package.json`, then import its
   `lib/getExePath.js` **by absolute path** and call it. This is the same function the
   official `tsc` launcher uses.
3. Not implemented: a nestgo-managed download of the platform package (the model Deno
   uses), which would let nestgo work without `typescript` in `node_modules`.

Two rules learned the hard way, both measured:

- **Never construct `node_modules/@typescript/...` paths by hand.** Under pnpm the
  platform package is not reachable from the project root; under Yarn PnP there is no
  `node_modules` directory at all, and the unplugged path contains a content hash.
- **Spawn the native binary directly, not through the Node launcher.** The launcher costs
  ~105 ms per invocation — comparable to an entire incremental build.

Under Yarn PnP the locator probe must run as `yarn node` (detected by `.pnp.cjs` at the
project root). Yarn Berry below 4.17 cannot install `typescript@7` at all; that is a
documented floor, not a bug we can work around.

### compile — spawning and parsing

Base flags: `--pretty false --preserveWatchOutput --incremental --listEmittedFiles`.

Diagnostics are parsed with the classic positional regex, folding indented continuation
lines into the preceding message (TypeScript 7 emits multi-line elaborations). Exit codes
map to: 0 success, 2 compiled-with-errors, 1 errors-and-no-output.

**Leave `Cmd.Env` alone.** Go's `os.Getwd` trusts `$PWD` whenever it stats to the same
inode as the real working directory, so whether the compiler reports paths under the
directory we asked for or under its resolved target comes down to what `PWD` says — and
it silently changes every path in diagnostics and `--listEmittedFiles`. On macOS
(`/tmp` → `/private/tmp`) and in containers with symlinked mounts this is live, and we
are a Go program spawning a Go program, so both sides behave this way.

`os/exec` already solves it: with `Cmd.Env` nil it sets `PWD` to `Cmd.Dir`. Setting
`Cmd.Env` explicitly is what breaks it — verified, and guarded by a test that fails when
`Env` is set without carrying `PWD`.

### watch

nestgo wraps `tsc --watch` and drives its own lifecycle from the compiler's output: parse
the cycle sentinels, collect `TSFILE:` lines, rewrite only those files, then restart the
child process. No tool currently wraps `tsgo --watch`; measurements confirm it works,
is genuinely incremental, and produces parseable per-cycle output.

nestgo skips restarting the application for cycles that emit nothing. The compiler
re-checks whenever it notices a change, including ones needing no new output — a touch
with identical content, for instance — and restarting on those would bounce the app for
no reason.

A failed rebuild leaves the running process alone: it is still serving the last good
build, which beats taking the app down over a typo.

Two gaps nestgo has to cover: stale outputs are never cleaned when a source file is
deleted, and incremental state can outlive the output it describes (see below).

**Incremental state must be invalidated when the output goes.** `.tsbuildinfo` is trusted
over the filesystem, so after a manual `rm -rf dist` the compiler concludes every file is
current and emits nothing, exiting 0 having produced no build. That is tsc's own
behaviour and `nest build` inherits it, but a build command that silently does nothing is
the worst possible failure, so nestgo clears the state when the output directory has
gone. This also means deriving the default buildinfo path — it sits beside the tsconfig,
named after it — since `deleteOutDir` otherwise only knows about an explicitly configured
one.

### rewrite — path aliases

TypeScript does not rewrite `paths` aliases on emit; `paths` is descriptive, not
prescriptive. The Nest CLI solves this with a custom transformer, which is exactly the
mechanism TypeScript 7 removes. nestgo therefore rewrites emitted JavaScript after the
fact, driven by the `TSFILE:` list so only changed files are touched.

Requirements, several of which are bugs in existing tools:

- Rewrite only in genuine import/export/require positions — never inside string literals,
  comments, or template expressions. This needs a scanner, not a regex.
- Rewrite dynamic `import()` and `require()` calls. The Nest CLI's own transformer handles
  only `ImportDeclaration`/`ExportDeclaration` and misses these.
- Installed packages win over alias matches: if a specifier resolves as a real module,
  leave it alone (Nest CLI parity, [nest-cli#838](https://github.com/nestjs/nest-cli/issues/838)).
- Append `.js` / `/index.js` where the output needs it — mandatory for ESM, which NestJS 12
  moves toward.
- Update `.js.map` mappings when rewriting shifts columns. `tsc-alias` does not do this;
  its source maps are stale on every import line.

**Deliberate divergence:** we append `.js` to *every* extensionless relative import,
including ones that were never aliases, so emitted CommonJS reads
`require("./service.js")` where `nest build` emits `require("./service")`. Both
resolve identically under CommonJS, and the explicit form is what ESM will require.
Revisit if a project needs byte-for-byte parity with `nest build` output.

### config and preflight

`nest-cli.json` is parsed by nestgo (defaults, `projects[app]` precedence, builder
validation). The **resolved tsconfig comes from `tsc --showConfig`** rather than a
hand-rolled parser, eliminating an entire class of divergence between what nestgo believes
and what the compiler does.

TypeScript 7 removed options that existing NestJS projects rely on. The stock
`@nestjs/schematics@11.1.0` template **does not compile** untouched — though it is closer
than older projects, since it already uses `module`/`moduleResolution: "nodenext"`, and
`baseUrl` is the only removed option it sets. Projects generated before that switch carry
more of the list:

| Removed / changed | Error | Migration |
| --- | --- | --- |
| `baseUrl` | TS5102 | drop it; make `paths` targets relative |
| implicit `rootDir` | TS5011 | set `rootDir` explicitly |
| non-relative `paths` values | TS5090 | prefix with `./` |
| `moduleResolution: node`/`node10`/`classic` | TS5108 | `bundler` or `nodenext` |
| `target: ES5`, `outFile`, `module: AMD/System/UMD` | TS5102 | no downlevel-to-ES5 support exists |
| `esModuleInterop: false`, `allowSyntheticDefaultImports: false` | TS5102 | remove the override |

nestgo runs these as a preflight check and reports them with actionable messages (or
auto-migrates on request). This is unavoidable work for any TypeScript 7 adoption path and
is a feature, not an obstacle.

### runner and assets

Both exist today and are kept. Fidelity notes recorded from the Nest CLI source:

- Entry file resolution is a two-guess probe (`dist/<sourceRoot>/<entry>.js`, then
  `dist/<entry>.js`), not a `rootDir` computation. nestgo adds a `rootDir` candidate and
  warns when several match.
- Spawn shape: `node --enable-source-maps [--env-file=...] [--inspect[=host:port]] <entry> [args]`.
  Repeated `--env-file` flags are passed as separate arguments (the v11 CLI joins them into
  one, which breaks under `--no-shell`; v12 fixes this and nestgo follows v12).
- Termination kills the process group children-first with SIGTERM, escalating to SIGKILL;
  Windows uses `taskkill /T /F`. Restart happens only after the old process has exited.
- Assets: globs are resolved under `sourceRoot`, per-item `outDir` overrides the tsconfig
  `outDir`, and removals delete the corresponding output file.

**Deliberate divergence:** the Nest CLI hands chokidar a pre-resolved *file list*, so
assets created after startup are never picked up. nestgo watches directories, so new files
are handled. This is a bug fix, and it is intentional.

## 5. Distribution

Two npm packages, each a thin Node launcher plus per-platform binary packages
(`@nestgo/core-<os>-<arch>`, `@ttsgo/core-<os>-<arch>`), following the esbuild model.

- **`nestgo`** — the NestJS build/start replacement.
- **`ttsgo`** — the same engine as a standalone `tsc` wrapper with correct path-alias
  rewriting. This is a genuinely better `tsc-alias`: scanner-based rather than regex,
  incremental via `--listEmittedFiles`, and it keeps source maps correct.

Under the target architecture these binaries no longer embed the compiler, so they shrink
from ~19 MB to a small fraction of that.

## 6. CLI plugins

`@nestjs/swagger` and `@nestjs/graphql` ship their CLI plugins as TypeScript custom
transformers. TypeScript 7 has no transformer API and will not gain one — the compiler is
a Go binary and cannot load JavaScript into its process — so those plugins cannot run as
transformers under nestgo, under the Nest CLI, or under anything else on TypeScript 7.

The plugins also expose a `ReadonlyVisitor`, which walks the AST without transforming it
and serialises what it finds into a `metadata.ts` the application loads at runtime
(`SwaggerModule.loadPluginMetadata`). NestJS documents that path for its own SWC builder,
because SWC cannot run TypeScript transformers either. nestgo takes the same route.

A `ReadonlyVisitor` is handed a live `ts.Program` and returns `ts.Node` values, so it
cannot be driven from Go. nestgo runs a short-lived **Node sidecar** before compiling,
and the `metadata.ts` it writes is compiled as an ordinary source file.

Three details make this work, none of them obvious:

- **The visitors need the classic compiler API**, which TypeScript 7 does not have. A
  project using CLI plugins therefore needs `@typescript/typescript6` installed alongside
  TypeScript 7. It is used only for metadata; the code still compiles with 7.
- **The plugins `require("typescript")` themselves** and call the classic API on whatever
  comes back — which in a nestgo project is TypeScript 7, where the visitor dies on its
  first `ts.visitNode`. The sidecar redirects that specifier to the TypeScript 6 module
  for its own process.
- **The generated file uses extensionless dynamic imports**, which `node16`/`nodenext`
  reject — and those are the only resolutions TypeScript 7 still has. The sidecar adds
  the extensions, without which no plugin-using project could compile at all.

Configure plugins as usual in `nest-cli.json`:

```json
{ "compilerOptions": { "plugins": ["@nestjs/swagger"] } }
```

`tests/nest-app` covers this end to end: a DTO with no `@ApiProperty` decorators whose
swagger schema is nonetheless complete, proving the metadata came from the plugin.

## 7. Roadmap

**Done.** The compiler locator, subprocess spawn/parse layer, `--showConfig` config
resolution, TypeScript 7 preflight, rewriter hardening (source maps, node_modules
precedence, `TSFILE`-driven incremental passes), compiler-driven watch mode, removal of
the embedded engine and shim tree, the CLI plugin sidecar, and a real NestJS application
fixture in CI.

**Next — `nest build --webpack` and monorepo mode.** Still out of scope, and the largest
remaining gap against the Nest CLI for projects that use them.

**Ongoing — NestJS 12 readiness.** v12 moves the ecosystem toward ESM, which makes
extension-correct rewriting mandatory rather than optional.

**Ongoing — the TypeScript canary.** Because nestgo drives whatever compiler the user
installed, an upstream change reaches users without anything here changing. A weekly job
(`.github/workflows/canary.yml`) runs the end-to-end checks against `typescript@next`, so
a breaking change in 7.1 surfaces here first. It is allowed to fail: red means upstream
moved.

**Known limitation.** Emitted CommonJS uses `require("./service.js")` where `nest build`
emits `require("./service")`. Both resolve identically; the explicit form is what ESM
requires.

## 8. Verification

The load-bearing test is end-to-end: build both binaries, compile a real project, and
**run the output**. Unit tests cover the rewriter, the diagnostic parser, config
resolution and process lifecycle; they cannot cover the compiler boundary. When the
compiler was embedded, the entire unit suite passed twice while the binaries segfaulted
on any real input.

Three checks run in CI on every push:

| Check | What it would catch |
| --- | --- |
| `tests/dummy-ts` compiled by both binaries, then executed | a broken pipeline, or alias rewriting that produces unresolvable imports |
| `scripts/verify-decorator-emit.sh` | decorator metadata drifting from `tsc`, which would break NestJS dependency injection at runtime rather than at build time |
| `scripts/verify-nest-app.sh` | a real NestJS app failing to build, dependency injection not resolving, or plugin metadata not being generated |
| `go test ./...` in both modules | everything below the compiler boundary |

Integration tests install a real `typescript@7` and run the actual compiler; they skip
rather than fail when offline.
