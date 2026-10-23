package checks

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func dnsWireName(name string) []byte {
	var b []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

func dnsFixtureResponse(query []byte, truncate bool) []byte {
	if len(query) < 17 {
		return nil
	}
	pos := 12
	var labels []string
	for pos < len(query) && query[pos] != 0 {
		n := int(query[pos])
		pos++
		if n > 63 || pos+n >= len(query) {
			return nil
		}
		labels = append(labels, string(query[pos:pos+n]))
		pos += n
	}
	if pos+5 > len(query) {
		return nil
	}
	pos++
	qtype := binary.BigEndian.Uint16(query[pos:])
	end := pos + 4
	name := strings.ToLower(strings.Join(labels, "."))
	out := make([]byte, 12)
	copy(out[:2], query[:2])
	out[2] = 0x81
	out[3] = 0x80
	binary.BigEndian.PutUint16(out[4:6], 1)
	out = append(out, query[12:end]...)
	if truncate {
		out[2] |= 2
		return out
	}
	if strings.HasPrefix(name, "missing.") {
		out[3] |= 3
		return out
	}
	count := uint16(0)
	appendRR := func(owner []byte, typ uint16, data []byte) {
		out = append(out, owner...)
		var header [10]byte
		binary.BigEndian.PutUint16(header[0:2], typ)
		binary.BigEndian.PutUint16(header[2:4], 1)
		binary.BigEndian.PutUint32(header[4:8], 30)
		binary.BigEndian.PutUint16(header[8:10], uint16(len(data)))
		out = append(out, header[:]...)
		out = append(out, data...)
		count++
	}
	owner := []byte{0xc0, 0x0c}
	if qtype == 1 || qtype == 28 {
		appendRR(owner, 5, dnsWireName("canonical.fixture.invalid"))
		owner = dnsWireName("canonical.fixture.invalid")
	}
	switch qtype {
	case 1:
		appendRR(owner, 1, []byte{192, 0, 2, 7})
	case 28:
		appendRR(owner, 28, net.ParseIP("2001:db8::7").To16())
	case 5:
		appendRR(owner, 5, dnsWireName("canonical.fixture.invalid"))
	case 15:
		appendRR(owner, 15, append([]byte{0, 10}, dnsWireName("mail.fixture.invalid")...))
	case 16:
		appendRR(owner, 16, append(append([]byte{7}, []byte("fixture")...), append([]byte{5}, []byte("-text")...)...))
	case 2:
		appendRR(owner, 2, dnsWireName("ns.fixture.invalid"))
	case 12:
		appendRR(owner, 12, dnsWireName("ptr.fixture.invalid"))
	}
	binary.BigEndian.PutUint16(out[6:8], count)
	return out
}

func fixtureDNS(t *testing.T, truncateUDP, drop bool) (string, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", udp.LocalAddr().String())
	if err != nil {
		udp.Close()
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var tcpCount atomic.Int32
	entered := make(chan struct{}, 32)
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 65535)
		for {
			n, peer, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			if !drop {
				response := dnsFixtureResponse(buf[:n], truncateUDP)
				_, _ = udp.WriteTo(response, peer)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				var header [2]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				query := make([]byte, int(binary.BigEndian.Uint16(header[:])))
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				tcpCount.Add(1)
				if !drop {
					response := dnsFixtureResponse(query, false)
					binary.BigEndian.PutUint16(header[:], uint16(len(response)))
					_, _ = conn.Write(append(header[:], response...))
				}
			}()
		}
	}()
	t.Cleanup(func() { udp.Close(); tcp.Close(); wg.Wait() })
	return udp.LocalAddr().String(), &tcpCount, entered
}

func TestDNSAllSupportedRecordsWithCustomResolver(t *testing.T) {
	server, _, _ := fixtureDNS(t, false, false)
	for _, tc := range []struct{ record, target, expected string }{
		{"A", "fixture.invalid.", "192.0.2.7"},
		{"AAAA", "fixture.invalid.", "2001:0db8:0:0:0:0:0:7"},
		{"CNAME", "fixture.invalid.", "CANONICAL.fixture.invalid."},
		{"MX", "fixture.invalid.", "10 MAIL.fixture.invalid."},
		{"TXT", "fixture.invalid.", "fixture-text"},
		{"NS", "fixture.invalid.", "NS.fixture.invalid."},
		{"PTR", "192.0.2.7", "PTR.fixture.invalid."},
	} {
		t.Run(tc.record, func(t *testing.T) {
			c := Config{Type: DNS, Target: tc.target, Timeout: time.Second, DNS: &DNSConfig{RecordType: tc.record, Server: server, Expected: []string{tc.expected}}}
			requireState(t, Check(context.Background(), c), Up)
			c.DNS.Expected = nil
			requireState(t, Check(context.Background(), c), Up)
		})
	}
	c := Config{Type: DNS, Target: "fixture.invalid.", Timeout: time.Second, DNS: &DNSConfig{Server: server, Expected: []string{"192.0.2.7", "192.0.2.8"}}}
	requireState(t, Check(context.Background(), c), Down)
	c.Target = "missing.invalid."
	c.DNS.Expected = nil
	requireState(t, Check(context.Background(), c), Down)
}

func TestDNSCustomResolverTCPFallback(t *testing.T) {
	server, tcpCount, _ := fixtureDNS(t, true, false)
	requireState(t, Check(context.Background(), Config{Type: DNS, Target: "fixture.invalid.", Timeout: time.Second, DNS: &DNSConfig{Server: server, Expected: []string{"192.0.2.7"}}}), Up)
	if tcpCount.Load() == 0 {
		t.Fatal("truncated UDP response did not retry custom DNS over TCP")
	}
}

func TestDNSLiteralCannotReportServerAvailability(t *testing.T) {
	server, _, _ := fixtureDNS(t, false, true)
	r := Check(context.Background(), Config{Type: DNS, Target: "192.0.2.7", Timeout: time.Second, DNS: &DNSConfig{Server: server}})
	requireState(t, r, Down)
	if !strings.Contains(r.Message, "local data") {
		t.Fatalf("unexpected literal result: %+v", r)
	}
}

func TestDNSDeadlineAndCancellation(t *testing.T) {
	server, _, entered := fixtureDNS(t, false, true)
	start := time.Now()
	requireState(t, Check(context.Background(), Config{Type: DNS, Target: "fixture.invalid.", Timeout: 50 * time.Millisecond, DNS: &DNSConfig{Server: server}}), Down)
	if time.Since(start) > time.Second {
		t.Fatal("DNS deadline ignored")
	}
	// Consume the first run's requests before waiting for the next run.
	for len(entered) > 0 {
		<-entered
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		done <- Check(ctx, Config{Type: DNS, Target: "fixture.invalid.", Timeout: time.Minute, DNS: &DNSConfig{Server: server}})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no DNS query")
	}
	cancel()
	select {
	case r := <-done:
		requireState(t, r, Down)
	case <-time.After(time.Second):
		t.Fatal("DNS cancellation ignored")
	}
}
