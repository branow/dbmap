"use strict";
// The parts of this plugin that are wired by string rather than by import: the
// hook's command line, the scripts the skills are allowed to run, and the skill
// names the other skills point at. A rename that misses one of these breaks at
// session start, in a place no unit test looks.

const { test } = require("node:test");
const assert = require("node:assert");
const { readFileSync, readdirSync, existsSync } = require("node:fs");
const { join } = require("node:path");

const PLUGIN = join(__dirname, "..", "..");
const SKILLS = join(PLUGIN, "skills");

function skills() {
  return readdirSync(SKILLS, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => ({
      dir: entry.name,
      text: readFileSync(join(SKILLS, entry.name, "SKILL.md"), "utf8"),
    }));
}

// ${CLAUDE_PLUGIN_ROOT} is substituted by the harness, so a path that is wrong
// here fails as a denied Bash call in front of the user.
function scriptsReferencedIn(text) {
  return [...text.matchAll(/\$\{CLAUDE_PLUGIN_ROOT\}\/(\S+?\.js)/g)].map((m) => m[1]);
}

test("every skill directory holds a SKILL.md whose name matches it", () => {
  for (const skill of skills()) {
    const name = /^name:\s*(\S+)/m.exec(skill.text);
    assert.ok(name, `${skill.dir}/SKILL.md declares no name`);
    assert.equal(name[1], skill.dir);
  }
});

test("every script a skill may run exists", () => {
  for (const skill of skills()) {
    for (const script of scriptsReferencedIn(skill.text)) {
      assert.ok(existsSync(join(PLUGIN, script)), `${skill.dir} points at a missing ${script}`);
    }
  }
});

test("the session-start hook runs a script that is there", () => {
  const hooks = readFileSync(join(PLUGIN, "hooks", "hooks.json"), "utf8");
  const scripts = scriptsReferencedIn(hooks);

  assert.ok(scripts.length > 0, "hooks.json runs no plugin script");
  for (const script of scripts) {
    assert.ok(existsSync(join(PLUGIN, script)), `hooks.json points at a missing ${script}`);
  }
});

// A skill that offers another by name is a dead end if that name is gone.
test("every slash command a skill offers is a skill of this plugin", () => {
  const names = new Set(skills().map((skill) => skill.dir));
  for (const skill of skills()) {
    for (const [, offered] of skill.text.matchAll(/\/(dbmap[a-z-]*)/g)) {
      assert.ok(names.has(offered), `${skill.dir} offers /${offered}, which does not exist`);
    }
  }
});

// The marketplace entry is how anyone installs this at all, and its version and
// the plugin's own are read from two different files.
test("the marketplace and the plugin agree on name and version", () => {
  const marketplace = JSON.parse(readFileSync(join(PLUGIN, "..", "..", ".claude-plugin", "marketplace.json"), "utf8"));
  const plugin = JSON.parse(readFileSync(join(PLUGIN, ".claude-plugin", "plugin.json"), "utf8"));
  const entry = marketplace.plugins.find((p) => p.name === plugin.name);

  assert.ok(entry, `the marketplace lists no plugin named ${plugin.name}`);
  assert.equal(entry.version, plugin.version);
  assert.equal(entry.source, "./plugins/dbmap");
});
