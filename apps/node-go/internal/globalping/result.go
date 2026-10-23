package globalping

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

func evaluate(ctx context.Context, p *prepared, m object) Result {
	results, _ := arr(m, "results") // envelope has admitted this exact shape
	entry := results[0].(object)
	r, _ := obj(entry, "result")
	status, _ := str(r, "status")
	switch status {
	case "in-progress":
		return providerFailure(p.config.Options, "Globalping result incomplete")
	case "offline":
		return providerFailure(p.config.Options, "Globalping probe unavailable")
	case "failed":
		source, _ := str(r, "failureSource")
		switch source {
		case "internal":
			return providerFailure(p.config.Options, "Globalping provider measurement failed")
		case "resolver":
			return Result{State: Down, Message: "remote resolver measurement failed"}
		case "target":
			return Result{State: Down, Message: "remote target measurement failed"}
		default:
			return Result{State: Down, Message: "remote measurement failed"} // absent source: compatibility
		}
	case "finished":
		switch p.payload.Type {
		case "ping":
			return pingResult(p, r)
		case "http":
			return httpResult(ctx, p, r)
		case "dns":
			return dnsResult(p, r)
		}
	}
	return invalidResult(p)
}

func invalidResult(p *prepared) Result {
	return providerFailure(p.config.Options, "Globalping measurement data invalid")
}

func pingResult(p *prepared, r object) Result {
	s, ok := obj(r, "stats")
	if !ok {
		return invalidResult(p)
	}
	total, a := integer(s, "total", 1, 16)
	rcv, b := integer(s, "rcv", 0, total)
	drop, c := integer(s, "drop", 0, total)
	loss, d := number(s, "loss")
	timings, e := arr(r, "timings")
	if !a || !b || !c || !d || !e || total != int64(p.config.Options.Ping.Packets) ||
		drop != total-rcv || loss > 100 || math.Abs(loss-100*float64(drop)/float64(total)) > 1 || len(timings) != int(rcv) {
		return invalidResult(p)
	}
	for _, timing := range timings {
		t, ok := timing.(object)
		if !ok {
			return invalidResult(p)
		}
		if _, ok := number(t, "rtt"); !ok {
			return invalidResult(p)
		}
		if p.config.Options.Ping.Protocol == "ICMP" {
			if _, ok := integer(t, "ttl", 0, 255); !ok {
				return invalidResult(p)
			}
		}
	}
	if rcv == 0 {
		for _, key := range []string{"min", "avg", "max"} {
			if v, exists := s[key]; !exists || v != nil {
				return invalidResult(p)
			}
		}
		return Result{State: Down, Message: "remote ping received no replies"}
	}
	minRTT, a := number(s, "min")
	avg, b := number(s, "avg")
	maxRTT, c := number(s, "max")
	if !a || !b || !c || minRTT > avg || avg > maxRTT {
		return invalidResult(p)
	}
	return Result{State: Up, LatencyMs: avg, LatencyAvailable: true, Message: "remote ping received replies"}
}

func httpResult(ctx context.Context, p *prepared, r object) Result {
	o := p.config.Options.HTTP
	code, codeOK := integer(r, "statusCode", 100, 599)
	timings, timingsOK := obj(r, "timings")
	total, totalOK := number(timings, "total")
	truncated, truncatedOK := boolean(r, "truncated")
	bodyValue, bodyExists := r["rawBody"]
	body, stringBody := bodyValue.(string)
	if !codeOK || !timingsOK || !totalOK || !truncatedOK || !bodyExists || bodyValue != nil && !stringBody || len(body) > 64<<10 {
		return invalidResult(p)
	}
	for _, key := range []string{"dns", "tcp", "tls", "firstByte", "download"} {
		if v, exists := timings[key]; exists {
			if v == nil && (key == "dns" || key == "tls") {
				continue
			}
			if _, ok := number(timings, key); !ok {
				return invalidResult(p)
			}
		}
	}
	tlsValue, tlsExists := r["tls"]
	if !tlsExists {
		return invalidResult(p)
	}
	authorized := true
	if tlsValue != nil {
		t, ok := tlsValue.(object)
		if !ok {
			return invalidResult(p)
		}
		var authorizedOK bool
		authorized, authorizedOK = boolean(t, "authorized")
		if !authorizedOK {
			return invalidResult(p)
		}
	} else if p.https {
		return invalidResult(p)
	}
	result := Result{State: Down, LatencyMs: total, LatencyAvailable: true, StatusCode: int(code)}
	if p.https && !o.IgnoreTLS && !authorized {
		result.Message = "remote HTTP TLS certificate rejected"
		return result
	}
	if !accepted(int(code), o.AcceptedStatuses) {
		result.Message = "remote HTTP status not accepted"
		return result
	}
	if o.Keyword != "" || o.JSONata != nil {
		if !stringBody || truncated {
			result.Message = "remote HTTP body unavailable or truncated"
			return result
		}
	}
	if o.Keyword != "" {
		matched := strings.Contains(body, o.Keyword)
		if o.InvertKeyword {
			matched = !matched
		}
		if !matched {
			result.Message = "remote HTTP keyword condition not met"
			return result
		}
	}
	if o.JSONata != nil {
		// Malformed/ambiguous JSON cannot satisfy a condition. Never fall back to
		// rawOutput, evaluator calls, or a JSON string wrapping invalid JSON.
		if _, err := decodeJSON([]byte(body)); err != nil {
			result.Message = "remote HTTP body is not valid bounded JSON"
			return result
		}
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || ctx.Err() != nil {
			result.State, result.Message = Pending, "Globalping HTTP JSONata worker unavailable"
			return result
		}
		query := jsonquery.Check(ctx, jsonquery.Request{Type: jsonquery.Type, Timeout: remaining, Query: *o.JSONata, Document: json.RawMessage(body)})
		if query.State == Pending {
			result.State, result.Message = Pending, "Globalping HTTP JSONata worker unavailable"
			return result
		}
		if query.State != Up {
			result.Message = "remote HTTP JSONata evaluation failed or condition not met"
			return result
		}
	}
	result.State, result.Message = Up, "remote HTTP conditions passed"
	return result
}

func accepted(code int, ranges []StatusRange) bool {
	if len(ranges) == 0 {
		return code >= 200 && code <= 299
	}
	for _, r := range ranges {
		if code >= r.Min && code <= r.Max {
			return true
		}
	}
	return false
}

func dnsResult(p *prepared, r object) Result {
	code, codeOK := integer(r, "statusCode", 0, 4095)
	name, nameOK := str(r, "statusCodeName")
	timings, timingsOK := obj(r, "timings")
	total, totalOK := number(timings, "total")
	resolver, resolverOK := str(r, "resolver")
	answers, answersOK := arr(r, "answers")
	if !codeOK || !nameOK || !text(name, 64, false) || !timingsOK || !totalOK || !resolverOK || !host(resolver, false) ||
		!answersOK || len(answers) > 256 || code == 0 && name != "NOERROR" || code != 0 && name == "NOERROR" {
		return invalidResult(p)
	}
	matched := false
	bytes := 0
	for _, answer := range answers {
		a, ok := answer.(object)
		if !ok {
			return invalidResult(p)
		}
		owner, ownerOK := str(a, "name")
		typeName, typeOK := str(a, "type")
		class, classOK := str(a, "class")
		_, ttlOK := integer(a, "ttl", 0, 4294967295)
		value, valueOK := str(a, "value")
		if !ownerOK || !text(owner, 1024, false) || !typeOK || !text(typeName, 32, false) || !classOK || !text(class, 32, false) || !ttlOK ||
			!valueOK || len(value) > 8192 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return invalidResult(p)
		}
		bytes += len(owner) + len(typeName) + len(class) + len(value)
		if bytes > 64<<10 {
			return invalidResult(p)
		}
		if p.dnsRegex != nil && p.dnsRegex.MatchString(value) {
			matched = true
		}
	}
	result := Result{State: Down, LatencyMs: total, LatencyAvailable: true}
	if code != 0 {
		result.Message = "remote DNS response reported an error"
		return result
	}
	if len(answers) == 0 && !p.config.Options.DNS.AllowEmptyAnswers {
		result.Message = "remote DNS response had no answers"
		return result
	}
	if p.dnsRegex != nil && !matched {
		result.Message = "remote DNS answer condition not met"
		return result
	}
	result.State, result.Message = Up, "remote DNS conditions passed"
	return result
}
