#!/usr/bin/env node
"use strict";

// Generates NestJS CLI plugin metadata.
//
// @nestjs/swagger and @nestjs/graphql ship their CLI plugins as TypeScript
// custom transformers, which TypeScript 7 has no API for and never will — the
// compiler is a Go binary and cannot load JavaScript into its process. The
// plugins also expose a ReadonlyVisitor that walks the AST without transforming
// it and serializes what it finds to a metadata.ts the application loads at
// runtime. That is the path NestJS itself documents for its SWC builder, and it
// is the only one that survives a compiler which cannot run transformers.
//
// A ReadonlyVisitor takes a live ts.Program and returns ts.Node values, so it
// cannot run from Go. This script is the sidecar: nestgo runs it with Node
// before compiling, and the metadata.ts it writes is compiled as an ordinary
// source file.
//
// Input is a JSON config on argv[2]:
//   { cwd, tsconfigPath, outputDir, filename, plugins: [{name, options}] }
// Output on stdout is JSON: { written, error }

const fs = require("fs");
const path = require("path");
const Module = require("module");

const SERIALIZED_METADATA_FILENAME = "metadata.ts";
const TYPE_IMPORT_VARIABLE_NAME = "t";
const PLUGIN_ENTRY_FILENAME = "plugin";

function fail(message) {
  process.stdout.write(JSON.stringify({ error: message }));
  process.exit(1);
}

// The visitors need the classic TypeScript compiler API. TypeScript 7 does not
// have one, so a project using CLI plugins needs a TypeScript 6 alongside it —
// Microsoft publishes @typescript/typescript6 for exactly this.
function loadTypeScript(cwd) {
  const paths = [cwd, path.join(cwd, "node_modules"), ...module.paths];
  const candidates = ["@typescript/typescript6", "typescript"];

  for (const name of candidates) {
    try {
      const resolved = require.resolve(name, { paths });
      const ts = require(resolved);
      if (typeof ts.createProgram === "function") {
        return ts;
      }
    } catch {
      // try the next candidate
    }
  }

  fail(
    "CLI plugins need the TypeScript compiler API, which TypeScript 7 does not provide.\n" +
      "Install TypeScript 6 alongside it:\n" +
      "  npm install --save-dev @typescript/typescript6\n" +
      "It is used only to generate plugin metadata; your code still compiles with TypeScript 7.",
  );
}

// The plugins `require("typescript")` themselves and call the classic API on
// whatever comes back. In a project set up for nestgo that is TypeScript 7,
// which has none of those functions, so the visitor dies on its first
// ts.visitNode. Point the specifier at the TypeScript 6 module we already
// resolved, for this process only.
function redirectTypeScriptRequires(ts) {
  const load = Module._load;
  Module._load = function (request, parent, isMain) {
    if (request === "typescript") {
      return ts;
    }
    return load.apply(this, arguments);
  };
}

// Resolving plugin paths is separated from loading them so that a plugin the
// project never installed is reported as such, rather than surfacing as
// whatever the next step happens to fail on.
function resolvePlugins(cwd, plugins) {
  const paths = [path.join(cwd, "node_modules"), ...module.paths];

  return plugins.map((entry) => {
    const name = typeof entry === "object" ? entry.name : entry;
    const options = (typeof entry === "object" && entry.options) || {};

    let resolved;
    try {
      // Plugins expose their entry point at "<name>/plugin"; fall back to the
      // package root, which is how the Nest CLI resolves them too.
      try {
        resolved = require.resolve(`${name}/${PLUGIN_ENTRY_FILENAME}`, { paths });
      } catch {
        resolved = require.resolve(name, { paths });
      }
    } catch {
      fail(`"${name}" plugin is not installed.`);
    }

    return { name, options, resolved };
  });
}

function loadVisitors(resolvedPlugins, extras) {
  const visitors = [];

  for (const { name, options, resolved } of resolvedPlugins) {
    const plugin = require(resolved);

    if (!plugin.ReadonlyVisitor) {
      fail(
        `"${name}" does not expose a ReadonlyVisitor, so its metadata cannot be generated ` +
          `without a transformer-capable compiler.`,
      );
    }

    const visitor = new plugin.ReadonlyVisitor({ ...options, ...extras, readonly: true });
    visitor.key = name;
    visitors.push(visitor);
  }

  return visitors;
}

function createProgram(ts, cwd, tsconfigPath) {
  const configPath = path.isAbsolute(tsconfigPath) ? tsconfigPath : path.join(cwd, tsconfigPath);
  const parsed = ts.getParsedCommandLineOfConfigFile(configPath, undefined, ts.sys);
  if (!parsed) {
    fail(`could not read ${configPath}`);
  }
  return ts.createProgram({
    rootNames: parsed.fileNames,
    projectReferences: parsed.projectReferences,
    options: { ...parsed.options, noEmit: true },
  });
}

// The output shape below matches what @nestjs/swagger and @nestjs/graphql
// expect from SwaggerModule.loadPluginMetadata / GraphQLModule's metadata
// option, so it has to match the Nest CLI's printer exactly.
function printMetadata(ts, metadata, typeImports, outputDir, filename) {
  const propertyAssignment = (identifier, meta) => {
    if (Array.isArray(meta)) {
      return ts.factory.createPropertyAssignment(
        ts.factory.createStringLiteral(identifier),
        ts.factory.createArrayLiteralExpression(
          meta.map(([importExpr, entryMeta]) =>
            ts.factory.createArrayLiteralExpression([
              importExpr,
              ts.factory.createObjectLiteralExpression(
                Object.keys(entryMeta).map((key) => propertyAssignment(key, entryMeta[key])),
              ),
            ]),
          ),
        ),
      );
    }
    return ts.factory.createPropertyAssignment(
      ts.factory.createStringLiteral(identifier),
      ts.isObjectLiteralExpression(meta)
        ? meta
        : ts.factory.createObjectLiteralExpression(
            Object.keys(meta).map((key) => propertyAssignment(key, meta[key])),
          ),
    );
  };

  const typeImportsStatement = ts.factory.createVariableStatement(
    undefined,
    ts.factory.createVariableDeclarationList(
      [
        ts.factory.createVariableDeclaration(
          ts.factory.createIdentifier(TYPE_IMPORT_VARIABLE_NAME),
          undefined,
          undefined,
          ts.factory.createObjectLiteralExpression(
            Object.keys(typeImports).map((key) =>
              ts.factory.createPropertyAssignment(
                ts.factory.createComputedPropertyName(ts.factory.createStringLiteral(key)),
                ts.factory.createIdentifier(typeImports[key]),
              ),
            ),
            true,
          ),
        ),
      ],
      ts.NodeFlags.Const |
        ts.NodeFlags.AwaitContext |
        ts.NodeFlags.ContextFlags |
        ts.NodeFlags.TypeExcludesFlags,
    ),
  );

  const exportAssignment = ts.factory.createExportAssignment(
    undefined,
    undefined,
    ts.factory.createArrowFunction(
      [ts.factory.createToken(ts.SyntaxKind.AsyncKeyword)],
      undefined,
      [],
      undefined,
      ts.factory.createToken(ts.SyntaxKind.EqualsGreaterThanToken),
      ts.factory.createBlock(
        [
          typeImportsStatement,
          ts.factory.createReturnStatement(
            ts.factory.createObjectLiteralExpression(
              Object.keys(metadata).map((key) => propertyAssignment(key, metadata[key])),
            ),
          ),
        ],
        true,
      ),
    ),
  );

  const printer = ts.createPrinter({ newLine: ts.NewLineKind.LineFeed });
  const resultFile = ts.createSourceFile("file.ts", "", ts.ScriptTarget.Latest, false, ts.ScriptKind.TS);
  const target = path.join(outputDir, filename || SERIALIZED_METADATA_FILENAME);

  let output = printer.printNode(ts.EmitHint.Unspecified, exportAssignment, resultFile);

  // The visitors emit extensionless dynamic imports — import("./cats.controller")
  // — which node16/nodenext resolution rejects. Those are the only resolutions
  // TypeScript 7 still has, so without this the generated file cannot compile in
  // any project that uses plugins.
  output = output.replace(
    /import\((["'])(\.[^"']*?)\1\)/g,
    (match, quote, specifier) =>
      /\.[cm]?js$/.test(specifier) ? match : `import(${quote}${specifier}.js${quote})`,
  );

  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, "/* eslint-disable */\n" + output);
  return target;
}

function main() {
  let config;
  try {
    config = JSON.parse(process.argv[2]);
  } catch (err) {
    fail(`could not parse the sidecar config: ${err.message}`);
  }

  const { cwd, tsconfigPath, outputDir, filename, plugins } = config;
  if (!plugins || plugins.length === 0) {
    process.stdout.write(JSON.stringify({ written: null }));
    return;
  }

  // Check the plugins exist before anything else, so a typo in nest-cli.json
  // is reported as a typo.
  const resolvedPlugins = resolvePlugins(cwd, plugins);

  const ts = loadTypeScript(cwd);
  redirectTypeScriptRequires(ts);
  const visitors = loadVisitors(resolvedPlugins, { pathToSource: outputDir });
  const program = createProgram(ts, cwd, tsconfigPath);

  for (const sourceFile of program.getSourceFiles()) {
    if (!sourceFile.isDeclarationFile) {
      for (const visitor of visitors) {
        visitor.visit(program, sourceFile);
      }
    }
  }

  let typeImports = {};
  const metadata = {};
  for (const visitor of visitors) {
    metadata[visitor.key] = visitor.collect();
    typeImports = { ...typeImports, ...visitor.typeImports };
  }

  const written = printMetadata(ts, metadata, typeImports, outputDir, filename);
  process.stdout.write(JSON.stringify({ written }));
}

try {
  main();
} catch (err) {
  fail(err && err.stack ? err.stack : String(err));
}
