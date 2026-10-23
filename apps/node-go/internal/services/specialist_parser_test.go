package services

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func FuzzSpecialistBoundedParsers(f *testing.F) {
	f.Add([]byte{0x30, 0x0b, 2, 1, 1, 4, 1, 'x', 0xa2, 3, 2, 1, 0})
	f.Add([]byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte{1, 0, 0, 0, 0, 0, 4, 0, 10, 0, 51, 0xce})
	f.Add([]byte{2, 0, 0, 20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, p []byte) {
		if len(p) > snmpMaxPacket {
			return
		}
		h, e := snmpHeader(p)
		if e == nil && h.version < 3 {
			_, _ = decodeSNMP(&gosnmp.GoSNMP{Version: gosnmp.SnmpVersion(h.version)}, p)
		}
		_, _ = readSIPResponse(bufio.NewReaderSize(bytes.NewReader(p), 4096), true)
		_, _ = readAMQPMethod(bytes.NewReader(p), 51, rabbitMaxFrame)
		count := 0
		_ = amqpTable(p, 0, &count)
		request := make([]byte, 20)
		request[0] = 1
		_ = radiusResponse(p, request, RADIUSConfig{Secret: "fixture-secret"})
	})
}
