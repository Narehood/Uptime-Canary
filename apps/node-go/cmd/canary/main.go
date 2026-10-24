package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"uptime-canary/apps/node-go/internal/canary"
	"uptime-canary/apps/node-go/internal/probeworker"
)

func run() int {
	args := os.Args[1:]
	if handled, code := probeworker.Dispatch(args); handled {
		return code
	}
	if len(args) == 1 && args[0] == "dashboard" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := canary.RunDashboard(ctx, os.Getenv, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "Uptime-Canary dashboard:", err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		fmt.Println(canary.Usage())
		return 0
	}
	if len(args) > 0 && args[0] != "serve" {
		result, err := canary.RunCLI(context.Background(), args, os.Getenv, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	options, err := canary.OptionsFromEnvironment(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Uptime-Canary:", err)
		return 1
	}
	node, err := canary.New(options)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Uptime-Canary: Invalid startup configuration:", err)
		return 1
	}
	if err = node.Start(); err != nil {
		_ = node.Stop()
		fmt.Fprintln(os.Stderr, "Uptime-Canary: Listener failed to start")
		return 1
	}
	fmt.Printf("Uptime-Canary %s listening at %s\n", node.ID, node.URL())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-node.Done():
	}
	fault := node.Error()
	err = node.Stop()
	if err != nil || fault != nil {
		fmt.Fprintln(os.Stderr, "Uptime-Canary: Runtime stopped after a persistence failure")
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
