// Offline JS reference for nonfinite producers that do not call $number.
import { createRequire } from "node:module";
import { readFile, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const require = createRequire(import.meta.url);
const modulePath = process.argv[2];
if (!modulePath) throw new Error("pass the isolated jsonata module path");
const jsonata = require(modulePath);
assert.equal(require(`${modulePath}/package.json`).version, "2.2.2");
const file = new URL("./independent-nonfinite-probes.json", import.meta.url);
const cases = [];
for (const [name, expression, text, comparison] of [
  ["arithmetic overflow", "1e308 * 10", "Infinity", "> 0"],
  ["negative arithmetic overflow", "-1e308 * 10", "-Infinity", "< 0"],
  ["power overflow", "$power(10,999)", "Infinity", "> 0"],
  ["sum overflow", "$sum([1e308,1e308])", "Infinity", "> 0"],
  ["sum NaN", "$sum([$sum([1e308,1e308]),$sum([-1e308,-1e308])])", "NaN", "!= $n"],
]) {
  for (const [consumer, query, expected] of [
    ["type", `$type((${expression}))`, "number"],
    ["boolean", `$boolean((${expression}))`, true],
    ["string", `$string((${expression}))`, text],
    ["exists", `$exists((${expression}))`, true],
    ["comparison", `($n := (${expression}); $n ${comparison})`, true],
    ["discarded", `((${expression}); 'ok')`, "ok"],
  ]) cases.push({name:`${name}: ${consumer}`, expression:query, document:{}, operator:"==", expected});
}
cases.push(
  {name:"finite arithmetic control", expression:"$type(1e308 * 0.1)", document:{}, operator:"==", expected:"number", control:true},
  {name:"finite sum control", expression:"$sum([1,2,3])", document:{}, operator:"==", expected:6, control:true},
  {name:"lexical shadow control", expression:"($number := function($x){42}; $number('+2'))", document:{}, operator:"==", expected:42, control:true},
  {name:"nested finite eval control", expression:"$eval('$number(2)')", document:{}, operator:"==", expected:2, control:true},
);
for (const f of cases) {
  try {
    const value = await jsonata(f.expression).evaluate(f.document);
    const kind = value === undefined ? "undefined" : value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
    f.reference = {kind, ...(kind === "number" && !Number.isFinite(value) ? {number:String(value)} : value !== undefined ? {value} : {})};
    f.reference.state = ["string", "number", "boolean"].includes(kind) && value.toString() === f.expected.toString() ? "up" : "down";
  } catch (error) {
    f.reference = {kind:"error", code:String(error.code), state:"down"};
  }
}
if (process.argv.includes("--record")) await writeFile(file, JSON.stringify(cases, null, 2)+"\n");
else assert.deepEqual(cases, JSON.parse(await readFile(file,"utf8")));
console.log(`${cases.length} independent nonfinite JS reference probes ${process.argv.includes("--record") ? "recorded" : "verified"}`);
