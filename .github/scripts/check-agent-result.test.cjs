const assert = require("node:assert/strict");
const { spawnSync } = require("node:child_process");
const { copyFileSync, mkdtempSync, rmdirSync, unlinkSync, writeFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { test } = require("node:test");
const { requireTerminalResult } = require("./check-agent-result.cjs");

const script = join(__dirname, "check-agent-result.cjs");
const encode = (items) => items.map((item) => JSON.stringify(item)).join("\n");

for (const item of [
  { type: "noop", message: "Completed review; no eligible change." },
  { type: "create_pull_request", title: "Fix behavior", body: "Verified fix.", branch: "fix-behavior" },
  { type: "report_incomplete", reason: "Duplicate evidence is blocked.", details: "No edits published." },
]) {
  test(`accepts one ${item.type} result`, () => {
    assert.equal(requireTerminalResult(encode([item])), item.type);
  });
}

test("matches gh-aw's hyphenated type normalization", () => {
  assert.equal(
    requireTerminalResult(encode([{ type: "report-incomplete", reason: "Blocked." }])),
    "report_incomplete",
  );
});

test("allows other safe outputs alongside one terminal result", () => {
  assert.equal(
    requireTerminalResult(encode([{ type: "missing_data", reason: "Unavailable." }, { type: "report_incomplete" }])),
    "report_incomplete",
  );
});

test("accepts blank lines and CRLF output", () => {
  assert.equal(requireTerminalResult('\r\n{"type":"noop"}\r\n\r\n'), "noop");
});

for (const content of ["", " \n\r\n", '{"type":"missing_data"}\n']) {
  test(`rejects output without a terminal result: ${JSON.stringify(content)}`, () => {
    assert.throws(() => requireTerminalResult(content), /exactly one terminal safe output, found 0/);
  });
}

test("rejects the blocked-check prose instead of treating it as success", () => {
  assert.throws(
    () => requireTerminalResult("Unable to publish because the required duplicate check was incomplete."),
    /not valid JSON/,
  );
});

test("rejects contradictory terminal results", () => {
  assert.throws(
    () => requireTerminalResult(encode([{ type: "noop" }, { type: "report_incomplete" }])),
    /exactly one terminal safe output, found 2/,
  );
});

test("rejects repeated terminal calls", () => {
  assert.throws(
    () => requireTerminalResult(encode([{ type: "noop" }, { type: "noop" }])),
    /exactly one terminal safe output, found 2/,
  );
});

for (const item of [null, [], 1, "noop", {}, { type: 1 }]) {
  test(`rejects invalid safe-output records: ${JSON.stringify(item)}`, () => {
    assert.throws(() => requireTerminalResult(encode([item])), /object with a type/);
  });
}

test("does not ignore malformed lines after a valid result", () => {
  assert.throws(() => requireTerminalResult('{"type":"noop"}\ninvalid'), /line 2 is not valid JSON/);
});

test("CLI fails a missing result file with an error annotation", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "agent-result-"));
  t.after(() => rmdirSync(directory));
  const result = spawnSync(process.execPath, [script, join(directory, "missing.jsonl")], { encoding: "utf8" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /::error::.*ENOENT/);
});

test("prepared validation is unaffected by later checkout edits", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "agent-result-"));
  const checkoutScript = join(directory, "checkout.cjs");
  const preparedScript = join(directory, "prepared.cjs");
  const output = join(directory, "safe-outputs.jsonl");
  copyFileSync(script, checkoutScript);
  copyFileSync(checkoutScript, preparedScript);
  writeFileSync(checkoutScript, "process.exit(0);\n");
  writeFileSync(output, "");
  t.after(() => {
    for (const file of [checkoutScript, preparedScript, output]) {
      unlinkSync(file);
    }
    rmdirSync(directory);
  });
  const result = spawnSync(process.execPath, [preparedScript, output], { encoding: "utf8" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /::error::.*found 0/);
});

for (const [name, content, status] of [
  ["empty result", "", 1],
  ["completed review", '{"type":"noop","message":"Reviewed."}\n', 0],
  ["reported blocker", '{"type":"report_incomplete","reason":"Evidence is blocked."}\n', 0],
]) {
  test(`CLI handles ${name}`, (t) => {
    const directory = mkdtempSync(join(tmpdir(), "agent-result-"));
    const file = join(directory, "safe-outputs.jsonl");
    writeFileSync(file, content);
    t.after(() => {
      unlinkSync(file);
      rmdirSync(directory);
    });
    const result = spawnSync(process.execPath, [script, file], { encoding: "utf8" });
    assert.equal(result.status, status, result.stderr);
    if (status === 0) {
      assert.match(result.stdout, /Verified terminal safe output/);
      assert.equal(result.stderr, "");
    } else {
      assert.match(result.stderr, /::error::.*found 0/);
    }
  });
}
