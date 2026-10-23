package jsvm

import (
	_ "embed"
	"testing"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// These tests compile fixed trusted JS helper programs, never JSONata/user text.
// Actual DSL compilation/evaluation is covered only by the leaf's self-exec tests.
//
//go:embed guard-fixture.js
var guardFixture string

func TestPrivateGuardSemantics(t *testing.T) {
	cases := map[string]string{
		"root budgets":               `canaryFinite(Array(65535).fill(0)); let failed=false; try {canaryFinite(Array(65536).fill(0))} catch (_) {failed=true} failed`,
		"depth":                      `let a=0;for(let i=0;i<128;i++)a=[a];canaryFinite(a);let failed=false;try{canaryFinite([a])}catch(_){failed=true}failed`,
		"cycle":                      `let a={};a.a=a;let failed=false;try{canaryFinite(a)}catch(_){failed=true}failed`,
		"aliases":                    `let a=Array(32767).fill(0);let failed=false;try{canaryFinite([a,a])}catch(_){failed=true}failed`,
		"forged marker":              `let failed=false;try{canaryFinite({_jsonata_lambda:true,x:Infinity})}catch(_){failed=true}failed`,
		"no getters":                 `let reads=0;let a={};Object.defineProperty(a,'x',{enumerable:true,get(){reads++;return 1}});let failed=false;try{canaryFinite(a)}catch(_){failed=true}failed&&reads===0`,
		"opaque registered identity": `let reads=0;let a={};Object.defineProperty(a,'environment',{enumerable:true,get(){reads++;throw 1}});canaryProcedures.add(a);canaryFinite(a);canaryFinite(function(){throw 1});reads===0`,
		"fresh budget and mutation":  `let a={x:0};canaryFinite([a,a]);a.x=Infinity;let failed=false;try{canaryFinite([a,a])}catch(_){failed=true}failed`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			vm := goja.New()
			vm.SetParserOptions(parser.WithDisableSourceMaps)
			v, err := vm.RunString(guardFixture + "\n" + script)
			if err != nil || v == nil || !v.ToBoolean() {
				t.Fatalf("fixed guard control failed: %v", err)
			}
		})
	}
}

func TestFixedEngineNumbers(t *testing.T) {
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	v, err := vm.RunString(`JSON.stringify([Number('0x10000000000000000'),18446744073709552000,parseInt('10000000000000000',16),Number(BigInt('0x10000000000000000'))])`)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(v.String())
}
