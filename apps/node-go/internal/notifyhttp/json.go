package notifyhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errJSON = errors.New("invalid acknowledgement")

// Bound recursion, node count, collection sizes, duplicate keys and critical
// key casing before looking at acceptance fields. json.Number keeps IDs exact.
func strictObject(data []byte) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, errJSON
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	v, err := jsonValue(d, 0, &nodes)
	if err != nil {
		return nil, errJSON
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errJSON
	}
	object, ok := v.(map[string]any)
	if !ok {
		return nil, errJSON
	}
	return object, nil
}

func jsonValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	(*nodes)++
	if depth > 16 || *nodes > 4096 {
		return nil, errJSON
	}
	token, err := d.Token()
	if err != nil {
		return nil, errJSON
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		seen := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || len(object) >= 256 || len(key) > 256 {
				return nil, errJSON
			}
			lower := strings.ToLower(key)
			if seen[lower] {
				return nil, errJSON
			}
			seen[lower] = true
			for _, critical := range criticalKeys {
				if strings.EqualFold(key, critical) && key != critical {
					return nil, errJSON
				}
			}
			value, err := jsonValue(d, depth+1, nodes)
			if err != nil {
				return nil, errJSON
			}
			object[key] = value
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errJSON
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for d.More() {
			if len(array) >= 512 {
				return nil, errJSON
			}
			v, err := jsonValue(d, depth+1, nodes)
			if err != nil {
				return nil, errJSON
			}
			array = append(array, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errJSON
		}
		return array, nil
	default:
		return nil, errJSON
	}
}

var criticalKeys = []string{
	"ok", "result", "error_code", "parameters", "retry_after", "message_id",
	"message_thread_id", "chat", "id", "username", "status", "request",
	"error", "errors", "errorCode", "event", "topic", "code", "description",
}

func objectField(object map[string]any, key string) map[string]any {
	v, _ := object[key].(map[string]any)
	return v
}

func stringField(object map[string]any, key string) string {
	v, _ := object[key].(string)
	return v
}

func integerField(object map[string]any, key string) (int64, bool) {
	v, ok := object[key].(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	return n, err == nil
}

func hasAny(object map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := object[key]; ok {
			return true
		}
	}
	return false
}
