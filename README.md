# ttsgo & nestgo

**nestgo** is a drop-in replacement for `nest build` and `nest start` that compiles with **TypeScript 7**, the native Go compiler.

Same `nest-cli.json`. Same `tsconfig.json`. It runs the TypeScript your project already has installed, so you type-check against the exact compiler version you pinned.

Why this exists, and where it's going: [ARCHITECTURE.md](./ARCHITECTURE.md). The short version is that the Nest CLI drives TypeScript through its JavaScript compiler API and injects custom transformers at emit; TypeScript 7 is a Go binary with neither, so the Nest CLI does not work on TypeScript 7 today and its design does not port there.

---

## Benchmarks

Measured on `tests/decorators` (a small decorator-heavy fixture, macOS arm64), cold build,
best of three:

| Tool | Time |
|------|------|
| `tsc` 5.9.3 — the version `@nestjs/cli` pins | 0.34s |
| `ttsgo` | 0.05s |

That is ~7x on a fixture this small, where Node's startup dominates `tsc`'s number. The
gap widens with project size, and watch rebuilds are cheaper again because the compiler
stays resident and re-emits only what changed. Measure your own project before quoting a
number — the speedup depends heavily on how much of your build is type-checking.

## nestgo

A CLI replacement for the NestJS build toolchain. Reads your existing `nest-cli.json` and compiles with `ttsgo` instead of `tsc`.

### Installation

nestgo needs TypeScript 7 in your project — it drives your compiler rather than bundling one:

```bash
npm install --save-dev nestgo typescript@^7
```

Then update your npm scripts:

```json
// package.json
{
  "scripts": {
    "build":     "nestgo build",
    "start":     "nestgo start",
    "start:dev": "nestgo start --watch"
  }
}
```

### Usage

```bash
nestgo build                  # replaces: nest build
nestgo build --watch          # watch mode
nestgo start                  # replaces: nest start
nestgo start --watch          # hot-reload dev server
nestgo start --debug          # attach Node.js inspector
nestgo start -- --port 3001   # pass extra args to Node
```

All commands auto-detect `nest-cli.json` and `tsconfig.json` in the current directory. No flags required for standard project layouts.

### Supported `nest-cli.json` fields

| Field                             | Supported |
|-----------------------------------|-----------|
| `sourceRoot`                      | ✅        |
| `entryFile`                       | ✅        |
| `exec`                            | ✅        |
| `compilerOptions.tsConfigPath`    | ✅        |
| `compilerOptions.deleteOutDir`    | ✅        |
| `compilerOptions.assets`          | ✅        |
| `compilerOptions.watchAssets`     | ✅        |
| `compilerOptions.plugins`         | ✅        |
| `compilerOptions.builder: "swc"`  | ❌        |
| `compilerOptions.builder: "webpack"` | ❌     |
| generate, add, new                | ❌        |

Only the `tsc` builder is supported. If your project uses `swc` or `webpack`, nestgo will exit with a clear error rather than silently producing wrong output.

### CLI plugins

`@nestjs/swagger` and `@nestjs/graphql` CLI plugins work. Configure them as usual:

```json
// nest-cli.json
{ "compilerOptions": { "plugins": ["@nestjs/swagger"] } }
```

These plugins are TypeScript custom transformers, and TypeScript 7 has no transformer API — they cannot run as transformers under nestgo, the Nest CLI, or anything else. nestgo instead runs their `ReadonlyVisitor` in a short-lived Node sidecar before compiling, generating the `metadata.ts` your app loads at runtime. This is the same approach NestJS documents for its SWC builder.

That sidecar needs the classic compiler API, so install TypeScript 6 alongside 7:

```bash
npm install --save-dev @typescript/typescript6
```

It is used only to generate metadata; your code still compiles with TypeScript 7. Then load the metadata as usual:

```ts
import metadata from './metadata.js';
await SwaggerModule.loadPluginMetadata(metadata);
```

### TypeScript 7 configuration requirements

nestgo compiles with TypeScript 7, which removed several options existing NestJS projects rely on. A freshly generated app only needs `baseUrl` removed; projects generated before NestJS moved to `nodenext` usually need more. If your `tsconfig.json` uses any of these, the compiler will reject it:

| Removed | Replace with |
|---------|--------------|
| `baseUrl` | `"paths": { "*": ["./*"] }` |
| implicit `rootDir` | set `rootDir` explicitly |
| non-relative `paths` values | prefix each with `./` |
| `moduleResolution: "node"` / `"node10"` / `"classic"` | `"bundler"` or `"nodenext"` |
| `target: "ES5"`, `outFile`, `module: "AMD"`/`"System"`/`"UMD"` | no ES5 downlevel support exists |
| `esModuleInterop: false`, `allowSyntheticDefaultImports: false` | remove the override |

### Path aliases

If your `tsconfig.json` defines `paths`, nestgo rewrites them in the emitted output automatically — no `tsc-alias` or post-processing step needed.

```json
// tsconfig.json
{
  "compilerOptions": {
    "paths": {
      "@modules/*": ["./src/modules/*"],
      "@common/*":  ["./src/common/*"]
    }
  }
}
```

This works for both `import ... from` and `require(...)` syntax, including `.d.ts` declaration files.

### Environment variables

| Variable            | Description                                           |
|---------------------|-------------------------------------------------------|
| `NESTGO_TSC_BIN`    | Path to the TypeScript compiler to run, bypassing discovery |
| `TTSGO_WORKERS`     | Number of workers used to rewrite emitted files       |
| `NESTGO_BINARY`     | Override the `nestgo` binary the npm launcher runs    |
| `TTSGO_BINARY`      | Override the `ttsgo` binary the npm launcher runs     |

---

## ttsgo

A standalone TypeScript compiler for non-NestJS projects. A faster drop-in for `tsc`.

### Installation

```bash
npm install --save-dev ttsgo typescript@^7
```

### Usage

```bash
ttsgo -p tsconfig.json           # compile project
ttsgo -p tsconfig.json --noEmit  # type-check only, no output
ttsgo -p tsconfig.json --outDir dist
ttsgo --version                  # print version
```

`ttsgo` runs your project's TypeScript 7 compiler and then rewrites `paths` aliases in the
emitted files, so the output runs under Node without `tsconfig-paths` at runtime. Unlike
`tsc-alias`, it only rewrites genuine import positions — never alias-looking text inside
string literals — and it updates the companion source maps so debuggers and stack traces
stay accurate on import lines.

---

## How it works

```
nestgo build
     │
     ├── reads nest-cli.json
     │
     ├── locates your TypeScript 7 compiler
     │     node_modules/@typescript/typescript-<os>-<arch>
     │
     ├── asks it for the resolved tsconfig  (tsc --showConfig)
     │     extends chains flattened, include globs expanded
     │
     ├── runs it                            (tsc --listEmittedFiles)
     │     type-check + emit
     │
     ├── rewrites path aliases
     │     @aliases → relative paths, in the files that changed
     │     source maps adjusted to match
     │
     └── copies assets
```

`nestgo start --watch` runs the compiler in watch mode and restarts your app after each
clean build. The compiler does the file watching: it knows your project's real file set
and recompiles incrementally, so a rebuild touches only what changed. A failing rebuild
leaves the running app alone rather than taking it down over a typo.

For the reasoning behind this design, and what it replaced, see
[ARCHITECTURE.md](./ARCHITECTURE.md).

## Local development

Requires [just](https://github.com/casey/just), [Go 1.26+](https://github.com/kerolloz/go-installer) and Node.js 20+.

The test fixtures install their own TypeScript, which is how a real project works:

```bash
npm install --prefix tests/dummy-ts
npm install --prefix tests/decorators
```

```bash
git clone https://github.com/kerolloz/nestgo.git
cd nestgo
just build          # builds both binaries to ./bin/
just clean          # removes built binaries
```

Or manually:

```bash
# build ttsgo
cd packages/ttsgo
go build -o ../../bin/ttsgo ./cmd/ttsgo

# build nestgo
cd packages/nestgo
go build -o ../../bin/nestgo .
```

Run against the included test project:

```bash
cd tests/dummy-ts
../../bin/ttsgo -p tsconfig.json
node dist/main.js       # should print: App running. 2 + 3 = 5
```

That last step is the check that matters, and CI runs it on every push. Unit tests cannot
cover the compiler boundary: nestgo drives a separate process, so the only way to know the
pipeline works is to compile a real project and run what comes out.

### Tests

```bash
just test                 # both Go modules
just verify-decorators    # decorator metadata still matches tsc
```

Integration tests install a real TypeScript and run the actual compiler; they skip rather
than fail when offline.

---

## License

MIT
