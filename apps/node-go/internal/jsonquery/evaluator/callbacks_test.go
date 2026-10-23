package jsonata

import "testing"

func TestComparatorTruthy(t *testing.T) {
	// Pure conversion only; no expression evaluation, invocation or host binding.
	for _, value := range []interface{}{
		nil, false, "", float64(0), float32(0), int(0), int8(0), int16(0), int32(0), int64(0),
		uint(0), uint8(0), uint16(0), uint32(0), uint64(0), &JSONataNull{}, JSONataNull{},
	} {
		if comparatorTruthy(value) {
			t.Fatalf("false JS conditional value of type %T became true", value)
		}
	}
	called := false
	opaque := profileRepairOpaque{called: &called}
	for _, value := range []interface{}{
		true, "yes", float64(-1), float32(-1), int(-1), int8(-1), int16(-1), int32(-1), int64(-1),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
		[]interface{}{}, []interface{}{false}, &JSONataArray{}, map[string]interface{}{}, opaque,
	} {
		if !comparatorTruthy(value) || called {
			t.Fatal("true JS conditional value rejected or formatted")
		}
	}
}

type profileRepairOpaque struct{ called *bool }

func (v profileRepairOpaque) String() string {
	*v.called = true
	return "repair-private-marker"
}
