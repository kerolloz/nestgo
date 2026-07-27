#!/usr/bin/env node
"use strict";

// Launcher script for the nestgo binary.
// Locates the platform-specific binary from @nestgo/core-<platform> and
// executes it with the same arguments. Follows the esbuild/ttsc pattern.

const { execFileSync } = require("child_process");
const { existsSync, statSync, chmodSync } = require("fs");
const path = require("path");

const PLATFORMS = {
  "darwin-arm64": { pkg: "@nestgo/core-darwin-arm64", bin: "nestgo" },
  "darwin-x64":   { pkg: "@nestgo/core-darwin-x64",   bin: "nestgo" },
  "linux-x64":    { pkg: "@nestgo/core-linux-x64",    bin: "nestgo" },
  "linux-arm64":  { pkg: "@nestgo/core-linux-arm64",  bin: "nestgo" },
  "win32-x64":    { pkg: "@nestgo/core-win32-x64",    bin: "nestgo.exe" },
  "win32-arm64":  { pkg: "@nestgo/core-win32-arm64",  bin: "nestgo.exe" },
};

function ensureExecutable(binary) {
  if (process.platform === "win32") return;
  try {
    const mode = statSync(binary).mode & 0o777;
    if ((mode & 0o111) !== 0) return;
    chmodSync(binary, mode | 0o755);
  } catch {
    // keep the original spawn error path
  }
}

function getBinaryPath() {
  if (process.env.NESTGO_BINARY) {
    return process.env.NESTGO_BINARY;
  }

  const localBinary = path.join(__dirname, "..", "..", "..", "bin", "nestgo");
  if (existsSync(localBinary)) {
    return localBinary;
  }

  const key = `${process.platform}-${process.arch}`;
  const entry = PLATFORMS[key];
  if (!entry) {
    console.error(
      `nestgo: unsupported platform ${key}\n` +
      `No pre-built binary is available for this platform.`
    );
    process.exit(1);
  }

  try {
    return require.resolve(`${entry.pkg}/bin/${entry.bin}`);
  } catch {
    console.error(
      `nestgo: could not find binary package ${entry.pkg}\n` +
      `Make sure it is installed. You may need to run "npm install".`
    );
    process.exit(1);
  }
}

const binPath = getBinaryPath();
ensureExecutable(binPath);

try {
  execFileSync(binPath, process.argv.slice(2), {
    stdio: "inherit",
    env: process.env,
  });
} catch (err) {
  if (err.status != null) {
    process.exit(err.status);
  }
  console.error(`nestgo: failed to execute ${binPath}`);
  console.error(err.message);
  process.exit(1);
}
