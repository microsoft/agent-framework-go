const { readFileSync } = require("node:fs");

const terminalTypes = new Set(["create_pull_request", "noop", "report_incomplete"]);

function requireTerminalResult(content) {
  const results = [];
  for (const [index, line] of content.split(/\r?\n/).entries()) {
    if (!line.trim()) {
      continue;
    }

    let item;
    try {
      item = JSON.parse(line);
    } catch {
      throw new Error(`Safe output line ${index + 1} is not valid JSON.`);
    }
    if (
      item === null ||
      typeof item !== "object" ||
      Array.isArray(item) ||
      typeof item.type !== "string"
    ) {
      throw new Error(`Safe output line ${index + 1} must be an object with a type.`);
    }

    const type = item.type.replaceAll("-", "_");
    if (terminalTypes.has(type)) {
      results.push(type);
    }
  }

  if (results.length !== 1) {
    throw new Error(
      `Expected exactly one terminal safe output, found ${results.length}. ` +
        "Call create_pull_request, noop, or report_incomplete; a prose-only explanation is not a result.",
    );
  }
  return results[0];
}

if (require.main === module) {
  try {
    if (process.argv.length !== 3) {
      throw new Error("Usage: node check-agent-result.cjs <safe-outputs.jsonl>");
    }
    const result = requireTerminalResult(readFileSync(process.argv[2], "utf8"));
    console.log(`Verified terminal safe output: ${result}`);
  } catch (error) {
    console.error(`::error::${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  }
}

module.exports = { requireTerminalResult };
