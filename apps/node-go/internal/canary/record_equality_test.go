package canary

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func recordJSONOracle(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestRecordEqualityMatchesJSON(t *testing.T) {
	incident := capacityScopeOriginal("source", "monitor", "generation", 1, 10)
	incident.ResolvedAt = Ptr(int64(20))
	incident.Closed = map[string]int64{"a": 2, "b": 2}
	values := []any{
		attempt{ID: "attempt", MonitorID: "monitor", State: "queued"},
		NotificationIntent{ID: "intent", MonitorID: "monitor", IncidentID: "source"},
		Sample{Observation: Observation{MonitorID: "monitor", State: "up", LatencyMs: 1, LatencyAvailable: Ptr(true)}, DownSince: Ptr(int64(1))},
		incident,
	}
	check := func(t *testing.T, a, b any) {
		t.Helper()
		if got, want := equalJSON(a, b), recordJSONOracle(a, b); got != want {
			t.Fatalf("record comparison differs from JSON for %T and %T: got %v, want %v", a, b, got, want)
		}
	}
	for _, original := range values {
		t.Run(reflect.TypeOf(original).Name(), func(t *testing.T) {
			check(t, original, original)
			check(t, original, nil)
			check(t, original, map[string]any{})
			// A separate decode gives equal pointer values with distinct identities.
			encoded, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			copy := reflect.New(reflect.TypeOf(original))
			if err := json.Unmarshal(encoded, copy.Interface()); err != nil {
				t.Fatal(err)
			}
			check(t, original, copy.Elem().Interface())
			// Exercise every exported field; future fields join this oracle check.
			for index := range copy.Elem().NumField() {
				field := copy.Elem().Type().Field(index)
				if !field.IsExported() {
					continue
				}
				t.Run(field.Name, func(t *testing.T) {
					changed := reflect.New(copy.Elem().Type()).Elem()
					changed.Set(copy.Elem())
					value := changed.Field(index)
					switch value.Kind() {
					case reflect.String:
						value.SetString(value.String() + "changed")
					case reflect.Bool:
						value.SetBool(!value.Bool())
					case reflect.Int, reflect.Int64:
						value.SetInt(value.Int() + 1)
					case reflect.Slice:
						value.Set(reflect.Append(value, reflect.ValueOf("changed")))
					case reflect.Map:
						value.Set(reflect.MakeMap(value.Type()))
					case reflect.Pointer:
						if value.IsNil() {
							value.Set(reflect.New(value.Type().Elem()))
						} else {
							value.SetZero()
						}
					case reflect.Struct:
						value.SetZero()
					default:
						t.Fatalf("add an oracle mutation for %s", value.Kind())
					}
					check(t, original, changed.Interface())
				})
			}
		})
	}
	for _, other := range []Incident{
		{Aliases: []string{}, Predecessors: []string{}, Runs: map[string][]int64{}, DeliveryTargets: []string{}},
		{MaintenanceIDs: []string{}},
		{uncertainties: []incidentUncertainty{{ObserverID: "private"}}},
	} {
		check(t, Incident{}, other)
	}
	for _, latency := range []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1)} {
		for _, other := range []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(-1)} {
			check(t, Sample{Observation: Observation{LatencyMs: latency}}, Sample{Observation: Observation{LatencyMs: other}})
		}
	}
}

func BenchmarkRecordEquality(b *testing.B) {
	incident := capacityScopeOriginal("source", "monitor", "generation", 1, 10)
	sample := Sample{Observation: Observation{MonitorID: "monitor", State: "up", LatencyMs: 1}}
	for name, value := range map[string]any{"incident": incident, "sample": sample, "attempt": attempt{ID: "attempt", State: "queued"}} {
		for method, compare := range map[string]func(any, any) bool{"json": recordJSONOracle, "record": equalJSON} {
			b.Run(name+"/"+method, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if !compare(value, value) {
						b.Fatal("equal records differ")
					}
				}
			})
		}
	}
}
