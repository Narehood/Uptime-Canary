package checks_test

import (
	"os"
	"testing"

	"uptime-canary/apps/node-go/internal/probeworker"
)

func TestMain(m *testing.M) {
	if handled, code := probeworker.Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
