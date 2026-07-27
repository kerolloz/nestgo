# ttsgo & nestgo

**nestgo** is a drop-in replacement for `nest build` and `nest start`, powered by **ttsgo** — a TypeScript compiler built on top of TypeScript 7.0's native Go port.

No config changes required. Same `nest-cli.json`. Same `tsconfig.json`. Just faster.

Why this exists, and where it's going: [ARCHITECTURE.md](./ARCHITECTURE.md). The short version is that the Nest CLI drives TypeScript through its JavaScript compiler API and injects custom transformers at emit; TypeScript 7 is a Go binary with neither, so the Nest CLI does not work on TypeScript 7 today and its design does not port there.

---

## Benchmarks

Benchmarks were run on dummy projects. Real-world speedups depend on project size, but typically land between **5x and 30x**.

| Project      | Tool           | Time    | Speedup     |
|--------------|----------------|---------|-------------|
| TypeScript   | `tsc`          | 0.643s  | baseline    |
| TypeScript   | `ttsgo`        | 0.130s  | **~5x**     |
| NestJS app   | `nest build`   | 1.856s  | baseline    |
| NestJS app   | `nestgo build` | 0.060s  | **~30x**    |

---

## nestgo

A CLI replacement for the NestJS build toolchain. Reads your existing `nest-cli.json` and compiles with `ttsgo` instead of `tsc`.

### Installation

Install as a dev dependency and update your npm scripts:

```bash
npm install --save-dev nestgo
```

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
| `compilerOptions.builder: "swc"`  | ❌        |
| `compilerOptions.builder: "webpack"` | ❌     |
| plugins, generate, add, new       | ❌        |

Only the `tsc` builder is supported. If your project uses `swc` or `webpack`, nestgo will exit with a clear error rather than silently producing wrong output.

> **CLI plugins** (`@nestjs/swagger`, `@nestjs/graphql`) are TypeScript custom transformers. TypeScript 7 has no transformer API, so they cannot run under nestgo — nor under the Nest CLI itself on TypeScript 7. This is a property of the compiler, not a missing feature. See [ARCHITECTURE.md](./ARCHITECTURE.md#6-cli-plugins-the-honest-limitation) for the supported path forward.

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
| `NESTGO_DEBOUNCE_MS`| File watcher debounce in milliseconds (default: 500)  |
| `TTSGO_WORKERS`     | Number of I/O workers for parallel file emit          |
| `NESTGO_BINARY`     | Override the `nestgo` binary the npm launcher runs    |
| `TTSGO_BINARY`      | Override the `ttsgo` binary the npm launcher runs     |

---

## ttsgo

A standalone TypeScript compiler for non-NestJS projects. A faster drop-in for `tsc`.

### Installation

```bash
npm install --save-dev ttsgo
```

### Usage

```bash
ttsgo -p tsconfig.json           # compile project
ttsgo -p tsconfig.json --noEmit  # type-check only, no output
ttsgo -p tsconfig.json --outDir dist
ttsgo --version                  # print version
```

`ttsgo` reads your `tsconfig.json`, runs type-checking and emit using TypeScript 7.0's native Go compiler, and rewrites any `paths` aliases in the emitted files in one pass — no separate post-processing step.

---

## How it works

```
nestgo build
     │
     ├── reads nest-cli.json + tsconfig.json
     │
     └── calls ttsgo engine (in-process, no child process)
              │
              ├── TypeScript 7.0 Go compiler (microsoft/typescript-go)
              │     type-check + emit
              │
              ├── concurrent I/O worker pool
              │     parallel WriteFile across CPU cores
              │
              └── paths rewriter
                    rewrites @aliases → relative paths
                    in-memory, during emit
```

The compiler runs in the same process as nestgo — there's no `exec.Command` spawned for compilation. The only child process is the Node.js app itself (`nestgo start`).

Path alias rewriting happens as each file buffer is handed off to disk, so there's no second pass over the output directory.

> This in-process design is being replaced: nestgo will locate and drive the official TypeScript 7 binary as a subprocess instead of embedding the compiler. The reasoning, the evidence, and the migration plan are in [ARCHITECTURE.md](./ARCHITECTURE.md).

---

## Local development

Requires [just](https://github.com/casey/just), [Go 1.26+](https://github.com/kerolloz/go-installer) and Node.js 20+.

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

That last step is the check that matters, and CI runs it on every push. The
compiler is reached through `go:linkname` into typescript-go's internals, which
Go does not type-check — an upstream signature change compiles cleanly, passes
every unit test, and then fails at runtime. Only running the output catches it.
See [ARCHITECTURE.md](./ARCHITECTURE.md) for why that boundary exists and how
it is being removed.

### Tests

```bash
cd packages/ttsgo && go test ./pkg/...
cd packages/nestgo && go test ./...
```

---

## License

MIT
