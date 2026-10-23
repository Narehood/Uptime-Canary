// Package jsonquery evaluates JSONata monitor conditions in disposable guarded
// worker processes. Call Check outside monitor-state locks. Validate never
// compiles or evaluates an expression.
package jsonquery

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

const (
	Type               = "json-query"
	MaxDocumentBytes   = 1 << 20
	MaxExpressionBytes = 4096
	MaxExpectedBytes   = 4096
)

type Operator string

const (
	Equal        Operator = "=="
	NotEqual     Operator = "!="
	Greater      Operator = ">"
	GreaterEqual Operator = ">="
	Less         Operator = "<"
	LessEqual    Operator = "<="
	Contains     Operator = "contains"
)

// Config contains only data, never bindings or native functions. Expected is a
// JSON string, finite number, or boolean (not a JSON-encoded expression).
// Equality/contains use scalar text; ordering uses JSONata numeric conversion.
// See README.md for the verified Kuma coercion contract and compatibility gaps.
type Config struct {
	Expression string          `json:"expression"`
	Operator   Operator        `json:"operator"`
	Expected   json.RawMessage `json:"expected"`
}

// Request carries the actual response document, not a URL or a selector result.
// Timeout includes worker admission, startup, compilation and evaluation.
type Request struct {
	Type     string          `json:"type"`
	Timeout  time.Duration   `json:"timeout"`
	Query    Config          `json:"query"`
	Document json.RawMessage `json:"document"`
}

var errConfig = errors.New("invalid JSON query configuration")

// Validate performs bounded shape/size/type checks only. In particular invalid
// JSONata syntax can pass this check and is rejected only in the guarded child.
func (c Config) Validate() error {
	if len(c.Expression) == 0 || len(c.Expression) > MaxExpressionBytes || !utf8.ValidString(c.Expression) || len(bytes.TrimSpace([]byte(c.Expression))) == 0 {
		return errConfig
	}
	switch c.Operator {
	case Equal, NotEqual, Greater, GreaterEqual, Less, LessEqual, Contains:
	default:
		return errConfig
	}
	if len(c.Expected) == 0 || len(c.Expected) > MaxExpectedBytes || !utf8.Valid(c.Expected) {
		return errConfig
	}
	if _, ok := scalar(c.Expected); !ok {
		return errConfig
	}
	return nil
}

func (r Request) Validate() error {
	if r.Type != Type || r.Timeout <= 0 || r.Timeout > 5*time.Minute || len(r.Document) == 0 || len(r.Document) > MaxDocumentBytes {
		return errConfig
	}
	if r.Query.Validate() != nil || !utf8.Valid(r.Document) || !json.Valid(r.Document) {
		return errConfig
	}
	return nil
}

// scalar refuses containers, null, malformed JSON and non-finite numbers.
// JSON decoding deliberately uses float64, as the JS reference uses Number.
func scalar(raw []byte) (any, bool) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || b[0] == '{' || b[0] == '[' || bytes.Equal(b, []byte("null")) {
		return nil, false
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil, false
	}
	switch n := v.(type) {
	case string, bool:
		return v, true
	case float64:
		return v, !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return nil, false
	}
}
