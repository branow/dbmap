#!/usr/bin/env node
"use strict";
// CLI for the catalog stages of the database index: manifest, structure and
// module bodies, cached per environment and database.
//
// Usage: node fetch-db-structure.js -e <env> [-d <database>] [-o <cache-dir>]
//
// Everything here reads sys.* catalog views only — no user table is read and no
// row of customer data is fetched. Sampling is a separate stage for that reason.
// Each source and each batch is preceded by a memory-headroom check, so a run
// stops on its own rather than pushing an instance that is already tight.

const { mkdirSync, writeFileSync, readFileSync, existsSync } = require("node:fs");
const { join } = require("node:path");
const args = require("./lib/args.js");
const { createLogger } = require("./lib/logger.js");
const { resolveTargets } = require("./db-index/targets.js");
const { fetchManifest, summarize } = require("./db-index/manifest.js");
const { fetchStructure } = require("./db-index/structure.js");
const { fetchModules } = require("./db-index/modules.js");
const { checkHealth } = require("./db-index/health.js");
const { DEFAULT_CACHE } = require("./fetch-db-manifest.js");

async function main(argv, injected = {}) {
  const flags = args.parse(argv).flags;
  const env = args.alias(flags, "e", "env");
  const only = args.alias(flags, "d", "database");
  const cacheDir = args.resolve(args.alias(flags, "o", "out"), DEFAULT_CACHE);
  if (!env) throw new Error("fetch-db-structure: -e/--env <env> is required");

  const deps = { logger: createLogger({ name: "db-index" }), ...injected };
  const report = [];

  for (const target of resolveTargets(env, typeof only === "string" ? only : undefined)) {
    const dir = join(cacheDir, target.env, target.logical);
    mkdirSync(dir, { recursive: true });

    const before = await checkHealth(target, deps);
    deps.logger.info(`${target.logical}: ${before.health?.availableGb ?? "?"} GB free before start`);

    const manifest = await readOrFetchManifest(dir, target, deps);
    const structure = await fetchStructure(target, deps);
    const { definitions, redactions } = await fetchModules(target, manifest.objects, deps);

    write(dir, "structure.json", structure);
    write(dir, "modules.json", definitions);

    const after = await checkHealth(target, deps);
    report.push({
      ...summarize(manifest),
      structured: Object.keys(structure).length,
      bodies: Object.keys(definitions).length,
      bodyBytes: Object.values(definitions).reduce((n, d) => n + d.length, 0),
      redactions,
      freeGbBefore: before.health?.availableGb ?? null,
      freeGbAfter: after.health?.availableGb ?? null,
    });
  }

  return { env, cacheDir, databases: report };
}

// The manifest is reused when already cached, so a resumed run does not requery
// what it already has.
async function readOrFetchManifest(dir, target, deps) {
  const path = join(dir, "manifest.json");
  if (existsSync(path)) {
    const cached = JSON.parse(readFileSync(path, "utf8"));
    if (cached.objects?.[0]?.objectId) return cached;
  }
  const manifest = await fetchManifest(target, deps);
  write(dir, "manifest.json", manifest);
  return manifest;
}

function write(dir, name, value) {
  writeFileSync(join(dir, name), JSON.stringify(value, null, 2) + "\n");
}

if (require.main === module) {
  main(process.argv.slice(2))
    .then((summary) => process.stdout.write(JSON.stringify(summary, null, 2) + "\n"))
    .catch((err) => {
      process.stderr.write(`${err.message}\n`);
      process.exit(1);
    });
}

module.exports = { main };
