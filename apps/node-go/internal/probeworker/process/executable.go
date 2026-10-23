//go:build !canary_worker_race

package process

import "os"

// Production always executes itself; neither configuration nor environment can
// select another program. The separate race-only build variant is never part
// of the CGO-disabled release build.
func workerExecutable() (string, error) { return os.Executable() }
