package steam

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Validate structure before extracting fields: duplicate/case-confusable
// authority, incomplete JSON and excessive nesting must never certify Up.
func uniqueValue(d *json.Decoder, depth int, budget *int) bool {
	*budget--
	if depth > 32 || *budget < 0 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
			if !uniqueValue(d, depth+1, budget) {
				return false
			}
		}
		last, err := d.Token()
		return err == nil && last == json.Delim('}')
	case '[':
		for d.More() {
			if !uniqueValue(d, depth+1, budget) {
				return false
			}
		}
		last, err := d.Token()
		return err == nil && last == json.Delim(']')
	default:
		return false
	}
}

func directoryResult(raw []byte, address net.IP, port string) (listed, valid bool) {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return false, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	budget := 16384
	if !uniqueValue(d, 0, &budget) {
		return false, false
	}
	if _, err := d.Token(); err != io.EOF {
		return false, false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return false, false
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(root["response"], &response) != nil || response == nil {
		return false, false
	}
	// An absent repeated field in an otherwise empty protobuf response is an
	// empty list. Unknown/error-only response shapes do not certify absence.
	value, present := response["servers"]
	if !present {
		return false, len(response) == 0
	}
	for _, key := range []string{"error", "error_code", "errormsg"} {
		if _, present := response[key]; present {
			return false, false
		}
	}
	var servers []map[string]json.RawMessage
	if json.Unmarshal(value, &servers) != nil || servers == nil || len(servers) > 128 {
		return false, false
	}
	for _, server := range servers {
		var addr string
		if json.Unmarshal(server["addr"], &addr) != nil || len(addr) > 320 {
			return false, false
		}
		host, foundPort, err := net.SplitHostPort(addr)
		if err != nil {
			// Steam can encode an IPv6 address without brackets. The final
			// colon still separates its decimal game port, as in the filter.
			index := strings.LastIndexByte(addr, ':')
			if index <= 0 {
				return false, false
			}
			host, foundPort = addr[:index], addr[index+1:]
		}
		ip := net.ParseIP(host)
		n, err := strconv.ParseUint(foundPort, 10, 16)
		if ip == nil || err != nil || n == 0 || strconv.FormatUint(n, 10) != foundPort {
			return false, false
		}
		if ip.Equal(address) && foundPort == port {
			listed = true
		}
	}
	// A nonempty answer that does not match this address failed the requested
	// filter. It cannot certify either membership or authoritative absence.
	return listed, listed || len(servers) == 0
}
