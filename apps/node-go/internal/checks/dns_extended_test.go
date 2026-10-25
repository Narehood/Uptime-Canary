package checks

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestDNSExtendedCanonicalAdmissionAndTCPFallback(t *testing.T) {
	server, count, _ := fixtureDNS(t, true, false)
	for _, kind := range []string{"SRV", "SOA", "CAA"} {
		c := Config{Type: DNS, Target: "fixture.invalid", Timeout: time.Second, DNS: &DNSConfig{RecordType: kind, Server: server}}
		requireState(t, Check(context.Background(), c), Up)
		if kind != "SRV" {
			c.DNS.Server = ""
			if Validate(c) == nil {
				t.Fatal("raw DNS record silently selected a resolver")
			}
		}
	}
	if count.Load() < 3 {
		t.Fatal("truncated extended queries did not retry TCP")
	}
	for _, pair := range [][2]string{{"SRV", "0 0 65536 host.invalid"}, {"SRV", "0 -1 80 host.invalid"}, {"SOA", "ns.invalid host.invalid 4294967296 1 1 1 1"}, {"CAA", `256 issue "value"`}, {"CAA", `0 bad-tag "value"`}, {"CAA", `0 issue "unfinished`}} {
		if _, ok := canonicalRecord(pair[0], pair[1]); ok {
			t.Fatalf("accepted malformed %s expectation", pair[0])
		}
	}
	if value, ok := canonicalRecord("CAA", `0 ISSUE letsencrypt.org`); !ok || value != `0 issue "letsencrypt.org"` {
		t.Fatal("CAA presentation was not canonical")
	}
}

func TestDNSExtendedRejectsUnrelatedAndMalformedEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "alias", "wrong-id", "query", "bad-question", "foreign-owner", "alias-loop", "malformed-tag", "invalid-value", "nxdomain"} {
		t.Run(mode, func(t *testing.T) {
			server, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				buf := make([]byte, 4096)
				n, peer, err := server.ReadFrom(buf)
				if err != nil {
					return
				}
				var query dnsmessage.Message
				if query.Unpack(buf[:n]) != nil || len(query.Questions) != 1 {
					return
				}
				name := query.Questions[0].Name
				message := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true}, Questions: query.Questions}
				if mode == "wrong-id" {
					message.ID++
				}
				if mode == "query" {
					message.Response = false
				}
				if mode == "bad-question" {
					message.Questions[0].Type = dnsmessage.TypeA
				}
				if mode == "nxdomain" {
					message.RCode = dnsmessage.RCodeNameError
				}
				other, _ := dnsmessage.NewName("other.fixture.invalid.")
				if mode == "alias" || mode == "alias-loop" {
					message.Answers = append(message.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET}, Body: &dnsmessage.CNAMEResource{CNAME: other}})
					if mode == "alias-loop" {
						message.Answers = append(message.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: other, Class: dnsmessage.ClassINET}, Body: &dnsmessage.CNAMEResource{CNAME: name}})
					}
					name = other
				}
				if mode == "foreign-owner" {
					name = other
				}
				data := append([]byte{0, 5}, []byte("issueletsencrypt.org")...)
				if mode == "malformed-tag" {
					data[1] = 250
				}
				if mode == "invalid-value" {
					data = append(data, 0xff)
				}
				message.Answers = append(message.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET}, Body: &dnsmessage.UnknownResource{Type: dnsmessage.Type(257), Data: data}})
				raw, err := message.Pack()
				if err == nil {
					_, _ = server.WriteTo(raw, peer)
				}
			}()
			t.Cleanup(func() { server.Close(); wg.Wait() })
			c := Config{Type: DNS, Target: "fixture.invalid", Timeout: time.Second, DNS: &DNSConfig{RecordType: "CAA", Server: server.LocalAddr().String(), Expected: []string{`0 issue "letsencrypt.org"`}}}
			want := Down
			if mode == "valid" || mode == "alias" {
				want = Up
			}
			requireState(t, Check(context.Background(), c), want)
		})
	}
}

func TestDNSExtendedCancellationClosesOutstandingQuery(t *testing.T) {
	server, _, entered := fixtureDNS(t, false, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		done <- Check(ctx, Config{Type: DNS, Target: "fixture.invalid", Timeout: time.Second, DNS: &DNSConfig{RecordType: "CAA", Server: server}})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("query never entered private fixture")
	}
	cancel()
	select {
	case result := <-done:
		requireState(t, result, Down)
	case <-time.After(time.Second):
		t.Fatal("canceled DNS query retained its socket")
	}
}
