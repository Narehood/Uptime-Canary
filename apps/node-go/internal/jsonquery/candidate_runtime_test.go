package jsonquery

import (
	"context"
	"testing"
	"time"
)

func TestCandidateTwoSlotsAndRecovery(t *testing.T) {
	supported(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r := request("$contains($pad('a', 1000000, 'a'), /a{0,1000}b/)", "true")
			r.Timeout = 4 * time.Second
			results <- Check(ctx, r).State
		}()
	}
	time.Sleep(100 * time.Millisecond)
	result := Check(context.Background(), request("true", "true"))
	if result.State != "pending" || result.LatencyMs != 0 {
		t.Errorf("third check did not respect shared admission: %+v", result)
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case state := <-results:
			if state == "up" {
				t.Error("interrupted work succeeded")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not reap")
		}
	}
	if next := Check(context.Background(), request("true", "true")); next.State != "up" {
		t.Fatalf("capacity not restored: %+v", next)
	}
}
