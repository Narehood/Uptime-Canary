package checks

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/dns/dnsmessage"
)

var errDNSRecord = errors.New("invalid DNS evidence")

func canonicalExtendedRecord(kind, input string) (string, bool) {
	fields := strings.Fields(input)
	name := func(value string) (string, bool) {
		if value == "." {
			return value, true
		}
		return strings.ToLower(strings.TrimSuffix(value, ".")), validDNSName(value)
	}
	if kind == "SRV" && len(fields) == 4 {
		out := make([]string, 4)
		for i := 0; i < 3; i++ {
			value, err := strconv.ParseUint(fields[i], 10, 16)
			if err != nil {
				return "", false
			}
			out[i] = strconv.FormatUint(value, 10)
		}
		var ok bool
		out[3], ok = name(fields[3])
		return strings.Join(out, " "), ok
	}
	if kind == "SOA" && len(fields) == 7 {
		out := make([]string, 7)
		for i := 0; i < 2; i++ {
			var ok bool
			out[i], ok = name(fields[i])
			if !ok {
				return "", false
			}
		}
		for i := 2; i < 7; i++ {
			value, err := strconv.ParseUint(fields[i], 10, 32)
			if err != nil {
				return "", false
			}
			out[i] = strconv.FormatUint(value, 10)
		}
		return strings.Join(out, " "), true
	}
	if kind == "CAA" && len(fields) >= 3 {
		flags, err := strconv.ParseUint(fields[0], 10, 8)
		if err != nil || len(fields[1]) < 1 || len(fields[1]) > 15 {
			return "", false
		}
		for _, ch := range []byte(fields[1]) {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
				return "", false
			}
		}
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), fields[0])), fields[1]))
		if strings.HasPrefix(value, "\"") {
			if json.Unmarshal([]byte(value), &value) != nil {
				return "", false
			}
		}
		if !utf8.ValidString(value) || len(value) > 8192 {
			return "", false
		}
		quoted, _ := json.Marshal(value)
		return fmt.Sprintf("%d %s %s", flags, strings.ToLower(fields[1]), quoted), true
	}
	return "", false
}

func extendedDNS(ctx context.Context, target, kind, server string) ([]string, error) {
	if server == "" {
		return nil, errDNSRecord
	}
	queryType := dnsmessage.TypeSOA
	if kind == "CAA" {
		queryType = dnsmessage.Type(257)
	}
	name, err := dnsmessage.NewName(strings.TrimSuffix(target, ".") + ".")
	if err != nil {
		return nil, err
	}
	var nonce [2]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(nonce[:])
	question := dnsmessage.Question{Name: name, Type: queryType, Class: dnsmessage.ClassINET}
	query, err := (&dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{question}}).Pack()
	if err != nil {
		return nil, err
	}
	exchange := func(tcp bool) ([]byte, error) {
		network := "udp"
		if tcp {
			network = "tcp"
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, network, server)
		if err != nil {
			return nil, err
		}
		defer bindConnection(ctx, connection)()
		wire := query
		if tcp {
			wire = append([]byte{byte(len(query) >> 8), byte(len(query))}, query...)
		}
		for len(wire) > 0 {
			n, err := connection.Write(wire)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, io.ErrShortWrite
			}
			wire = wire[n:]
		}
		if tcp {
			var prefix [2]byte
			if _, err = io.ReadFull(connection, prefix[:]); err != nil {
				return nil, err
			}
			size := int(binary.BigEndian.Uint16(prefix[:]))
			if size < 12 {
				return nil, errDNSRecord
			}
			wire = make([]byte, size)
			_, err = io.ReadFull(connection, wire)
			return wire, err
		}
		wire = make([]byte, maxDNSBytes)
		n, err := connection.Read(wire)
		return wire[:n], err
	}
	raw, err := exchange(false)
	if err != nil {
		return nil, err
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(raw)
	if err != nil || !header.Response || header.ID != id || header.OpCode != 0 || header.RCode != dnsmessage.RCodeSuccess {
		return nil, errDNSRecord
	}
	if header.Truncated {
		raw, err = exchange(true)
		if err != nil {
			return nil, err
		}
		header, err = parser.Start(raw)
		if err != nil || !header.Response || header.ID != id || header.OpCode != 0 || header.RCode != dnsmessage.RCodeSuccess || header.Truncated {
			return nil, errDNSRecord
		}
	}
	got, err := parser.Question()
	if err != nil || got.Type != question.Type || got.Class != question.Class || !strings.EqualFold(got.Name.String(), question.Name.String()) {
		return nil, errDNSRecord
	}
	if _, err = parser.Question(); err != dnsmessage.ErrSectionDone {
		return nil, errDNSRecord
	}
	aliases := map[string]string{}
	values := map[string][]string{}
	for count := 0; ; count++ {
		record, err := parser.Answer()
		if err == dnsmessage.ErrSectionDone {
			break
		}
		if err != nil || count >= 1024 || record.Header.Class != dnsmessage.ClassINET {
			return nil, errDNSRecord
		}
		owner := strings.ToLower(record.Header.Name.String())
		if cname, ok := record.Body.(*dnsmessage.CNAMEResource); ok {
			alias := strings.ToLower(cname.CNAME.String())
			if old, exists := aliases[owner]; exists && old != alias {
				return nil, errDNSRecord
			}
			aliases[owner] = alias
		}
		if record.Header.Type != queryType {
			continue
		}
		var value string
		if soa, ok := record.Body.(*dnsmessage.SOAResource); ok && kind == "SOA" {
			value = fmt.Sprintf("%s %s %d %d %d %d %d", soa.NS, soa.MBox, soa.Serial, soa.Refresh, soa.Retry, soa.Expire, soa.MinTTL)
		} else if caa, ok := record.Body.(*dnsmessage.UnknownResource); ok && kind == "CAA" {
			if len(caa.Data) < 3 || int(caa.Data[1])+2 > len(caa.Data) {
				return nil, errDNSRecord
			}
			length := int(caa.Data[1])
			if !utf8.Valid(caa.Data[2+length:]) {
				return nil, errDNSRecord
			}
			quoted, _ := json.Marshal(string(caa.Data[2+length:]))
			value = fmt.Sprintf("%d %s %s", caa.Data[0], caa.Data[2:2+length], quoted)
		} else {
			return nil, errDNSRecord
		}
		normalized, ok := canonicalExtendedRecord(kind, value)
		if !ok {
			return nil, errDNSRecord
		}
		values[owner] = append(values[owner], normalized)
	}
	owner := strings.ToLower(name.String())
	visited := map[string]bool{}
	for depth := 0; depth < 9; depth++ {
		if visited[owner] {
			return nil, errDNSRecord
		}
		visited[owner] = true
		if alias, ok := aliases[owner]; ok {
			if len(values[owner]) != 0 {
				return nil, errDNSRecord
			}
			owner = alias
			continue
		}
		return values[owner], nil
	}
	return nil, errDNSRecord
}
