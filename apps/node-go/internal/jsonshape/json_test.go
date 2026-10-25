package jsonshape

import (
	"strings"
	"testing"
)

func TestUnambiguousJSONShape(t *testing.T) {
	for _, raw := range []string{`{}`, `{"name":"\ud83d\ude80"}`, `{"NAME":1,"name":2}`, strings.Repeat("[", 32) + strings.Repeat("]", 32)} {
		if Validate([]byte(raw)) != nil {
			t.Fatal("valid JSON refused", raw)
		}
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":1,"\u0078":2}`, `{"n":{"x":1,"x":2}}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, "{\"x\":\"\xff\"}", `{} {}`, strings.Repeat("[", 33) + strings.Repeat("]", 33)} {
		if Validate([]byte(raw)) == nil {
			t.Fatal("ambiguous JSON admitted")
		}
	}
}
