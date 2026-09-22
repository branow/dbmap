#!/usr/bin/env node
"use strict";
// CLI for the manifest stage of the database index.
//
// Usage: node fetch-db-manifest.js -e <env> [-d <database>] [-o <cache-dir>]
//
// Queries one database per invocation-step and writes each manifest to
// <cache-dir>/<env>/<database>/manifest.json. Nothing else in the build talks
// to SQL Server until a manifest exists, so this is also the connectivity
// check: if it fails, no later stage is worth starting.

const { mkdirSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");
const args = require("./lib/args.js");
const { resolveTargets } = require("./db-index/targets.js");
const { fetchManifest, summarize } = require("./db-index/manifest.js");

const DEFAULT_CACHE = ".claude-work/db-index-cache";

async function main(argv, deps = {}) {
  const flags = args.parse(argv).flags;
  const env = args.alias(flags, "e", "env");
  const only = args.alias(flags, "d", "database");
  const cacheDir = args.resolve(args.alias(flags, "o", "out"), DEFAULT_CACHE);

  if (!env) throw new Error("fetch-db-manifest: -e/--env <env> is required");

  const summaries = [];
  for (const target of resolveTargets(env, typeof only === "string" ? only : undefined)) {
    const manifest = await fetchManifest(target, deps);
    writeManifest(cacheDir, manifest);
    summaries.push(summarize(manifest));
  }
  return { env, cacheDir, databases: summaries };
}

function writeManifest(cacheDir, manifest) {
  const dir = join(cacheDir, manifest.env, manifest.database);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
}

if (require.main === module) {
  main(process.argv.slice(2))
    .then((summary) => process.stdout.write(JSON.stringify(summary, null, 2) + "\n"))
    .catch((err) => {
      process.stderr.write(`${err.message}\n`);
      process.exit(1);
    });
}

module.exports = { main, writeManifest, DEFAULT_CACHE };
