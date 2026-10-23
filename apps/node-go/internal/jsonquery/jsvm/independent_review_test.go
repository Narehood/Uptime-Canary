package jsvm

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// Extract the actual embedded guard, never a manually maintained duplicate.
// Only fixed JS graph/helper programs execute here; no JSONata expressions,
// host objects, host functions, or user text are compiled in this process.
func TestIndependentCandidateActualGuard(t *testing.T) {
	start := strings.Index(library, "    // Canary private finite-intermediate profile.")
	end := strings.Index(library, "    var staticFrame = createFrame(null);")
	if start < 0 || end <= start {
		t.Fatal("actual guard extraction boundary missing")
	}
	guard := library[start:end]
	tests := []struct{ name, script string }{
		{"finite primitive roots", `canaryFinite(0);canaryFinite(-0);canaryFinite('x');canaryFinite(null);canaryFinite(undefined);canaryFinite(true);true`},
		{"all nonfinite primitive roots", `[NaN,Infinity,-Infinity].every(v=>rejects(v))`},
		{"nested nonfinite objects", `rejects({a:[{x:Infinity}]})&&rejects([{x:NaN}])`},
		{"array exact visits", `canaryFinite(Array(65535).fill(0));true`},
		{"array excessive visits", `rejects(Array(65536).fill(0))`},
		{"sparse array exact visits", `canaryFinite(Array(65535));true`},
		{"sparse array excessive visits", `rejects(Array(65536))`},
		{"object exact visits", `let a={};for(let i=0;i<65535;i++)a[i]=0;canaryFinite(a);true`},
		{"object excessive visits", `let a={};for(let i=0;i<65536;i++)a[i]=0;rejects(a)`},
		{"mixed exact depth", `let a=0;for(let i=0;i<128;i++)a=i%2?[a]:{v:a};canaryFinite(a);true`},
		{"mixed excessive depth", `let a=0;for(let i=0;i<129;i++)a=i%2?[a]:{v:a};rejects(a)`},
		{"alias exact aggregate", `let a=Array(32766).fill(0),b=Array(32767).fill(0);canaryFinite([a,b]);true`},
		{"alias excessive aggregate", `let a=Array(32767).fill(0);rejects([a,a])`},
		{"object repeated references count again", `let a=Array(10000).fill(0);canaryFinite({a,b:a,c:a,d:a,e:a,f:a});rejects({a,b:a,c:a,d:a,e:a,f:a,g:a})`},
		{"mixed cycle", `let a={},b=[a];a.b=b;rejects(a)`},
		{"self array cycle", `let a=[];a[0]=a;rejects(a)`},
		{"fresh calls and changed aliases", `let a={v:0};canaryFinite([a,a]);a.v=Infinity;let rejected=rejects([a,a]);a.v=1;canaryFinite([a,a]);rejected`},
		{"enumerable object getter rejected without call", `let reads=0,a={};Object.defineProperty(a,'v',{enumerable:true,get(){reads++;throw 9}});rejects(a)&&reads===0`},
		{"indexed array getter rejected without call", `let reads=0,a=[0];Object.defineProperty(a,0,{get(){reads++;throw 9}});rejects(a)&&reads===0`},
		{"setter descriptor rejected", `let a={};Object.defineProperty(a,'v',{enumerable:true,set(x){throw 9}});rejects(a)`},
		{"inherited getter ignored", `let reads=0,p={};Object.defineProperty(p,'v',{enumerable:true,get(){reads++;throw 9}});canaryFinite(Object.create(p));reads===0`},
		{"no toString invocation", `let reads=0,a={toString(){reads++;throw 9}};canaryFinite(a);reads===0`},
		{"native JS function opaque", `let reads=0,f=function(){throw 9};Object.defineProperty(f,'v',{enumerable:true,get(){reads++;throw 9}});canaryFinite(f);reads===0`},
		{"registered object opaque", `let reads=0,a={};Object.defineProperty(a,'environment',{enumerable:true,get(){reads++;throw 9}});canaryProcedures.add(a);canaryFinite(a);reads===0`},
		{"unregistered lambda marker not authority", `rejects({_jsonata_lambda:true,v:Infinity})`},
		{"unregistered native marker not authority", `rejects({_jsonata_function:true,v:NaN})`},
		{"copied registered header loses exemption", `let a={v:Infinity};canaryProcedures.add(a);canaryFinite(a);rejects(Object.assign({},a))`},
		{"array sequence metadata ignored", `let a=[1];a.sequence=Infinity;a.tupleStream=NaN;canaryFinite(a);true`},
		{"fixed error text", `let message;try{canaryFinite({privateMarker:Infinity})}catch(e){message=e.message}message==='JSON query result rejected by finite profile'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm := goja.New()
			vm.SetParserOptions(parser.WithDisableSourceMaps)
			v, err := vm.RunString(guard + `
function rejects(v){try{canaryFinite(v);return false}catch(e){return e.message==='JSON query result rejected by finite profile'}}
` + tc.script)
			if err != nil || v == nil || !v.ToBoolean() {
				t.Fatalf("actual guard control failed: %v", err)
			}
		})
	}
}

func TestIndependentCandidateFixedEngineJSON(t *testing.T) {
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	v, err := vm.RunString(`(function(){try{return JSON.stringify({value:JSON.parse('1e400')})}catch(e){return e.name}})()`)
	if err != nil || v == nil {
		t.Fatal("fixed engine diagnostic failed")
	}
	t.Logf("JSON.parse overflow observation: %s", v.String())
}
