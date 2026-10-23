package globalping

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

var errResponse = errors.New("invalid Globalping response")

type object map[string]any

// Decode through exact-name maps rather than Go's case-insensitive struct
// matching. Reject duplicate keys (including case variants and escaped keys)
// at every depth, including ignored provider metadata and target documents.
func decodeJSON(b []byte) (any, error) {
	if len(b) == 0 || !utf8.Valid(b) || !unicodeEscapes(b) {
		return nil, errResponse
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	budget := 100000
	v, err := jsonValue(d, 0, &budget)
	if err != nil {
		return nil, errResponse
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errResponse
	}
	return v, nil
}

func jsonValue(d *json.Decoder, depth int, budget *int) (any, error) {
	*budget--
	if depth > 32 || *budget < 0 {
		return nil, errResponse
	}
	t, err := d.Token()
	if err != nil {
		return nil, errResponse
	}
	delim, container := t.(json.Delim)
	if !container {
		return t, nil
	}
	switch delim {
	case '{':
		m := object{}
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok || seen[strings.ToLower(key)] {
				return nil, errResponse
			}
			seen[strings.ToLower(key)] = true
			v, err := jsonValue(d, depth+1, budget)
			if err != nil {
				return nil, errResponse
			}
			m[key] = v
		}
		if t, err := d.Token(); err != nil || t != json.Delim('}') {
			return nil, errResponse
		}
		return m, nil
	case '[':
		var a []any
		for d.More() {
			v, err := jsonValue(d, depth+1, budget)
			if err != nil {
				return nil, errResponse
			}
			a = append(a, v)
		}
		if t, err := d.Token(); err != nil || t != json.Delim(']') {
			return nil, errResponse
		}
		return a, nil
	}
	return nil, errResponse
}

func decodeObject(b []byte) (object, error) {
	v, err := decodeJSON(b)
	m, ok := v.(object)
	if err != nil || !ok {
		return nil, errResponse
	}
	return m, nil
}

func str(m object, k string) (string, bool)   { s, ok := m[k].(string); return s, ok }
func obj(m object, k string) (object, bool)   { v, ok := m[k].(object); return v, ok }
func arr(m object, k string) ([]any, bool)    { v, ok := m[k].([]any); return v, ok }
func boolean(m object, k string) (bool, bool) { v, ok := m[k].(bool); return v, ok }

func number(m object, k string) (float64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

func integer(m object, k string, min, max int64) (int64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil && i >= min && i <= max
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, b := range []byte(s) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

// encoding/json replaces isolated UTF-16 surrogates with U+FFFD. Refuse that
// lossy repair: a malformed body must never acquire a matching keyword/value.
func unicodeEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		v, ok := hex4(b, i+1)
		if !ok {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, ok := hex4(b, i+3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func hex4(b []byte, offset int) (int, bool) {
	if offset+4 > len(b) {
		return 0, false
	}
	n := 0
	for _, c := range b[offset : offset+4] {
		n *= 16
		switch {
		case c >= '0' && c <= '9':
			n += int(c - '0')
		case c >= 'a' && c <= 'f':
			n += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n += int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}
