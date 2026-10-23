// Run with an absolute path to an isolated jsonata@2.2.2 module; never install
// an npm dependency into the project. See ../README.md for the command.
import { createRequire } from "node:module";
import { readFile, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const require = createRequire(import.meta.url);
const modulePath = process.argv[2];
if (!modulePath) throw new Error("pass the isolated jsonata module path");
const jsonata = require(modulePath);
assert.equal(require(`${modulePath}/package.json`).version, "2.2.2");
const operators = { "==": "$.value = $.expected", "!=": "$.value != $.expected",
  ">": "$number($.value) > $number($.expected)", ">=": "$number($.value) >= $number($.expected)",
  "<": "$number($.value) < $number($.expected)", "<=": "$number($.value) <= $number($.expected)",
  contains: "$contains($.value, $.expected)" };
function describe(value) {
  const kind = value === undefined ? "undefined" : value === null ? "null" : Array.isArray(value) ? "array" :
    (typeof value === "function" || value?._jsonata_function || value?._jsonata_lambda) ? "function" : typeof value;
  return { kind, ...(kind === "number" && (!Number.isFinite(value) || Object.is(value, -0)) ? {number: Object.is(value, -0) ? "-0" : String(value)} :
    value !== undefined && kind !== "function" ? { value } : {}) };
}
let total = 0;
const report = { referenceMatching: 0, failClosed: 0, failClosedSameOutcome: 0, failClosedDivergence: 0, unsupportedLanguage: 0, originalNonfiniteRegressions: 0 };
for (const name of ["fixtures.json", "number-fixtures.json"]) {
 const file = new URL(`./${name}`, import.meta.url);
 const fixtures = JSON.parse(await readFile(file, "utf8"));
 for (const f of fixtures) {
  let reference;
  try {
    const value = await jsonata(f.expression).evaluate(f.document);
    const kind = describe(value).kind;
    const scalar = ["string", "number", "boolean"].includes(kind);
    reference = describe(value);
    if (scalar) {
      reference.state = (await jsonata(operators[f.operator]).evaluate({value:value.toString(), expected:f.expected.toString()})) ? "up" : "down";
    } else reference.state = "down";
  } catch (error) {
    reference = {kind:"error", code: String(error.code), state:"down"};
  }
  if (process.argv.includes("--record")) f.reference = reference;
  else assert.deepEqual(reference, f.reference, f.name);
  // Safety policy is recorded separately from JS language compatibility,
  // including policy rejections where the reference also ends DOWN.
  if (f.policy) {
    assert.equal(f.policy, "fail-closed-nonfinite", f.name);
    assert.equal(f.goState, "down", f.name);
    assert.ok(f.policyReason && !f.gap, f.name);
    report.failClosed++;
    if (reference.state === "down") report.failClosedSameOutcome++;
    else report.failClosedDivergence++;
  } else if (f.goState) {
    assert.equal(f.goState, "down", `known false UP must not be an accepted gap: ${f.name}`);
    assert.ok(f.gap && reference.state === "up", f.name);
    report.unsupportedLanguage++;
  } else report.referenceMatching++;
  if (f.regression) {
    assert.equal(f.regression, "original-downstream-nonfinite", f.name);
    assert.equal(f.policy, "fail-closed-nonfinite", f.name);
    report.originalNonfiniteRegressions++;
  }
  if (f.probe) {
    let probeReference;
    try { probeReference = describe(await jsonata(f.probe).evaluate(f.document)); }
    catch (error) { probeReference = {kind:"error", code:String(error.code)}; }
    if (process.argv.includes("--record")) f.probeReference = probeReference;
    else assert.deepEqual(probeReference, f.probeReference, `${f.name} raw builtin probe`);
  }
 }
 if (process.argv.includes("--record")) await writeFile(file, JSON.stringify(fixtures, null, 2) + "\n");
 total += fixtures.length;
}
console.log(`${total} official JSONata 2.2.2 reference fixtures ${process.argv.includes("--record") ? "recorded" : "verified"}`);
assert.equal(report.originalNonfiniteRegressions, 5);
console.log(JSON.stringify(report));
