package canary

import (
	"os"
	"testing"

	"uptime-canary/apps/node-go/internal/probeworker"
)

// Run launches the current executable, which is the package test binary here.
// Claim worker mode before testing parses flags or opens any node resources.
func TestMain(m *testing.M) {
	if handled, code := probeworker.Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
