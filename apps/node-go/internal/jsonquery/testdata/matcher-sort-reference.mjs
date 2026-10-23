// Followup evidence only; never rewrites the existing 251 records/review tests.
import { createRequire } from "node:module";
import { readFile, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const require = createRequire(import.meta.url);
const modulePath = process.argv[2];
if (!modulePath) throw new Error("pass the isolated jsonata module path");
const jsonata = require(modulePath);
assert.equal(require(`${modulePath}/package.json`).version,"2.2.2");
const cases = [];
function add(name,expression,expected,classification="reference") {
  cases.push({name,expression,document:{},operator:"==",expected,classification});
}
const first = next => `{'match':'a','start':0,'end':1,'groups':[],'next':function(){${next}}}`;
const second = next => `{'match':'b','start':1,'end':2,'groups':[],'next':function(){${next}}}`;
for (const [name,tail] of [
  ["undefined exhaustion","missing"],
  ["private error","$error('repair-private-marker')"],
  ["nonfinite arithmetic","$boolean(1e308*10)"],
  ["nonfinite aggregate","$type($sum([1e308,1e308]))"],
  ["malformed null","null"], ["malformed false","false"],
  ["malformed zero","0"], ["malformed string","'bad'"],
  ["malformed array","[]"], ["malformed object","{}"],
  ["malformed fields","{'start':'bad','end':'bad','groups':'bad','next':0}"],
  ["missing next","{'match':'b','start':1,'end':2,'groups':[]}"],
  ["noncallable next","{'match':'b','start':1,'end':2,'groups':[],'next':0}"],
]) {
  for (const stage of [1,2]) {
    const matcher = `function($s){${first(stage===1 ? tail : second(tail))}}`;
    add(`matcher ${name} stage${stage} match`,`$count($match('ab',${matcher}))`,stage);
    add(`matcher ${name} stage${stage} split`,`$count($split('ab',${matcher}))`,stage+1);
    add(`matcher ${name} stage${stage} replace`,`$replace('ab',${matcher},'x')`,stage===1 ? "xb" : "xx");
  }
}
add("initial matcher error","$count($match('ab',function($s){$error('repair-private-marker')}))",0);
add("initial malformed matcher","$count($match('ab',function($s){'bad'}))",0);
add("initial undefined matcher","$count($match('ab',function($s){missing}))",0);
add("matcher group data","$join($match('ab',function($s){{'match':'a','start':0,'end':1,'groups':['captured'],'next':function(){missing}}}).groups,',')","captured");
add("matcher aliases","($n:=function(){missing}; $m:=function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$n}}; $count($match('ab',$m)))",1);
add("matcher nested eval","$eval(\"$count($match('ab',function($s){{'match':'a','start':0,'end':1,'groups':[],'next':function(){missing}}}))\")",1);
add("matcher nested eval error","$eval(\"$count($match('ab',function($s){{'match':'a','start':0,'end':1,'groups':[],'next':function(){$error('repair-private-marker')}}}))\")",1);
add("matcher partial continuation contract error","($n:=function($x){$x}; $m:=function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$n(?)}}; $count($match('ab',$m)))",1);
add("matcher raw native continuation contract error","$count($match('a',function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$sum}}))",1);
add("matcher wrapped native continuation","($mk:=function($n){function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$n}}};$count($match('a',$mk($sum))))",1);
add("matcher wrapped partial continuation","($n:=function($x){$x}(?);$mk:=function($f){function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$f}}};$count($match('a',$mk($n))))",1);
add("matcher initial partial argument",`($m:=function($s){${first("missing")}}(?); $count($match('ab',$m)))`,1);
add("matcher closure captures finite","($x:='b'; $m:=function($s){"+first("{'match':$x,'start':1,'end':2,'groups':[],'next':function(){missing}}")+"}; $count($match('ab',$m)))",2);
add("matcher continuation alias error","($n:=function(){$error('repair-private-marker')}; $m:=function($s){{'match':'a','start':0,'end':1,'groups':[],'next':$n}}; $count($match('ab',$m)))",1);
add("matcher later-stage error",`$replace('ab',function($s){${first(second("$error('repair-private-marker')"))}},'x')`,"xx");
add("matcher limit still observes next error",`$count($match('ab',function($s){${first("$error('repair-private-marker')")}},1))`,1);
add("replacement callback error","$replace('ab',/a/,function($m){$error('repair-private-marker')})","xb");
add("replacement callback finite","$replace('ab',/a/,function($m){'x'})","xb");
for (const [name,pattern,text,matchCount,splitCount,replaced] of [
  ["multiple","/a/","aba",2,3,"xbx"],
  ["exhausted","/z/","aba",0,1,"aba"],
  ["whole string","/a+/","aaa",1,2,"x"],
  ["finite zero width at end","/a*/","a",1,2,"x"],
  ["zero width continuation","/a*/","ab",1,2,"xb"],
  ["initial zero width","/a*/","b",1,2,"xb"],
]) {
  add(`regex ${name} match`,`$count($match('${text}',${pattern}))`,matchCount);
  add(`regex ${name} split`,`$count($split('${text}',${pattern}))`,splitCount);
  add(`regex ${name} replace`,`$replace('${text}',${pattern},'x')`,replaced);
}
add("regex nested eval error","$eval(\"$count($match('ab',/a*/))\")",1);
add("regex alias error","($r:=/a*/; $count($split('ab',$r)))",2);
add("regex captures","$join($match('ab',/(a)(b)/).groups,',')","a,b");
add("matcher HOF callback error",`$sum($map(['ab'],function($s){$count($match($s,function($s){${first("$error('repair-private-marker')")}}))}))`,1);

add("sort native contains","$sort(['ab','a'],$contains)[0]","a");
add("sort native contains false UP","$sort(['ab','a'],$contains)[0]","ab");
add("sort native power","$sort([2,1],$power)[0]",1);
add("sort native alias","($f:=$contains; $sort(['ab','a'],$f)[0])","a");
add("sort native partial","$sort([2,1],$power(?,?))[0]",1);
add("sort finite partial sum","$count($sort([[1,2],[3]],$sum(?)))",2);
add("sort partial sum overflow","$count($sort([[1e308,1e308],[1]],$sum(?)))",2,"safe-profile");
add("sort partial overflow alias","($f:=$sum(?); $count($sort([[1e308,1e308],[1]],$f)))",2,"safe-profile");
add("sort partial overflow nested eval","$eval('$count($sort([[1e308,1e308],[1]],$sum(?)))')",2,"safe-profile");
add("sort native power error","$sort([-1,0.5],$power)[0]",-1);
add("sort native wrong arity","$sort([2,1],$number)[0]",2);
add("sort native wrong types","$sort([2,1],$contains)[0]",2);
add("sort lambda error","$sort([2,1],function($a,$b){$error('repair-private-marker')})[0]",2);
add("sort later comparator error","$sort([4,3,2,1],function($a,$b){$a=4 ? $error('repair-private-marker') : $a>$b})[0]",1);
add("sort unsupported number","$sort([2,1],42)[0]",2);
add("sort unsupported object","$sort([2,1],{})[0]",2);
add("sort unsupported null","$sort([2,1],null)[0]",2);
add("sort unsupported false","$sort([2,1],false)[0]",2);
add("sort finite lambda","$sort([3,1,2],function($a,$b){$a>$b})[0]",1);
add("sort two arguments","$sort([2,1],function($a,$b){$b=1})[0]",1);
add("sort regex callable","$sort(['ab','a'],/a/)[0]","a");
add("sort nested eval native","$eval(\"$sort(['ab','a'],$contains)[0]\")","a");
add("sort HOF native","$map([['ab','a']],function($xs){$sort($xs,$contains)[0]})[0]","a");
add("sort default comparator","$sort([3,1,2])[0]",1);
add("sort internal order by","[{'v':3},{'v':1},{'v':2}]^(v)[0].v",1);
add("sort no comparator call needed","$sort([7],function($a,$b){$error('repair-private-marker')})[0]",7);
for (const [name,value,expected] of [
  ["false","false",2],["true","true",1],["zero","0",2],["positive","2",1],
  ["negative","-1",1],["empty text","''",2],["text","'yes'",1],
  ["null","null",2],["undefined","missing",2],["empty array","[]",1],
  ["false array","[false]",1],["empty object","{}",1],["function","function(){false}",1],
]) add(`sort JS truthiness ${name}`,`$sort([2,1],function($a,$b){${value}})[0]`,expected);

const report={referenceUp:0,referenceDown:0,deliberateSafeDown:0};
const requiredUp = new Set([
  "initial undefined matcher", "matcher group data", "matcher aliases", "matcher nested eval",
  "matcher closure captures finite", "replacement callback finite",
  "matcher wrapped native continuation", "matcher wrapped partial continuation",
  "matcher initial partial argument",
  "regex captures", "sort native contains", "sort native power", "sort native alias",
  "sort native partial", "sort finite partial sum", "sort finite lambda", "sort two arguments",
  "sort regex callable", "sort nested eval native", "sort HOF native", "sort default comparator",
  "sort internal order by", "sort no comparator call needed",
]);
for (const f of cases) {
  try {
    const value=await jsonata(f.expression).evaluate(f.document);
    const kind=value===undefined ? "undefined" : value===null ? "null" : Array.isArray(value) ? "array" : typeof value;
    f.reference={kind,...(value!==undefined ? {value}: {})};
    f.reference.state=["string","number","boolean"].includes(kind) && value.toString()===f.expected.toString() ? "up" : "down";
  } catch(error) {
    f.reference={kind:"error",code:String(error.code),state:"down"};
  }
  if(requiredUp.has(f.name) || f.name.startsWith("sort JS truthiness ") || f.name.includes("undefined exhaustion")) {
    assert.equal(f.reference.state,"up",f.name);
  }
  if(f.name.startsWith("matcher malformed ") || f.name.includes("missing next") || f.name.includes("noncallable next")) {
    assert.equal(f.reference.kind,"error",f.name);
  }
  if(f.classification==="safe-profile") {
    assert.equal(f.reference.state,"up",f.name);
    report.deliberateSafeDown++;
  } else if(f.reference.state==="up") report.referenceUp++;
  else report.referenceDown++;
}
const file=new URL('./matcher-sort-fixtures.json',import.meta.url);
if(process.argv.includes('--record')) await writeFile(file,JSON.stringify(cases,null,2)+'\n');
else assert.deepEqual(cases,JSON.parse(await readFile(file,'utf8')));
console.log(`${cases.length} matcher/sort JS reference records ${process.argv.includes('--record') ? 'recorded':'verified'}`);
console.log(JSON.stringify(report));
