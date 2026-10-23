package jsvm

import (
	_ "embed"
	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"testing"
)

// Unmodified official 2.2.2, retained only as a private test oracle.
//
//go:embed resource-official-jsonata.js
var resourceOfficialJSONata string

// Fixed cases execute in isolated JS runtimes without host objects/functions.
// This is parity evidence, not a worker containment test.
func TestResourcePadOfficialParity(t *testing.T) {
	script := `(async function(){
        const chars=["'0'","'ab'","'😀'","'a😀'","''","undefined","null","0","false","['x']","['x','y']","{}"];
        const widths=[-7.9,-7,-2,-1,-0.5,0,0.5,1,2,7,7.9];
        const inputs=["'x'","'😀'","'a😀'","''"];
        const out=[];
        async function check(expression){
            try { const value=await jsonata(expression).evaluate({});
                out.push({ok:true,defined:typeof value!=='undefined',value:value});
            } catch(e) { out.push({ok:false,code:e.code,message:e.message}); }
        }
        for(const str of inputs)for(const width of widths)for(const char of chars)
            await check("$pad("+str+","+width+","+char+")");
        for(const expression of [
            "$pad(missing,7,'x')",
            "($p:=$pad(?,7,'ab');$p('x'))",
            "($p:=$pad(?,?,?);$p('😀',-7,'a😀'))",
            "($pad:=function($x,$n,$c){42};$pad('x',7,'0'))",
            "$pad('x',3,'\\ud800')",
            "$pad('x',3,'\\udc00')",
            "$pad('x',4294967296,'0')"
        ]) await check(expression);
        return JSON.stringify(out);
    })()`
	evaluate := func(source string) string {
		vm := goja.New()
		vm.SetParserOptions(parser.WithDisableSourceMaps)
		if _, err := vm.RunString(source); err != nil {
			t.Fatal(err)
		}
		value, err := vm.RunString(script)
		if err != nil {
			t.Fatal(err)
		}
		promise, ok := value.Export().(*goja.Promise)
		if !ok || promise.State() != goja.PromiseStateFulfilled {
			t.Fatal("parity oracle did not fulfill")
		}
		return promise.Result().String()
	}
	expected := evaluate(resourceOfficialJSONata)
	actual := evaluate(library)
	if actual != expected {
		t.Fatal("pad parity differs from unmodified official JSONata")
	}
	t.Log("535 fixed pad cases match official values and structured errors")
}
