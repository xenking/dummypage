/* jshint esversion: 6, node: true */

const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const coursesCSS = readFileSync(
  path.resolve(__dirname, "../static/css/courses.css"),
  "utf8",
);

test("course result titles keep readable leading at every breakpoint", () => {
  const rules = [...coursesCSS.matchAll(/\.result-title\s*\{([^}]+)\}/g)];

  assert.ok(rules.length > 0, "expected at least one .result-title rule");

  for (const [, declarations] of rules) {
    const match = declarations.match(/line-height:\s*([0-9.]+)/);
    if (!match) continue;

    const lineHeight = Number(match[1]);
    assert.ok(
      lineHeight >= 1.3,
      `.result-title line-height ${lineHeight} is below the readable 1.3 minimum`,
    );
  }
});
