package services

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

// evaluateJSONQuery runs after transport I/O, never during config validation.
// Text payloads are explicitly JSON-encoded; documents retain their structure.
func evaluateJSONQuery(ctx context.Context, query jsonquery.Config, document []byte, service string) Result {
	if len(document) > jsonquery.MaxDocumentBytes || !utf8.Valid(document) {
		return down(service + " JSONata input exceeds limits or is not UTF-8")
	}
	if !json.Valid(document) {
		document, _ = json.Marshal(string(document))
	}
	budget := 5 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < budget {
			budget = remaining
		}
	}
	r := jsonquery.Check(ctx, jsonquery.Request{Type: jsonquery.Type, Timeout: budget, Query: query, Document: document})
	message := service + " JSONata evaluation failed or condition not met"
	if r.State == "up" {
		message = service + " JSONata condition passed"
	} else if r.State == "pending" {
		message = service + " JSONata worker unavailable"
	}
	return Result{State: r.State, Message: message, LatencyMs: r.LatencyMs}
}
