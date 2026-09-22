#!/usr/bin/env node
"use strict";
// Proof-of-concept driver for the database index.
//
// Usage: node build-db-index.js -e <env> -d <database> [-m <model>]
//        [--match <substring>] [--limit N] [--samples N] [-o <dir>]
//
// Builds a PARTIAL index by default: --match narrows to objects whose name
// contains a substring and --limit caps how many are processed, so the concept
// can be proven against forty objects before anyone waits on 1,237. With
// neither flag it does the whole database.
//
// Reads the cache written by fetch-db-structure.js, samples the tables it is
// going to describe, describes them, and writes the index files. Nothing here
// is wired into the plugin yet.

const { readFileSync, existsSync, mkdirSync } = require("node:fs");
const { join } = require("node:path");
const args = require("./lib/args.js");
const { createLogger } = require("./lib/logger.js");
const { resolveTargets } = require("./db-index/targets.js");
const fingerprint = require("./db-index/fingerprint.js");
const plan = require("./db-index/plan.js");
const sample = require("./db-index/sample.js");
const describe = require("./db-index/describe-object.js");
const write = require("./db-index/write.js");
const { DEFAULT_CACHE } = require("./fetch-db-manifest.js");

const DEFAULT_OUT = ".claude-work/db-index";

async function main(argv, injected = {}) {
  const flags = args.parse(argv).flags;
  const env = args.alias(flags, "e", "env");
  const database = args.alias(flags, "d", "database");
  const model = args.alias(flags, "m", "model");
  const match = args.alias(flags, "match");
  const limit = Number(args.alias(flags, "limit")) || Infinity;
  const samples = Number(args.alias(flags, "samples")) || 0;
  const cacheDir = args.resolve(args.alias(flags, "c", "cache"), DEFAULT_CACHE);
  const outDir = args.resolve(args.alias(flags, "o", "out"), DEFAULT_OUT);

  if (!env || !database) throw new Error("build-db-index: -e <env> and -d <database> are required");

  const deps = { logger: createLogger({ name: "db-index" }), ...injected };
  const [target] = resolveTargets(env, database);
  const cache = readCache(join(cacheDir, env, database));

  const all = entriesFrom(cache);
  const selected = select(all, { match: typeof match === "string" ? match : null, limit });
  deps.logger.info(`selected ${selected.length} of ${all.length} objects`);

  const state = readState(join(outDir, env, database));
  const { describe: todo, unchanged } = plan.planDescribe(selected, state);

  const sampled = samples > 0 ? await sample.sampleAll(target, selected, { ...deps, limit: samples }) : {};
  const described = model ? await describe.describeAll(plan.describable(todo), { sampled, model, cache, ...deps }) : {};

  const entries = selected.map((entry) => ({
    ...entry,
    fingerprint: fingerprint.fingerprint(entry.object.kind, entry.payload),
    description: described[entry.key] ?? state.get(entry.key)?.description ?? "",
  }));

  const dir = join(outDir, env, database);
  mkdirSync(dir, { recursive: true });
  const written = write.writeIndex(dir, entries);

  return {
    env,
    database,
    outDir: dir,
    objects: entries.length,
    described: Object.keys(described).length,
    reused: unchanged.length,
    sampled: Object.keys(sampled).length,
    ...written,
  };
}

function readCache(dir) {
  const read = (name) => {
    const path = join(dir, name);
    if (!existsSync(path)) throw new Error(`build-db-index: ${path} missing — run fetch-db-structure.js first`);
    return JSON.parse(readFileSync(path, "utf8"));
  };
  return { manifest: read("manifest.json"), structure: read("structure.json"), modules: read("modules.json") };
}

// Pairs each manifest object with its structure and the payload its kind
// fingerprints over.
function entriesFrom({ manifest, structure, modules }) {
  return manifest.objects.map((object) => {
    const key = `${object.schema}.${object.name}`;
    const s = structure[key] ?? {};
    return {
      key,
      object,
      structure: s,
      payload:
        object.kind === "table"
          ? { ...s, rows: object.rows, triggers: object.triggers }
          : { definition: modules[key] ?? "", target: s.target },
    };
  });
}

function select(entries, { match, limit }) {
  const matched = match ? entries.filter((e) => e.key.toLowerCase().includes(match.toLowerCase())) : entries;
  return matched.slice(0, limit);
}

// The previous build's state comes out of the catalogs it wrote, so there is
// nothing else to keep in step with them.
function readState(dir) {
  return write.readState(dir, (path) => (existsSync(path) ? readFileSync(path, "utf8") : null));
}

if (require.main === module) {
  main(process.argv.slice(2))
    .then((summary) => process.stdout.write(JSON.stringify(summary, null, 2) + "\n"))
    .catch((err) => {
      process.stderr.write(`${err.message}\n`);
      process.exit(1);
    });
}

module.exports = { main, entriesFrom, select };
