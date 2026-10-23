//go:build canary_worker_race && race

package process

import (
	"errors"
	"os"
	"path/filepath"
)

// Set only by the explicit race-test runner's linker flag. This file requires
// both a test build tag and race instrumentation. In particular, the production
// CGO-disabled build cannot accidentally enable it with the tag alone.
// The child is the separately checksummed, uninstrumented production executable;
// resource limits, fixed arguments, private input and response validation remain
// unchanged. There is no runtime configuration or environment override.
var raceWorkerExecutable string

func workerExecutable() (string, error) {
	if !filepath.IsAbs(raceWorkerExecutable) {
		return "", errors.New("Race worker artifact was not configured")
	}
	info, err := os.Lstat(raceWorkerExecutable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Race worker artifact is unavailable")
	}
	return raceWorkerExecutable, nil
}
