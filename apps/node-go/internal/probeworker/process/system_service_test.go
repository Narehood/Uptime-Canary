package process

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestSystemServiceFramesPreserveOnlyFixedStateSummaries(t *testing.T) {
	for _, result := range []Result{
		{State: "up", Message: "Service is running"},
		{State: "down", Message: "Service is not running"},
		{State: "down", Message: "Service is not installed"},
		{State: "pending", Message: "Service status unavailable"},
	} {
		if !validTypedResult("system-service", result, time.Second) || safeMessage("system-service", result) != result.Message {
			t.Fatal("valid fixed manager result refused", result)
		}
		raw, err := json.Marshal(frame{Kind: "result", Result: &result})
		if err != nil {
			t.Fatal(err)
		}
		sink := streamSink{started: true, serviceType: "system-service", timeout: time.Second}
		if !sink.accept(raw) {
			t.Fatal("parent refused a valid typed frame", result)
		}
	}
	for _, result := range []Result{
		{State: "pending", Message: "Service is running"},
		{State: "up", Message: "Service status unavailable"},
		{State: "down", Message: "service check failed"},
		{State: "pending", Message: "private manager path or diagnostic"},
		{State: "unknown", Message: "Service status unavailable"},
		{State: "up", Message: "Service is running", LatencyMs: -1},
		{State: "pending", Message: "Service status unavailable", LatencyMs: math.NaN()},
		{State: "pending", Message: "Service status unavailable", LatencyMs: math.Inf(1)},
		{State: "pending", Message: "Service status unavailable", LatencyMs: 1001},
	} {
		if validTypedResult("system-service", result, time.Second) {
			t.Fatal("untrusted manager authority or scalar accepted", result)
		}
	}
	for _, kind := range []string{"oracledb", "jsonata", "redis", "", "System-service"} {
		if validTypedResult(kind, Result{State: "pending", Message: "Service status unavailable"}, time.Second) {
			t.Fatal("another engine gained Pending frame authority", kind)
		}
	}
}
