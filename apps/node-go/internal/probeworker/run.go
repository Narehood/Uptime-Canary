// Package probeworker connects service engines to the disposable process runner.
// Executables must call Dispatch before ordinary startup.
package probeworker

import (
	"context"

	"uptime-canary/apps/node-go/internal/probeworker/process"
	"uptime-canary/apps/node-go/internal/services"
)

const ModeArgument = process.ModeArgument

// Run preserves the typed API and isolates every engine explicitly routed here.
// services.Check automatically isolates Oracle on its own.
func Run(ctx context.Context, c services.Config) services.Result {
	if c.Type == services.Oracle {
		return services.Check(ctx, c)
	}
	return services.Result(process.RunConfig(ctx, c.Timeout, c))
}
