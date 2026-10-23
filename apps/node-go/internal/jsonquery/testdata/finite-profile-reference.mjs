// New profile evidence only. The original 177 records and 34 independent probes
// remain unchanged. Pass the existing isolated jsonata@2.2.2 directory.
import { createRequire } from "node:module";
import { readFile, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const require = createRequire(import.meta.url);
const modulePath = process.argv[2];
if (!modulePath) throw new Error("pass the isolated jsonata module path");
const jsonata = require(modulePath);
assert.equal(require(`${modulePath}/package.json`).version, "2.2.2");
const cases = [];
function add(name, expression, expected, profile = "finite-control", document = {}) {
  cases.push({name, expression, document, operator:"==", expected, profile});
}

add("finite arithmetic", "(1e308 * 0.1) > 0", true);
add("finite builtin", "$sum([1,2,3])", 6);
add("finite predicate", "$sum([1,2,3])[true]", 6);
add("finite group", "($g := [{'k':'a','v':1},{'k':'a','v':2},{'k':'b','v':3}]{k:$sum(v)}; $sum($g.*))", 6);
add("finite nested container", "$sum({'a':[[1,2],{'b':3}]}.a[1].b)", 3);
add("finite input container", "$count($)", 3, "finite-control", [{v:1},{v:2},{v:3}]);
add("builtin alias", "($f := $sum; $f([1,2,3]))", 6);
add("numeric alias", "($f := $number; $f('0x10000000000000000'))", 18446744073709551616);
add("lexical numeric shadow", "($number := function($x){42}; $number('+2'))", 42);
add("lexical sum shadow", "($sum := function($x){42}; $sum([1e308,1e308]))", 42);
add("lexical nested eval shadow", "($number := function($x){42}; $eval(\"$number('+2')\"))", 42);
add("native HOF map", "$sum($map([[1,2],[3,4]],$sum))", 10);
add("native HOF filter", "$count($filter([[1,2],[3,4]],$sum))", 2);
add("native HOF alias", "($f := $sum; $count($filter([[1,2],[3,4]],$f)))", 2);
add("native HOF each", "$sum($each({'a':[1,2],'b':[3,4]},$sum))", 10);
add("lambda HOF", "$sum($map([1,2,3],function($x){$x*2}))", 12);
add("closure capture", "($make := function($x){function($y){$x+$y}}; $f := $make(4); $f(5))", 9);
add("closure alias", "($x := 7; $f := function($y){$x+$y}; $g := $f; $g(2))", 9);
add("partial builtin", "($f := $substring(?,1,2); $f('abcd'))", "bc");
add("partial numeric", "($f := $number(?); $f('2'))", 2);
add("nested eval", "$eval('$sum([1,2,3])')", 6);
add("nested eval numeric", "$eval(\"$number('0x10000000000000000')\")", 18446744073709551616);
add("regex control", "$contains('abc',/b/)", true);
add("null handling control", "$type(null)", "null");
add("undefined handling control", "$exists(missing)", false);
add("container aliases", "($a := {'x':[1,2]}; $b := $a; $sum($b.x))", 3);
add("finite pipeline", "[1,2,3] ~> $sum()", 6);
add("unexecuted nonfinite closure", "($f := function(){1e308*10}; 7)", 7);

add("AST before predicate", "$exists($sum([1e308,1e308])[false])", false, "nonfinite");
add("AST before group", "$exists($sum([1e308,1e308]){'k':1})", true, "nonfinite");
add("native callback before filter consumption", "$count($filter([[1e308,1e308]],$sum))", 1, "nonfinite");
add("native alias callback before filter consumption", "($f := $sum; $count($filter([[1e308,1e308]],$f)))", 1, "nonfinite");
add("native map callback", "$exists($map([[1e308,1e308]],$sum))", true, "nonfinite");
add("native each callback", "$exists($each({'a':[1e308,1e308]},$sum))", true, "nonfinite");
add("lambda callback", "$count($filter([1],function($x){1e308*10}))", 1, "nonfinite");
add("container aggregate", "$exists({'a':[1e308*10]})", true, "nonfinite");
add("nested eval overflow", "$exists($eval('1e308*10'))", true, "nonfinite");
add("numeric partial overflow", "($f := $number(?); $type($f('0x' & $pad('f',300,'f'))))", "number", "nonfinite");
add("visit budget before finite consumption", "$count($)", 65536, "visit-budget", Array(65536).fill(0));
let deep = true;
for (let i=0;i<129;i++) deep = {a:deep};
add("depth budget before finite consumption", "$exists($)", true, "depth-budget", deep);

const report = {finiteControls:0, rejectedReferenceDown:0, deliberateSafeDown:0};
for (const f of cases) {
  try {
    const value = await jsonata(f.expression).evaluate(f.document);
    const kind = value === undefined ? "undefined" : value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
    f.reference = {kind, ...(kind === "number" && !Number.isFinite(value) ? {number:String(value)} : value !== undefined ? {value} : {})};
    f.reference.state = ["string","number","boolean"].includes(kind) && value.toString() === f.expected.toString() ? "up" : "down";
  } catch (error) {
    f.reference = {kind:"error", code:String(error.code), state:"down"};
  }
  if (f.profile === "finite-control") {
    assert.equal(f.reference.state,"up",f.name);
    report.finiteControls++;
  } else if (f.reference.state === "down") report.rejectedReferenceDown++;
  else report.deliberateSafeDown++;
}
const file = new URL("./finite-profile-fixtures.json",import.meta.url);
if (process.argv.includes("--record")) await writeFile(file, JSON.stringify(cases,null,2)+"\n");
else assert.deepEqual(cases,JSON.parse(await readFile(file,"utf8")));
console.log(`${cases.length} finite-profile JS reference records ${process.argv.includes("--record") ? "recorded" : "verified"}`);
console.log(JSON.stringify(report));
