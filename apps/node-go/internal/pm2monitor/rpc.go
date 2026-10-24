package pm2monitor

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
)

const maxVersionReply = 4 << 10
const maxMonitorReply = 4 << 20
const maxProcesses = 4096

// AMP 1: exactly two length-delimited arguments, JSON followed by correlation.
// The protocol is implemented from the pinned wire format, without a PM2 CLI
// or client initializer that could create directories or start a daemon.
func call(conn net.Conn, method, id string, maximum int) (json.RawMessage, error) {
	if method != "getVersion" && method != "getMonitorData" || len(id) == 0 || len(id) > 64 {
		return nil, errReply
	}
	body, err := json.Marshal(struct {
		Type   string     `json:"type"`
		Method string     `json:"method"`
		Args   []struct{} `json:"args"`
	}{"call", method, []struct{}{{}}})
	if err != nil {
		return nil, errReply
	}
	arguments := [][]byte{append([]byte("j:"), body...), []byte("s:" + id)}
	request := []byte{0x12}
	for _, argument := range arguments {
		request = binary.BigEndian.AppendUint32(request, uint32(len(argument)))
		request = append(request, argument...)
	}
	if len(request) > 512 {
		return nil, errReply
	}
	if _, err := io.Copy(conn, bytes.NewReader(request)); err != nil {
		return nil, errReply
	}
	var header [1]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 0x12 {
		return nil, errReply
	}
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, errReply
	}
	firstLength := binary.BigEndian.Uint32(length[:])
	if firstLength < 2 || uint64(firstLength)+9 > uint64(maximum) {
		return nil, errReply
	}
	first := make([]byte, int(firstLength))
	if _, err := io.ReadFull(conn, first); err != nil || !bytes.HasPrefix(first, []byte("j:")) {
		return nil, errReply
	}
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, errReply
	}
	secondLength := binary.BigEndian.Uint32(length[:])
	if secondLength != uint32(len(id)+2) || uint64(firstLength)+uint64(secondLength)+9 > uint64(maximum) {
		return nil, errReply
	}
	second := make([]byte, int(secondLength))
	if _, err := io.ReadFull(conn, second); err != nil || string(second) != "s:"+id {
		return nil, errReply
	}
	return replyPayload(first[2:])
}

func replyPayload(raw []byte) (json.RawMessage, error) {
	if strictJSON(raw) != nil {
		return nil, errReply
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || fields["args"] == nil {
		return nil, errReply
	}
	var args []json.RawMessage
	if json.Unmarshal(fields["args"], &args) != nil || len(args) != 1 {
		return nil, errReply
	}
	return args[0], nil
}

func replyString(raw json.RawMessage, required bool) (string, error) {
	if raw == nil && !required {
		return "", nil
	}
	var value string
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return "", errReply
	}
	return value, nil
}

func replyID(raw json.RawMessage) (uint64, error) {
	id, err := strconv.ParseUint(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil || id > 1<<53-1 {
		return 0, errReply
	}
	return id, nil
}

func processState(raw []byte, c Config) (string, string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return "", "", errReply
	}
	ids := map[uint64]bool{}
	matched, notOnline, unknown := false, false, false
	for count := 0; d.More(); count++ {
		if count >= maxProcesses {
			return "", "", errReply
		}
		var entry map[string]json.RawMessage
		if d.Decode(&entry) != nil || entry == nil {
			return "", "", errReply
		}
		name, nameErr := replyString(entry["name"], true)
		id, idErr := replyID(entry["pm_id"])
		var env map[string]json.RawMessage
		if nameErr != nil || name == "" || idErr != nil || ids[id] || json.Unmarshal(entry["pm2_env"], &env) != nil || env == nil {
			return "", "", errReply
		}
		ids[id] = true
		namespace, namespaceErr := replyString(env["namespace"], false)
		status, statusErr := replyString(env["status"], true)
		if namespaceErr != nil || statusErr != nil {
			return "", "", errReply
		}
		if env["name"] != nil {
			if environmentName, err := replyString(env["name"], true); err != nil || environmentName != name {
				return "", "", errReply
			}
		}
		if env["pm_id"] != nil {
			if environmentID, err := replyID(env["pm_id"]); err != nil || environmentID != id {
				return "", "", errReply
			}
		}
		if name != c.Name || c.Options.Namespace != "" && namespace != c.Options.Namespace {
			continue
		}
		matched = true
		switch status {
		case "online":
		case "stopped", "stopping", "launching", "errored", "waiting restart", "one-launch-status":
			notOnline = true
		default:
			unknown = true
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') {
		return "", "", errReply
	}
	if _, err := d.Token(); err != io.EOF {
		return "", "", errReply
	}
	if !matched {
		return "down", "PM2 process was not found", nil
	}
	if notOnline {
		return "down", "PM2 process is not online", nil
	}
	if unknown {
		return "pending", "PM2 status unavailable", nil
	}
	return "up", "PM2 process is online", nil
}
