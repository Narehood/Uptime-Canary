package pm2monitor

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// ValidateJSON preserves exact-key and Unicode admission when a PM2 name is
// embedded in a larger management or peer record before typed decoding.
func ValidateJSON(raw []byte) error { return strictJSON(raw) }

// Refuse replacement decoding and duplicate decoded keys. Authority is selected
// by exact keys separately; private environment dictionaries may contain HOME,
// NAME or STATUS without acquiring any process-state authority.
func strictJSON(raw []byte) error {
	if !utf8.Valid(raw) || !validStringEscapes(raw) {
		return errReply
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if jsonValue(d, 1) != nil {
		return errReply
	}
	if _, err := d.Token(); err != io.EOF {
		return errReply
	}
	return nil
}

func jsonValue(d *json.Decoder, depth int) error {
	token, err := d.Token()
	if err != nil {
		return errReply
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if depth > 32 {
		return errReply
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] {
				return errReply
			}
			seen[key] = true
			if jsonValue(d, depth+1) != nil {
				return errReply
			}
		}
		if token, err := d.Token(); err != nil || token != json.Delim('}') {
			return errReply
		}
	case '[':
		for d.More() {
			if jsonValue(d, depth+1) != nil {
				return errReply
			}
		}
		if token, err := d.Token(); err != nil || token != json.Delim(']') {
			return errReply
		}
	default:
		return errReply
	}
	return nil
}

// JSON permits surrogate escapes, but only a paired surrogate represents valid
// Unicode. The standard decoder otherwise repairs an isolated surrogate.
func validStringEscapes(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil || value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		i += 4
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inString
}
