"use strict";
// The one place this build talks to SQL Server.
//
// Every query goes out on a `-K ReadOnly` connection and is checked against
// DENIED here first, because a script spawning sqlcmd never reaches the
// PreToolUse gate that gets applied to a Bash command. The check is
// deliberately stricter than that gate: a statement must open with SELECT or
// WITH, so the build can only ever read, and a query it cannot prove read-only
// fails before a connection is opened.
//
// Output is read in `-h -1 -W -s <sep>` mode: no headers, trailing whitespace
// trimmed, one row per line. sqlcmd's own row-count footer is dropped.

const { execFile } = require("node:child_process");

const SEPARATOR = "\t";
const LOGIN_TIMEOUT = 30;
const QUERY_TIMEOUT = 120;
const MAX_BUFFER = 64 * 1024 * 1024;
const MAX_GRANT_PERCENT = 1;

const DENIED = [
  "insert",
  "update",
  "delete",
  "merge",
  "into",
  "create",
  "alter",
  "drop",
  "truncate",
  "grant",
  "revoke",
  "deny",
  "backup",
  "restore",
  "dbcc",
  "exec",
  "execute",
  "shutdown",
  "kill",
  "waitfor",
  "openrowset",
  "opendatasource",
  "openquery",
  "xp_cmdshell",
  "sp_configure",
];

const OPENS_READ = /^\s*(select|with)\b/i;
const ROWS_AFFECTED = /^\(\d+ rows? affected\)$/i;

// Throws unless `sql` is provably a read. Returns the query unchanged so
// callers can inline the check.
function assertReadOnly(sql) {
  const text = String(sql ?? "");
  if (!OPENS_READ.test(text)) throw new Error("db-index: query must open with SELECT or WITH, refusing to send it");

  const hit = DENIED.find((word) => new RegExp(`\\b${word}\\b`, "i").test(text));
  if (hit) throw new Error(`db-index: query contains "${hit}", refusing to send it`);

  return text;
}

// Caps what a query may cost the server, applied to every statement this build
// sends rather than to the ones a author remembers to guard.
//
// MAX_GRANT_PERCENT is the load-bearing one: it ceilings the query's memory
// grant at 1% of the workspace limit, so a query that wants more spills to
// tempdb instead of taking RAM the OS needs. On an instance configured with
// 119 GB of a 125 GB box that difference is the difference between a slow query
// and an unreachable server. MAXDOP 1 keeps a parallel plan from multiplying
// the footprint per thread.
function withGuard(sql) {
  return `${String(sql ?? "").replace(/;\s*$/, "")}\nOPTION (MAXDOP 1, MAX_GRANT_PERCENT = ${MAX_GRANT_PERCENT})`;
}

// Output modes. "rows" is tab-separated and header-free, for metadata whose
// cells never contain a tab or newline. "raw" is for module definitions, which
// contain both: sqlcmd refuses -y 0 alongside either -W or -h, so raw output
// keeps the untruncated value and the caller splits it on its own sentinel.
const MODES = {
  rows: ["-h", "-1", "-W", "-s", SEPARATOR],
  raw: ["-y", "0"],
};

// argv for one read-only sqlcmd invocation. Separate from run() so a test can
// assert the flags without spawning anything.
function buildArgs({ host, database, query, mode = "rows" }) {
  const shape = MODES[mode];
  if (!shape) throw new Error(`db-index: unknown sqlcmd output mode "${mode}"`);

  return [
    "-S",
    host,
    "-E",
    "-C",
    "-M",
    "-d",
    database,
    "-K",
    "ReadOnly",
    "-l",
    String(LOGIN_TIMEOUT),
    "-t",
    String(QUERY_TIMEOUT),
    "-b",
    ...shape,
    "-Q",
    withGuard(assertReadOnly(query)),
  ];
}

// Splits sqlcmd's row output into cells, dropping blank lines and the
// "(N rows affected)" footer.
function parseRows(stdout) {
  return String(stdout ?? "")
    .split("\n")
    .map((line) => line.replace(/\r$/, ""))
    .filter((line) => line.trim() !== "" && !ROWS_AFFECTED.test(line.trim()))
    .map((line) => line.split(SEPARATOR));
}

// Runs one query. Resolves parsed rows in "rows" mode and the raw stdout in
// "raw" mode. `deps.execFile` is injectable so tests never spawn sqlcmd.
function run({ host, database, query, mode = "rows" }, deps = {}) {
  const exec = deps.execFile ?? execFile;
  const args = buildArgs({ host, database, query, mode });

  return new Promise((resolve, reject) => {
    exec("sqlcmd", args, { maxBuffer: MAX_BUFFER }, (err, stdout, stderr) => {
      if (err)
        return reject(new Error(`sqlcmd failed on ${database}@${host}: ${String(stderr || err.message).trim()}`));
      resolve(mode === "raw" ? String(stdout ?? "") : parseRows(stdout));
    });
  });
}

module.exports = {
  assertReadOnly,
  withGuard,
  buildArgs,
  parseRows,
  run,
  SEPARATOR,
  DENIED,
  MAX_GRANT_PERCENT,
  MODES,
};
