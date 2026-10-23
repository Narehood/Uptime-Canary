package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// Condition compares a scalar with a JSON scalar. Numbers compare numerically
// without float64 precision loss; null never equals an empty string.
type Condition struct {
	Operator string          `json:"operator"` // eq, ne, gt, ge, lt, le, contains, prefix, suffix
	Expected json.RawMessage `json:"expected"`
}

func (c Condition) expected() (any, error) {
	if len(c.Expected) == 0 || len(c.Expected) > 4096 {
		return nil, errors.New("condition expected scalar is missing or too large")
	}
	d := json.NewDecoder(bytes.NewReader(c.Expected))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil || !json.Valid(c.Expected) {
		return nil, errors.New("invalid condition expected JSON scalar")
	}
	switch v.(type) {
	case nil, string, bool, json.Number:
		return v, nil
	}
	return nil, errors.New("condition expected value must be scalar")
}
func (c Condition) validate() error {
	v, err := c.expected()
	if err != nil {
		return err
	}
	if n, ok := v.(json.Number); ok {
		if _, ok := scalarNumber(string(n)); !ok {
			return errors.New("condition number exceeds numeric limit")
		}
	}
	switch c.Operator {
	case "eq", "ne":
		return nil
	case "gt", "ge", "lt", "le":
		if _, ok := v.(json.Number); ok {
			return nil
		}
	case "contains", "prefix", "suffix":
		if _, ok := v.(string); ok {
			return nil
		}
	default:
		return errors.New("invalid condition operator")
	}
	return errors.New("condition operator and expected scalar type disagree")
}
func (c Condition) matches(actual []byte, isNull bool) bool {
	v, err := c.expected()
	if err != nil {
		return false
	}
	eq := false
	cmp := 0
	ordered := false
	switch expected := v.(type) {
	case nil:
		eq = isNull
	case string:
		if !isNull {
			eq = string(actual) == expected
		}
		if !isNull {
			switch c.Operator {
			case "contains":
				return strings.Contains(string(actual), expected)
			case "prefix":
				return strings.HasPrefix(string(actual), expected)
			case "suffix":
				return strings.HasSuffix(string(actual), expected)
			}
		}
	case bool:
		s := string(actual)
		if !isNull {
			if s != "true" && s != "false" && s != "t" && s != "f" && s != "1" && s != "0" {
				return false
			}
			if expected {
				eq = s == "true" || s == "t" || s == "1"
			} else {
				eq = s == "false" || s == "f" || s == "0"
			}
		}
	case json.Number:
		// Bound the exponent before big.Rat to prevent attacker-controlled giant integers.
		if !isNull && len(actual) <= 1024 {
			a, ok := scalarNumber(string(actual))
			b, bok := scalarNumber(string(expected))
			if ok && bok {
				cmp = a.Cmp(b)
				eq = cmp == 0
				ordered = true
			}
		}
		if !isNull && !ordered {
			return false
		}
	}
	switch c.Operator {
	case "eq":
		return eq
	case "ne":
		return !eq
	case "gt":
		return ordered && cmp > 0
	case "ge":
		return ordered && cmp >= 0
	case "lt":
		return ordered && cmp < 0
	case "le":
		return ordered && cmp <= 0
	}
	return false
}
func scalarNumber(s string) (*big.Rat, bool) {
	if len(s) > 1024 || !numberScalar.MatchString(s) {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		ex := s[i+1:]
		if len(ex) > 5 {
			return nil, false
		}
		n, ok := new(big.Int).SetString(ex, 10)
		if !ok || n.Cmp(big.NewInt(4096)) > 0 || n.Cmp(big.NewInt(-4096)) < 0 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}

var numberScalar = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
