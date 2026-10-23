package jsvm

import (
	"fmt"
	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"strings"
	"testing"
)

func numberSource(source string) string {
	start := strings.Index(source, "    function number(arg) {")
	end := strings.Index(source[start:], "    function abs(arg) {") + start
	return source[start:end]
}

func TestR3NumberSingleConversionParity(t *testing.T) {
	// Reconstruct the exact R2 implementation from the one changed branch.
	current := numberSource(library)
	previous := strings.Replace(current,
		"Number.isFinite(result = parseFloat(arg))",
		"!isNaN(parseFloat(arg)) && isFinite(arg)", 1)
	previous = strings.Replace(previous,
		"// Primitive string validated by the same grammar: reuse its one conversion.",
		"result = parseFloat(arg);", 1)
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	makeCall := func(source string) goja.Callable {
		v, e := vm.RunString("(function(){" + source + "return function(x){try{const v=number(x);return JSON.stringify({ok:true,defined:typeof v!=='undefined',value:v,nan:Number.isNaN(v),negativeZero:Object.is(v,-0)})}catch(e){return JSON.stringify({ok:false,code:e.code,message:e.message})}}})()")
		if e != nil {
			t.Fatal(e)
		}
		call, _ := goja.AssertFunction(v)
		return call
	}
	old, newer := makeCall(previous), makeCall(current)
	cases := []string{"", "0", "-0", "-0.0", "0e+10", "00", "-00", "0.0", "1e2", "1E-2", "+2", " 2", "2 ", ".2", "1.", "1e999", "1e-999", "-1e-999", "5e-324", "2.4703282292062327e-324", "2.4703282292062328e-324", "1.7976931348623157e308", "1.7976931348623159e308", "0x10", "0x10000000000000000", "0x10z", "0b11", "0o17", "-0x10", "NaN", "Infinity", "é", "١", "１", "1\u0000", "1\n"}
	for i := -250; i <= 250; i++ {
		cases = append(cases, fmt.Sprintf("%de%d", i, i%330), fmt.Sprintf("%d.%03d", i, (i*i)%1000))
	}
	cases = append(cases, strings.Repeat("0", 1048576), strings.Repeat("0", 1048577), strings.Repeat("é", 524288), strings.Repeat("é", 524289))
	for i, arg := range cases {
		a, e := old(goja.Undefined(), vm.ToValue(arg))
		if e != nil {
			t.Fatal(e)
		}
		b, e := newer(goja.Undefined(), vm.ToValue(arg))
		if e != nil {
			t.Fatal(e)
		}
		if a.String() != b.String() {
			t.Fatalf("case %d differs, len=%d", i, len(arg))
		}
	}
	// Official unmodified source parity covers its valid grammar/error branch;
	// known pre-existing wide-base Goja Number repair is checked against R2.
	official := makeCall(numberSource(resourceOfficialJSONata))
	for i, arg := range cases {
		// C1 already replaces the official base-integer Number branch with
		// Number(BigInt(...)); raw Goja parity is not valid for that branch.
		if len(arg) > 10000 || strings.HasPrefix(arg, "0x") || strings.HasPrefix(arg, "0b") || strings.HasPrefix(arg, "0o") {
			continue
		}
		a, e := official(goja.Undefined(), vm.ToValue(arg))
		if e != nil {
			t.Fatal(e)
		}
		b, e := newer(goja.Undefined(), vm.ToValue(arg))
		if e != nil {
			t.Fatal(e)
		}
		if a.String() != b.String() {
			t.Fatalf("official case %d differs", i)
		}
	}
	t.Logf("%d R2 parity cases including cap/multibyte boundaries; official small-string parity separately", len(cases))
}
