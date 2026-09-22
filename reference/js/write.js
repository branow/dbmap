"use strict";
// Writes the index files an agent reads.
//
// Shape, per environment and database:
//
//   tables.tsv       one row per table: size, trigger count, description
//   views.tsv        one row per view
//   procedures.tsv   one row per procedure, parameters inline
//   functions.tsv    one row per function, parameters and return type inline
//   synonyms.tsv     one row per synonym and what it points at
//   columns/<schema>.<name>.tsv   per table and view: every column, the
//                    primary key and the indexes
//
// Every catalog carries `modified` and `fingerprint` beside the description.
// The next build reads its own staleness state straight out of the file it
// wrote, so there is no second file to drift out of step — and `modified` earns
// its place for a reader too, since a procedure untouched since 2018 says
// something a description cannot. Nothing derived is stored: a table's sample
// depth is min(rows, 25) and `rows` is already there.
//
// Columns get a file each because a reader needs all of them; procedures and
// functions do not, because their parameters fit on one line and a body is not
// something the index reproduces. There is no foreign key file: the three
// databases declare two foreign keys between them, so a join is a naming
// convention here and the index does not pretend otherwise.

const { mkdirSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");

const TAB = "\t";

// One catalog per kind. `facts` are the columns specific to that kind; every
// catalog then gets name, those facts, and the shared staleness trailer.
const TRAILER = ["modified", "fingerprint", "description"];

const CATALOGS = [
  {
    file: "tables.tsv",
    kind: "table",
    facts: ["rows", "size", "triggers", "cols"],
    values: (e) => [e.object.rows, size(e.object.kb), e.object.triggers, count(e, "columns")],
  },
  {
    file: "views.tsv",
    kind: "view",
    facts: ["cols"],
    values: (e) => [count(e, "columns")],
  },
  {
    file: "procedures.tsv",
    kind: "procedure",
    facts: ["params"],
    values: (e) => [params(e)],
  },
  {
    file: "functions.tsv",
    kind: "function",
    facts: ["returns", "params"],
    values: (e) => [returns(e), params(e)],
  },
  {
    file: "synonyms.tsv",
    kind: "synonym",
    facts: ["target"],
    values: (e) => [e.structure?.target ?? ""],
  },
];

function header(catalog) {
  return ["name", ...catalog.facts, ...TRAILER];
}

function rowFor(catalog, entry) {
  return [entry.key, ...catalog.values(entry), entry.object.modified, entry.fingerprint, entry.description];
}

// Where `rows` sits in a catalog's row, for the reader that needs it back.
function rowsIndexOf(catalog) {
  const at = catalog.facts.indexOf("rows");
  return at === -1 ? -1 : at + 1;
}

// Kinds whose columns are worth a file of their own.
const DETAILED = ["table", "view"];

function size(kb) {
  const n = Number(kb) || 0;
  if (n >= 1048576) return `${Math.round(n / 1048576)} GB`;
  if (n >= 1024) return `${Math.round(n / 1024)} MB`;
  return `${n} KB`;
}

function count(entry, field) {
  return String((entry.structure?.[field] ?? []).length);
}

// "@CustomerID int, @Since datetime" — the call signature, minus the return
// row sys.parameters emits with an empty name.
function params(entry) {
  return (entry.structure?.parameters ?? [])
    .filter((p) => p.name !== "(returns)")
    .map((p) => `${p.name} ${p.type}${p.output ? " out" : ""}`)
    .join(", ");
}

function returns(entry) {
  return (entry.structure?.parameters ?? []).find((p) => p.name === "(returns)")?.type ?? "table";
}

function clean(value) {
  return String(value ?? "")
    .replace(/[\t\n\r]/g, " ")
    .trim();
}

function tsv(header, rows) {
  return [header.join(TAB), ...rows.map((cells) => cells.map(clean).join(TAB))].join("\n") + "\n";
}

// A table or view file: its columns, then its key and indexes as comments so a
// reader takes them in without parsing a second shape.
function renderColumns(entry) {
  const s = entry.structure ?? {};
  const head = [`# ${entry.key} @${entry.fingerprint}`];
  if (entry.object.kind === "table")
    head.push(`# rows ${entry.object.rows}  size ${size(entry.object.kb)}  triggers ${entry.object.triggers}`);
  if (entry.description) head.push(`# ${entry.description}`);

  const rows = (s.columns ?? []).map((c) => [
    c.name,
    `${c.type}${c.length ?? ""}`,
    c.nullable ? "null" : "not null",
    [c.identity ? "identity" : "", c.computed ? "computed" : ""].filter(Boolean).join(" "),
  ]);

  const tail = [];
  if (s.primaryKey?.length) tail.push(`# pk ${s.primaryKey.join(", ")}`);
  for (const i of s.indexes ?? []) tail.push(`# index ${i.name} (${i.columns.join(", ")})${i.unique ? " unique" : ""}`);
  for (const f of s.foreignKeys ?? []) tail.push(`# fk ${f.column} -> ${f.references}`);

  return [...head, tsv(["column", "type", "null", "extra"], rows).trimEnd(), ...tail].join("\n") + "\n";
}

// Reads one catalog back into the Map planFetch and planDescribe expect. Row
// counts come back only for the catalog that stores them, which is the only one
// whose objects are ever sampled.
function parseCatalog(content, catalog) {
  const state = new Map();
  const at = rowsIndexOf(catalog);
  const width = header(catalog).length;

  for (const line of String(content ?? "").split("\n")) {
    if (!line.trim() || line.startsWith("#")) continue;
    const cells = line.split(TAB);
    if (cells[0] === "name" || !cells[0] || cells.length < width - 1) continue;
    state.set(cells[0], {
      modified: cells[width - 3] ?? "",
      fingerprint: cells[width - 2] ?? "",
      description: cells[width - 1] ?? "",
      rows: at === -1 ? 0 : Number(cells[at]) || 0,
    });
  }
  return state;
}

// entries: [{ key, object, structure, fingerprint, description }]
function writeIndex(dir, entries) {
  mkdirSync(join(dir, "columns"), { recursive: true });
  const written = [];

  for (const catalog of CATALOGS) {
    const rows = entries.filter((e) => e.object.kind === catalog.kind);
    if (rows.length === 0) continue;
    writeFileSync(
      join(dir, catalog.file),
      tsv(
        header(catalog),
        rows.map((e) => rowFor(catalog, e)),
      ),
    );
    written.push(`${catalog.file} (${rows.length})`);
  }

  const detailed = entries.filter((e) => DETAILED.includes(e.object.kind) && e.structure?.columns?.length);
  for (const entry of detailed) writeFileSync(join(dir, "columns", `${entry.key}.tsv`), renderColumns(entry));

  return { catalogs: written, columnFiles: detailed.length };
}

// The whole index's state, read back from the catalogs it was written to.
function readState(dir, readFile) {
  const state = new Map();
  for (const catalog of CATALOGS) {
    const content = readFile(join(dir, catalog.file));
    if (content == null) continue;
    for (const [key, value] of parseCatalog(content, catalog)) state.set(key, value);
  }
  return state;
}

module.exports = {
  CATALOGS,
  DETAILED,
  TRAILER,
  header,
  size,
  params,
  returns,
  renderColumns,
  parseCatalog,
  readState,
  writeIndex,
  tsv,
};
