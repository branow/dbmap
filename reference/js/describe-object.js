"use strict";
// Describe stage: one sentence per object, from a model with no tools.
//
// Mirrors index-build/describe.js — `claude -p` as a pure LLM, all context in
// the prompt — but the unit is one database object rather than one repository,
// because the inputs differ per kind: a table is described from its columns and
// a sample, a procedure from its body.
//
// Prompts are built per kind from PROMPTS, so a new kind is a row rather than a
// branch, and every prompt is a pure function of cached data a test can pass in.

const { spawn } = require("node:child_process");
const { renderSample } = require("./sample.js");

// The field is `sentence`, not `description`, and its schema text is an order
// rather than a noun phrase. Named `description` with the text "one sentence
// saying what this object does", models fill it with a description OF THE FIELD
// — "One-sentence summary of what dbo.PromoGet does" — and the better the model
// follows instructions the more reliably it does so.
const SENTENCE_FIELD = {
  type: "string",
  description: "Put the finished sentence here verbatim. Never describe what the sentence would say.",
};

const LOOKUP_FIELD = {
  type: "boolean",
  description: "For tables only: true when the table is a lookup of codes and their meanings.",
};

const SINGLE_SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: ["sentence"],
  properties: { sentence: SENTENCE_FIELD, lookup: LOOKUP_FIELD },
};

const BATCH_SCHEMA = {
  type: "object",
  additionalProperties: false,
  required: ["objects"],
  properties: {
    objects: {
      type: "array",
      description: "One entry per object described, in the order given.",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["name", "sentence"],
        properties: {
          name: { type: "string", description: "The object name exactly as it appeared in its header." },
          sentence: SENTENCE_FIELD,
          lookup: LOOKUP_FIELD,
        },
      },
    },
  },
};

// A procedure's logic cannot be summarised from its opening — the writes are
// usually at the bottom — so the body goes to the model whole wherever it can.
// 16,000 characters sends 90% of AppCore's 587 modules complete (they average
// 6,543); the cap exists only for the tail, where the longest is 88,623 and is
// a generated contest builder whose first pages are representative.
const MAX_BODY_CHARS = 16000;

// Without this a small model restates the instruction — "Stored procedure
// analysis: functionality, operation type, and data sources" — instead of
// answering it. Naming the failure and showing the shape fixes it.
const VERB_FIRST = [
  "Answer in one sentence that starts with a verb, present tense, under 25 words.",
  "Do not restate this instruction, do not write the word summary or analysis, and do not repeat the object name.",
].join("\n");

const PROMPTS = {
  table: (entry, ctx) =>
    [
      `SQL Server table ${entry.key} in the ${ctx.database} database.`,
      `${entry.object.rows} rows.`,
      "",
      "Columns:",
      columnLines(entry),
      keyLines(entry),
      ctx.sample ? "\nSample rows:\n" + renderSample(ctx.sample) : "",
      "",
      "Describe what this table holds, naming the grain — what one row is.",
      "If it is a lookup of codes and their meanings, say so and set lookup=true.",
      "Do not list the columns back; they are already indexed.",
      VERB_FIRST,
      'Good: "Maps order status codes to their descriptions, one row per status."',
      'Good: "Holds one row per placed order, keyed by OrderID."',
    ].join("\n"),

  view: (entry, ctx) =>
    [
      `SQL Server view ${entry.key} in the ${ctx.database} database.`,
      "",
      "Columns:",
      columnLines(entry),
      "",
      "Definition:",
      body(ctx.definition),
      "",
      "Write one sentence saying what this view exposes and from where.",
    ].join("\n"),

  procedure: (entry, ctx) =>
    [
      `SQL Server stored procedure ${entry.key} in the ${ctx.database} database.`,
      parameterLine(entry),
      "",
      "Body:",
      body(ctx.definition),
      "",
      "Describe what this procedure does, naming the tables it touches.",
      VERB_FIRST,
      'Good: "Inserts a promo code into dbo.PromoConfigurations, replacing any existing row for the same code."',
      'Good: "Reads order totals from dbo.Orders and dbo.OrderDetails for a single customer."',
      'Bad: "Stored procedure analysis: functionality, operation type, and data sources."',
    ].join("\n"),

  function: (entry, ctx) =>
    [
      `SQL Server function ${entry.key} in the ${ctx.database} database.`,
      parameterLine(entry),
      "",
      "Body:",
      body(ctx.definition),
      "",
      "Describe what this function returns.",
      VERB_FIRST,
      'Good: "Returns the commission run id covering a given period and period type."',
    ].join("\n"),
};

function columnLines(entry) {
  return (entry.structure?.columns ?? [])
    .map((c) => `  ${c.name} ${c.type}${c.length ?? ""}${c.nullable ? "" : " not null"}`)
    .join("\n");
}

function keyLines(entry) {
  const pk = entry.structure?.primaryKey ?? [];
  return pk.length ? `\nPrimary key: ${pk.join(", ")}` : "";
}

function parameterLine(entry) {
  const params = (entry.structure?.parameters ?? []).filter((p) => p.name !== "(returns)");
  return params.length ? `Parameters: ${params.map((p) => `${p.name} ${p.type}`).join(", ")}` : "No parameters.";
}

// A body is truncated for the prompt rather than for the cache: the opening of
// a procedure says what it does, and the tail is rarely worth the tokens.
function body(definition) {
  const text = String(definition ?? "").trim();
  if (!text) return "(no body available — CLR or encrypted)";
  return text.length > MAX_BODY_CHARS ? `${text.slice(0, MAX_BODY_CHARS)}\n-- truncated` : text;
}

function buildPrompt(entry, ctx) {
  const prompt = PROMPTS[entry.object.kind];
  if (!prompt) throw new Error(`db-index: no prompt defined for kind "${entry.object.kind}"`);
  return prompt(entry, ctx);
}

// Describes one object. `invoke` is injectable so tests never spawn a model.
async function describeObject(entry, opts) {
  const invoke = opts.invoke ?? invokeClaude;
  const structured = await invoke(buildPrompt(entry, opts.context), opts.model, SINGLE_SCHEMA);
  return structured?.sentence?.trim() ?? "";
}

// Several objects per call. Each `claude -p` pays process startup and a fresh
// context, so 1,237 of them is mostly overhead — and a batch of small objects
// costs barely more than one of them alone.
//
// Batches are sized by prompt characters rather than by count, because the
// objects are wildly uneven: a 200-line procedure and a two-column lookup table
// are not the same unit of work. A single object over the budget goes alone.
// Sized so a batch still holds about six procedures at their 6,543-character
// average, which is the grouping the output was checked at.
const BATCH_CHARS = 40000;
const BATCH_MAX = 12;

function batchByCost(prompts, { chars = BATCH_CHARS, max = BATCH_MAX } = {}) {
  const batches = [];
  let current = [];
  let spent = 0;

  for (const item of prompts) {
    const cost = item.prompt.length;
    if (current.length && (spent + cost > chars || current.length >= max)) {
      batches.push(current);
      current = [];
      spent = 0;
    }
    current.push(item);
    spent += cost;
  }
  if (current.length) batches.push(current);
  return batches;
}

function buildBatchPrompt(items) {
  const blocks = items.map((i, n) => `=== OBJECT ${n + 1}: ${i.key} ===\n${i.prompt}`);
  return [
    `Describe ${items.length} database objects. Each is delimited below.`,
    "Return one entry per object, with `name` exactly as given in its header.",
    "",
    ...blocks,
  ].join("\n\n");
}

// Runs `todo` in batches, returning { key: description }. A failed batch is
// logged and skipped rather than failing the run, because a partial index is
// worth more than none.
async function describeAll(todo, opts) {
  const invoke = opts.invoke ?? invokeClaude;
  const prompts = todo.map((item) => {
    const entry = item.entry ?? item;
    const key = entry.key ?? `${entry.object.schema}.${entry.object.name}`;
    return {
      key,
      prompt: buildPrompt(entry, {
        database: opts.cache?.manifest?.database ?? "",
        definition: opts.cache?.modules?.[key] ?? "",
        sample: opts.sampled?.[key] ?? null,
      }),
    };
  });

  const batches = batchByCost(prompts, opts);
  const descriptions = {};

  for (const [index, items] of batches.entries()) {
    try {
      const structured = await invoke(buildBatchPrompt(items), opts.model, BATCH_SCHEMA);
      for (const row of structured?.objects ?? []) {
        if (row?.name && row.sentence?.trim()) descriptions[row.name] = row.sentence.trim();
      }
      const missing = items.filter((i) => !descriptions[i.key]).map((i) => i.key);
      if (missing.length) opts.logger?.warn?.(`describe: no sentence for ${missing.join(", ")}`);
      opts.logger?.info?.(`describe: batch ${index + 1}/${batches.length} (${items.length} objects)`);
    } catch (err) {
      opts.logger?.warn?.(`describe: batch ${index + 1} failed — ${err.message}`);
    }
  }
  return descriptions;
}

function invokeClaude(prompt, model, schema = SINGLE_SCHEMA) {
  return new Promise((resolve, reject) => {
    const proc = spawn("claude", [
      "-p",
      prompt,
      "--output-format",
      "json",
      "--model",
      model,
      "--json-schema",
      JSON.stringify(schema),
      "--allowedTools",
      "",
    ]);

    let stdout = "";
    let stderr = "";
    proc.stdout.on("data", (c) => (stdout += c));
    proc.stderr.on("data", (c) => (stderr += c));
    proc.on("error", reject);
    proc.on("close", (code) => {
      if (code !== 0) return reject(new Error(`claude exited ${code}: ${stderr.trim()}`));
      try {
        resolve(JSON.parse(stdout).structured_output ?? null);
      } catch (e) {
        reject(new Error(`failed to parse claude output: ${e.message}`));
      }
    });
  });
}

module.exports = {
  SINGLE_SCHEMA,
  BATCH_SCHEMA,
  PROMPTS,
  MAX_BODY_CHARS,
  BATCH_CHARS,
  BATCH_MAX,
  body,
  buildPrompt,
  batchByCost,
  buildBatchPrompt,
  describeObject,
  describeAll,
};
