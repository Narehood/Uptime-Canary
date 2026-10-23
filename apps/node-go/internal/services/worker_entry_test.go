package services_test

import (
	"os"
	"testing"

	"uptime-canary/apps/node-go/internal/probeworker"
)

// External TestMain imports the worker adapter without an import cycle.
// Internal engine tests keep calling services.Check unchanged.
func TestMain(m *testing.M) {
	if handled, code := probeworker.Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
