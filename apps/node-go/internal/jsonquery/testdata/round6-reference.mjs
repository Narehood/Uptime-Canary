// Only new R6 records are writable. All earlier 408 records and review files
// remain immutable. Use the existing isolated official jsonata@2.2.2 package.
import {createRequire} from 'node:module';
import {readFile,writeFile} from 'node:fs/promises';
import assert from 'node:assert/strict';
const require=createRequire(import.meta.url),modulePath=process.argv[2];
if(!modulePath) throw new Error('pass the isolated jsonata module path');
const jsonata=require(modulePath);
assert.equal(require(`${modulePath}/package.json`).version,'2.2.2');
const cases=[];
function add(name,expression,expected,mode='up',document={}) {
  cases.push({name,expression,document,operator:'==',expected,mode});
}
const first=tail=>`{'match':'a','start':0,'end':1,'groups':[],'next':function(){${tail}}}`;
const second=tail=>`{'match':'b','start':1,'end':2,'groups':[],'next':function(){${tail}}}`;
for(const limit of [0,0.25,0.5,1,1.5,2.25,-0.25,-1.5]) {
  const matcher=`function($s){${first(second('missing'))}}`;
  const count=limit<=0?0:Math.min(2,Math.ceil(limit));
  const parts=limit<=0?0:count<limit?count+1:count;
  const mode=limit<0?'error':'up';
  add(`match numeric limit ${limit}`,`$count($match('ab',${matcher},${limit}))`,count,mode);
  add(`split numeric limit ${limit}`,`$count($split('ab',${matcher},${limit}))`,parts,mode);
  add(`replace numeric limit ${limit}`,`$replace('ab',${matcher},'x',${limit})`,count===0?'ab':count===1?'xb':'xx',mode);
}
for(const consumer of ['match','split','replace']) {
  for(const [stage,tail,limit] of [[1,"$error('r6-private-marker')",0.25],[2,second("$error('r6-private-marker')"),1.5]]) {
    const matcher=`function($s){${first(tail)}}`;
    const query=consumer==='replace'?`$replace('ab',${matcher},'x',${limit})`:`$count($${consumer}('ab',${matcher},${limit}))`;
    add(`${consumer} fractional error stage${stage}`,query,consumer==='replace'?'ab':0,'error');
  }
  const call=consumer==='replace'?`$replace(missing,function($s){$error('r6-private-marker')},'x',-0.5)`:`$${consumer}(missing,function($s){$error('r6-private-marker')},-0.5)`;
  add(`${consumer} undefined bypasses callback`,`$exists(${call})`,false);
}
add('literal split fractional ToUint32',"$count($split('a,b',',',0.5))",0);
add('literal split wrap ToUint32',"$count($split('a,b',',',4294967297))",1);
add('literal replace fractional',"$replace('aa','a','x',0.5)",'xa');
add('empty match valid exhaustion',"$count($match('',function($s){missing}))",0);
add('empty split valid exhaustion',"$count($split('',function($s){missing}))",1);
add('empty replace valid exhaustion',"$replace('',function($s){missing},'x')",'');
add('empty contains string',"$contains('','')",true);
add('empty contains callback error',"$contains('',function($s){$error('r6-private-marker')})",false,'error');

for(const property of ['start','end','next','custom']) {
  const matcher=property==='custom'?`function($s){{'match':'a','start':0,'end':1,'groups':[],'custom':7,'next':function(){missing}}}`:'/a/';
  add(`replacement ${property} retained`,`$replace('ab',${matcher},function($m){$exists($m.${property})?'yes':'no'})`,'yesb');
}
add('replacement no invented index',"$replace('ab',/a/,function($m){$exists($m.index)?'bad':'ok'})",'okb');
add('replacement callable next',"$replace('aba',/a/,function($m){$exists($m.next())?'more':'last'})",'moreba');
add('replacement next is function',"$replace('ab',/a/,function($m){$type($m.next)})",'functionb');
add('replacement next failure propagates',`$replace('ab',function($s){${first("$error('r6-private-marker')")}},function($m){$m.next();'x'})`,'xb','error');
add('replacement original custom group',"$replace('ab',function($s){{'match':'a','start':0,'end':1,'groups':[7],'custom':'value','next':function(){missing}}},function($m){$m.custom & ':' & $m.groups[0]})",'value:7b');
add('custom fractional index value',"$match('ab',function($s){{'match':'a','start':0.5,'end':1.5,'groups':[],'next':function(){missing}}}).index",0.5);
add('custom numeric group value',"$match('ab',function($s){{'match':'a','start':0,'end':1,'groups':[7],'next':function(){missing}}}).groups[0]",7);
add('custom numeric group replacement',"$replace('ab',function($s){{'match':'a','start':0,'end':1,'groups':[7],'next':function(){missing}}},'$1')",'7b');
add('custom null group slot count',"$count($match('ab',function($s){{'match':'a','start':0,'end':1,'groups':[null,7],'next':function(){missing}}}).groups)",2);
add('optional capture positional count',"$count($match('b',/(a)?(b)(c)?/).groups)",3);
add('optional capture second position',"$match('b',/(a)?(b)(c)?/).groups[1]",'b');
add('optional capture first undefined',"$exists($match('b',/(a)?(b)(c)?/).groups[0])",false);
add('optional capture third undefined',"$exists($match('b',/(a)?(b)(c)?/).groups[2])",false);
add('optional capture replacement slots',"$replace('b',/(a)?(b)(c)?/,'$1:$2:$3')",':b:');

for(const fn of ['$sum(?)','$power(?,?)','$contains(?,?)']) {
  const input=fn.includes('contains')?'[2,1]':"['b','a']";
  for(const expected of fn.includes('contains')?[1,2]:['a','b'])
    add(`invalid partial ${fn} paired ${expected}`,`$sort(${input},${fn})[0]`,expected,'error');
}
add('partial power numeric text',"$sort(['2','1'],$power(?,?))[0]",'1');
add('partial power leading plus',"$sort(['+2','1'],$power(?,?))[0]",'1');
add('partial power decimal dot',"$sort(['.5','1'],$power(?,?))[0]",'1');
add('partial power hex',"$sort(['0x2','1'],$power(?,?))[0]",'1');
add('partial power underscore is invalid',"$sort(['0x1_0','1'],$power(?,?))[0]",'1','error');
add('partial power whitespace',"$sort([' 2 ','1'],$power(?,?))[0]",'1');
add('partial power undefined first',"($f:=$power(?,?);$exists($f(missing,2)))",false);
add('partial sum undefined',"($f:=$sum(?);$exists($f(missing)))",false);
add('partial sum valid mixed addition',"($f:=$sum(?);$string($f([1,'2'])))",'12');
add('partial sum numeric scalar invalid',"$count($sort([2,1],$sum(?)))",2,'error');
add('partial sum repeated partial',"($f:=$sum(?);$g:=$f(?);$g([1,2]))",3);
add('partial power repeated partial',"($f:=$power(?,?);$g:=$f(?,2);$g(3))",9);
add('native eval lexical context',"($x:=7;$sort(['false','true'],$eval)[0])",'false');
add('partial eval lexical context',"($x:=7;$f:=$eval(?,?);$f('$x',{}))",7);
add('partial eval overridden input',"($f:=$eval(?,?);$f('x',{'x':7}))",7);
add('partial eval has no generated local',"($f:=$eval(?,?);$exists($f('$__native_partial_0',{})))",false);
add('partial eval original expression local',"($f:=$eval(?,?);$f('$expr',{}))",'$expr');
add('partial eval original focus local',"($f:=$eval(?,?);$f('$focus.x',{'x':7}))",7);
add('partial eval bound focus local',"($f:=$eval(?,{'x':7});$f('$focus.x'))",7);
add('partial eval extra argument has no generated local',"($f:=$eval(?,?,?);$f('$exists($__native_partial_2)',{},7))",false);
add('native eval comparator',"$sort(['false','true'],$eval)[0]",'false');
add('partial eval comparator',"$sort(['false','true'],$eval(?,?))[0]",'false');
add('partial eval alias property',"($f:=$eval(?,?);$o:={'f':$f};$sort(['false','true'],$o.f)[0])",'false');
add('partial native undefined continuation',"($mk:=function($f){function($s){"+first('missing').replace("function(){missing}","$f")+"}};$count($match('ab',$mk($sum(?)))))",1);
add('partial numeric shim remains scoped',"($f:=$number(?);$f('0x10000000000000000'))",18446744073709551616);
add('partial lexical override',"($sum:=function($x){42};$f:=$sum(?);$f('bad'))",42);
add('partial overflow guarded',"$count($sort([[1e308,1e308],[1]],$sum(?)))",2,'safe-profile');
add('partial overflow alias guarded',"($f:=$sum(?);$count($sort([[1e308,1e308],[1]],$f)))",2,'safe-profile');
for(const [name,value,expected] of [['ASCII','canary',6],['multibyte','é漢🙂',3],['combining','e\u0301',2],['empty','',0]]) {
  add(`length ${name}`,'$length($)',expected,'up',value);
}

const summary={referenceUp:0,referenceDown:0,deliberateSafeDown:0};
for(const f of cases) {
  try {
    const value=await jsonata(f.expression).evaluate(f.document);
    const kind=value===undefined?'undefined':value===null?'null':Array.isArray(value)?'array':typeof value;
    f.reference={kind,...(value===undefined?{}:{value})};
    f.reference.state=['string','number','boolean'].includes(kind)&&value.toString()===f.expected.toString()?'up':'down';
  } catch(error) {f.reference={kind:'error',code:String(error.code),state:'down'};}
  if(f.mode==='up') {assert.equal(f.reference.state,'up',f.name);summary.referenceUp++;}
  else if(f.mode==='error') {assert.equal(f.reference.kind,'error',f.name);summary.referenceDown++;}
  else if(f.mode==='safe-profile') {assert.equal(f.reference.state,'up',f.name);summary.deliberateSafeDown++;}
  else throw new Error('unclassified fixture');
}
const file=new URL('./round6-fixtures.json',import.meta.url);
if(process.argv.includes('--record')) await writeFile(file,JSON.stringify(cases,null,2)+'\n');
else assert.deepEqual(cases,JSON.parse(await readFile(file,'utf8')));
console.log(`${cases.length} R6 official JS reference records ${process.argv.includes('--record')?'recorded':'verified'}`);
console.log(JSON.stringify(summary));

// Verify the independent Go-embedded ROUND2 records directly, without edits.
const review=await readFile(new URL('../matcher_sort_review_test.go',import.meta.url),'utf8');
const records=JSON.parse(review.match(/const matcherSortReviewJSON = `([\s\S]*?)`/)[1]);
for(const f of records) {
  let result;
  try {
    const value=await jsonata(f.expression).evaluate(f.document);
    const kind=value===undefined?'undefined':value===null?'null':Array.isArray(value)?'array':typeof value;
    result={kind,...(value===undefined?{}:{value}),state:['string','number','boolean'].includes(kind)&&value.toString()===f.expected.toString()?'up':'down'};
  } catch(error) {result={kind:'error',code:String(error.code),state:'down'};}
  assert.deepEqual(result,f.reference,f.name);
}
console.log(`${records.length} unchanged independent ROUND2 JS records verified`);
