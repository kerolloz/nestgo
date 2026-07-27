# nestgo Architecture

> **Status:** This document records the target architecture and the decision behind it.
> The code currently implements the *previous* architecture (an embedded compiler); the
> migration is Phase 2 below. Where current and target differ, this document says so
> explicitly.

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
- **CLI plugins** (`@nestjs/swagger`, `@nestjs/graphql`) — see §6. These cannot work
  under TypeScript 7 in any tool, including the official Nest CLI. A compatible path
  exists and is planned (Phase 3); it is not a small feature.

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

### Why not embed the compiler (the current implementation)

Today's code statically embeds `microsoft/typescript-go` by declaring fake modules named
`github.com/microsoft/typescript-go/shim/*` and reaching into the upstream `internal/`
packages with `//go:linkname`. This is a real technique — typescript-eslint's `tsgolint`
uses the identical pattern — but it is the wrong trade for nestgo:

- **`go:linkname` has no type checking, and its failure mode is silent.** A linkname
  declaration whose signature no longer matches upstream compiles cleanly, passes
  `go vet`, runs, and returns garbage. (Measured on Go 1.26.2.) In this repository it has
  already broken twice on version bumps — both times as a runtime segfault while the full
  unit-test suite passed.
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
  compilation. `tsc --incremental` in watch mode re-emits only affected files: measured
  at 96 ms for a one-file edit in an 801-file project, versus 423 ms cold.

Embedding remains the only route to *native* transformer support (`samchon/ttsc` obtains
tsgo's builtin transformer chain via `//go:linkname internal/compiler.getScriptTransformers`).
We do not take that route because Nest's own SWC builder demonstrates that plugin parity
does not require transformers — see §6.

### Why the CLI surface is sufficient

Measured against `typescript@7.0.2`:

| Capability | Finding |
| --- | --- |
| **Decorator emit** | `emitDecoratorMetadata` output is **byte-identical** to `tsc@5.9.3` (`__decorate`/`__param`/`__metadata`). Full `dist/` diff: identical `.js` and `.d.ts`. NestJS DI is safe. |
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
| **Embedded compiler via `go:linkname`** (current code) | Silent-failure ABI, unfixable from our side, announced deprecation intent, and unnecessary for our use case. See above. |
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
3. Optionally, a nestgo-managed download of the platform package (the model Deno uses),
   so nestgo works without `typescript` in `node_modules`.

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

**`PWD` must match the working directory we `chdir` to.** Go's `os.Getwd` trusts `$PWD`
when it stats to the same inode, so a stale or symlinked `PWD` silently changes every path
in diagnostics and in `--listEmittedFiles` output. On macOS (`/tmp` → `/private/tmp`) and
in containers this is a live hazard, and nestgo is a Go program spawning a Go program —
doubly exposed.

### watch

nestgo wraps `tsc --watch` and drives its own lifecycle from the compiler's output: parse
the cycle sentinels, collect `TSFILE:` lines, rewrite only those files, then restart the
child process. No tool currently wraps `tsgo --watch`; measurements confirm it works,
is genuinely incremental, and produces parseable per-cycle output.

Two gaps in `tsc --watch` that nestgo must cover: stale outputs are never cleaned when a
source file is deleted, and a `.tsbuildinfo` written by a different compiler version
forces a silent full rebuild (so it must be invalidated when the toolchain changes).

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

### config and preflight

`nest-cli.json` is parsed by nestgo (defaults, `projects[app]` precedence, builder
validation). The **resolved tsconfig comes from `tsc --showConfig`** rather than a
hand-rolled parser, eliminating an entire class of divergence between what nestgo believes
and what the compiler does.

TypeScript 7 removed options that stock NestJS templates still use. Verified against the
unmodified `@nestjs/schematics@11.1.0` template, which **does not compile**:

| Removed / changed | Error | Migration |
| --- | --- | --- |
| `baseUrl` | TS5102 | `"paths": { "*": ["./*"] }` |
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

## 6. CLI plugins: the honest limitation

`@nestjs/swagger` and `@nestjs/graphql` CLI plugins are TypeScript custom transformers.
They cannot run under TypeScript 7 — not in nestgo, not in the Nest CLI, not in any tool.
This is permanent for the TypeScript 7 line.

There is a supported path, and Nest already blessed it for its own SWC builder: plugins
also expose a `ReadonlyVisitor` which walks the AST read-only and serializes results to a
`metadata.ts` file that the application loads at runtime
(`SwaggerModule.loadPluginMetadata`). The Nest CLI runs this in a **forked Node process**
holding a real TypeScript `Program`.

nestgo can do the same (Phase 3): fork a small Node sidecar with `@typescript/typescript6`
and the Nest CLI's exported `PluginMetadataGenerator`, then compile the generated
`metadata.ts` alongside the rest of the sources. Everything else — transpile, watch,
assets, process supervision — stays in Go.

Until that ships, projects depending on CLI plugins should stay on the Nest CLI with
TypeScript 6.

## 7. Roadmap

**Phase 1 — this document, plus interim guards.** Until Phase 2 lands, the embedded
compiler stays in place with: an end-to-end CI job that compiles and *runs* a real project
(the only check that catches linkname drift — unit tests do not), a rule that the
typescript-go dependency is pinned to release tags only, and a warning comment on every
`go:linkname` declaration.

**Phase 2 — migration to subprocess.** Toolchain locator; spawn/parse layer; rewriter
hardening (source maps, node_modules precedence, `TSFILE`-driven incremental passes);
`--showConfig` replacing the tsconfig parser; preflight migrations; deletion of the shim
tree and the embedded engine; CI fixtures for both a plain project and the stock NestJS
template.

**Phase 3 — plugin metadata sidecar.** As described in §6.

**Ongoing — NestJS 12 readiness.** v12 moves the ecosystem toward ESM, which makes
extension-correct rewriting mandatory rather than optional. A scheduled CI job tracks
`typescript@next` so TypeScript 7.1 changes surface before they reach users.

## 8. Verification

The load-bearing test is end-to-end: build both binaries, compile a real project, and
**run the output**. This session demonstrated why — the entire unit-test suite passed
while both binaries segfaulted on any real input. Unit tests cover the rewriter, config
resolution, and process lifecycle; they do not and cannot cover the compiler boundary.
