package jsvm

import (
	"context"
	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeHelperVM(t *testing.T, ctx context.Context) *goja.Runtime {
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	var stopped atomic.Bool
	value, err := vm.RunString("(function(validate){const ids=new WeakSet();globalThis.canaryProcedures=ids;globalThis.canaryFinite=function(v){return validate(v,ids)}})")
	if err != nil {
		t.Fatal(err)
	}
	setup, _ := goja.AssertFunction(value)
	if _, err = setup(goja.Undefined(), vm.ToValue(privateValidator(vm, ctx, &stopped))); err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestR3NativeGuardControls(t *testing.T) {
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
	tests = append(tests, []struct{ name, script string }{
		{"proxy plain trap zero", "let traps=0,p=new Proxy({x:Infinity},{ownKeys(){traps++;throw 1},getOwnPropertyDescriptor(){traps++;throw 2},get(){traps++;throw 3}});rejects(p)&&traps===0"},
		{"proxy callable trap zero", "let traps=0,p=new Proxy(function(){},{apply(){traps++;throw 1},get(){traps++;throw 2},ownKeys(){traps++;throw 3}});rejects(p)&&traps===0"},
		{"proxy revoked trap zero", "let traps=0,r=Proxy.revocable({},{get(){traps++;throw 1},ownKeys(){traps++;throw 2}});r.revoke();rejects(r.proxy)&&traps===0"},
		{"proxy nested alias trap zero", "let traps=0,p=new Proxy({},{get(){traps++;throw 1},ownKeys(){traps++;throw 2},getOwnPropertyDescriptor(){traps++;throw 3}});rejects({a:[p,p]})&&traps===0"},
		{"proxy registered rejected before identity", "let traps=0,p=new Proxy({},{get(){traps++;throw 1}});canaryProcedures.add(p);rejects(p)&&traps===0"},
		{"getter cannot reenter", "let calls=0,a={};Object.defineProperty(a,'x',{enumerable:true,get(){calls++;canaryFinite(0);return 0}});rejects(a)&&calls===0"},
		{"failed walk releases active flag", "rejects({x:Infinity});canaryFinite({x:1});true"},
		{"safe metadata builtins", "canaryFinite(new Date());canaryFinite(new Map());canaryFinite(new Set());canaryFinite(new Uint8Array([0,1]));canaryFinite(Object(1));true"},
	}...)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm := nativeHelperVM(t, context.Background())
			v, err := vm.RunString("function rejects(v){try{canaryFinite(v);return false}catch(e){return e.message==='JSON query result rejected by finite profile'}}\n" + tc.script)
			if err != nil || v == nil || !v.ToBoolean() {
				t.Fatalf("native control failed: %v", err)
			}
		})
	}
}

type nativeCancelContext struct {
	context.Context
	checks, at int
}

func (c *nativeCancelContext) Err() error {
	c.checks++
	if c.checks >= c.at {
		return context.Canceled
	}
	return nil
}

func TestR3NativeGuardCancellation(t *testing.T) {
	ctx := &nativeCancelContext{Context: context.Background(), at: 1000}
	vm := nativeHelperVM(t, ctx)
	start := time.Now()
	_, err := vm.RunString("canaryFinite(Array(65535).fill(0))")
	if err == nil || !strings.Contains(err.Error(), privateFiniteError) || ctx.checks != ctx.at || time.Since(start) > time.Second {
		t.Fatal("native traversal cancellation failed")
	}
}

func TestR3PrivateBootstrapWiring(t *testing.T) {
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	var stopped atomic.Bool
	validator := privateValidator(vm, context.Background(), &stopped)
	calls := 0
	factory, err := vm.RunString("(function(canaryPrivateValidator){" + runtimeLibrary + ";return jsonata;})")
	if err != nil {
		t.Fatal(err)
	}
	initialize, _ := goja.AssertFunction(factory)
	_, err = initialize(goja.Undefined(), vm.ToValue(func(c goja.FunctionCall) goja.Value { calls++; return validator(c) }))
	if err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString("(async function(){const result=await jsonata('40+2').evaluate({});return result===42&&typeof globalThis.canaryPrivateValidator==='undefined'&&typeof globalThis.canaryCapturedValidator==='undefined'})()")
	if err != nil {
		t.Fatal(err)
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled || !promise.Result().ToBoolean() || calls == 0 {
		t.Fatal("actual native wiring or private capture failed")
	}
}

func BenchmarkR3NativeGuard65535(b *testing.B) {
	vm := goja.New()
	var stopped atomic.Bool
	validate := privateValidator(vm, context.Background(), &stopped)
	value, err := vm.RunString("({value:Array(65535).fill(0),ids:new WeakSet()})")
	if err != nil {
		b.Fatal(err)
	}
	fixed := value.(*goja.Object)
	call := goja.FunctionCall{Arguments: []goja.Value{fixed.Get("value"), fixed.Get("ids")}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validate(call)
	}
}
