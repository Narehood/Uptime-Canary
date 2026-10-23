package checks

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

type selectorStep struct {
	key   string
	index int
	array bool
}

// parseSelector intentionally supports only unambiguous object/array access.
func parseSelector(path string) ([]selectorStep, error) {
	if len(path) == 0 || len(path) > 2048 || path[0] != '$' {
		return nil, invalid("JSON path must start with $ and be at most 2048 bytes")
	}
	steps := []selectorStep{}
	for pos := 1; pos < len(path); {
		if len(steps) >= 64 {
			return nil, invalid("JSON path exceeds 64 steps")
		}
		switch path[pos] {
		case '.':
			pos++
			start := pos
			for pos < len(path) {
				b := path[pos]
				if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || pos > start && b >= '0' && b <= '9' {
					pos++
					continue
				}
				break
			}
			if pos == start {
				return nil, invalid("JSON path object member is invalid")
			}
			steps = append(steps, selectorStep{key: path[start:pos]})
		case '[':
			pos++
			if pos < len(path) && path[pos] == '"' {
				start := pos
				pos++
				for pos < len(path) && path[pos] != '"' {
					if path[pos] == '\\' {
						pos++
					}
					pos++
				}
				if pos >= len(path) {
					return nil, invalid("JSON path quoted member is invalid")
				}
				pos++
				var key string
				if err := json.Unmarshal([]byte(path[start:pos]), &key); err != nil {
					return nil, invalid("JSON path quoted member is invalid")
				}
				steps = append(steps, selectorStep{key: key})
			} else {
				start := pos
				for pos < len(path) && path[pos] >= '0' && path[pos] <= '9' {
					pos++
				}
				if start == pos || pos-start > 7 || pos-start > 1 && path[start] == '0' {
					return nil, invalid("JSON path array index is invalid")
				}
				i, err := strconv.Atoi(path[start:pos])
				if err != nil || i > MaxBodyBytes {
					return nil, invalid("JSON path array index exceeds limit")
				}
				steps = append(steps, selectorStep{index: i, array: true})
			}
			if pos >= len(path) || path[pos] != ']' {
				return nil, invalid("JSON path bracket is invalid")
			}
			pos++
		default:
			return nil, invalid("JSON path syntax is unsupported")
		}
	}
	return steps, nil
}

func decodeJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, invalid("JSON must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, invalid("JSON has trailing input")
	}
	if !boundedJSON(v, 0) {
		return nil, invalid("JSON exceeds nesting or number limits")
	}
	return v, nil
}

func boundedJSON(v any, depth int) bool {
	if depth > 64 {
		return false
	}
	switch x := v.(type) {
	case json.Number:
		return len(x) <= 1024
	case []any:
		for _, item := range x {
			if !boundedJSON(item, depth+1) {
				return false
			}
		}
	case map[string]any:
		for _, item := range x {
			if !boundedJSON(item, depth+1) {
				return false
			}
		}
	}
	return true
}

func selectJSON(v any, steps []selectorStep) (any, bool) {
	for _, step := range steps {
		if step.array {
			a, ok := v.([]any)
			if !ok || step.index >= len(a) {
				return nil, false
			}
			v = a[step.index]
		} else {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok = m[step.key]
			if !ok {
				return nil, false
			}
		}
	}
	return v, true
}

// canonicalNumber compares JSON decimals exactly without float rounding or
// expanding large exponents into enormous integers. Inputs are capped at 1024.
func canonicalNumber(n json.Number) (string, string) {
	s := strings.ToLower(string(n))
	mantissa, exponent, _ := strings.Cut(s, "e")
	e := new(big.Int)
	if exponent != "" {
		e.SetString(exponent, 10)
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return "0", "0"
	}
	trimmed := strings.TrimRight(digits, "0")
	e.Add(e, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, e.String()
}

func equalJSON(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		xc, xe := canonicalNumber(x)
		yc, ye := canonicalNumber(y)
		return xc == yc && xe == ye
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !equalJSON(v, w) {
				return false
			}
		}
		return true
	}
	return false
}

func matchesJSON(body []byte, m JSONMatcher) (bool, error) {
	v, err := decodeJSON(body)
	if err != nil {
		return false, err
	}
	steps, err := parseSelector(m.Path)
	if err != nil {
		return false, err
	}
	selected, found := selectJSON(v, steps)
	matched := found
	if !m.ExistsOnly {
		expected, err := decodeJSON([]byte(m.Expected))
		if err != nil {
			return false, err
		}
		matched = found && equalJSON(selected, expected)
	}
	if m.Negate {
		matched = !matched
	}
	return matched, nil
}
