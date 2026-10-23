package services

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// Review fixtures intentionally assert the desired contracts. A failing review
// test is an unresolved defect, not an expected-failure waiver. All peers bind
// private ephemeral loopback ports; no production hook or dependency is changed.
func reviewListener(t *testing.T, handler func(net.Conn, string) error) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var connections []net.Conn
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 64; i++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				if err := handler(c, ln.Addr().String()); err != nil && !reviewDisconnect(err) {
					t.Errorf("review peer: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		mu.Lock()
		for _, c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}

func reviewDisconnect(err error) bool {
	var ne net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) || errors.As(err, &ne)
}

type reviewKafkaPeer struct {
	mutation string
	trap     string
	metadata atomic.Int32
	produces atomic.Int32
	dials    atomic.Int32
}

func (p *reviewKafkaPeer) serve(c net.Conn, address string) error {
	p.dials.Add(1)
	for {
		var header [4]byte
		if _, err := io.ReadFull(c, header[:]); err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint32(header[:]))
		if n < 10 || n > 128<<10 {
			return errors.New("review request size")
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c, body); err != nil {
			return err
		}
		key := int16(binary.BigEndian.Uint16(body))
		version := int16(binary.BigEndian.Uint16(body[2:]))
		request := kmsg.RequestForKey(key)
		if request == nil {
			return fmt.Errorf("unexpected review API %d", key)
		}
		request.SetVersion(version)
		clientLen := int(int16(binary.BigEndian.Uint16(body[8:])))
		offset := 10 + clientLen
		if clientLen < 0 || offset > len(body) {
			return errors.New("review client ID length")
		}
		if request.IsFlexible() {
			if offset >= len(body) || body[offset] != 0 {
				return errors.New("review header tags")
			}
			offset++
		}
		if err := request.ReadFrom(body[offset:]); err != nil {
			return err
		}
		response := request.ResponseKind()
		response.SetVersion(version)
		switch req := request.(type) {
		case *kmsg.ApiVersionsRequest:
			r := response.(*kmsg.ApiVersionsResponse)
			for _, api := range []struct{ key, max int16 }{{18, 3}, {3, 7}, {0, 7}} {
				r.ApiKeys = append(r.ApiKeys, kmsg.ApiVersionsResponseApiKey{ApiKey: api.key, MaxVersion: api.max})
			}
		case *kmsg.MetadataRequest:
			p.metadata.Add(1)
			r := response.(*kmsg.MetadataResponse)
			host, port, _ := net.SplitHostPort(address)
			portNumber, _ := strconv.Atoi(port)
			r.Brokers = []kmsg.MetadataResponseBroker{{NodeID: 1, Host: host, Port: int32(portNumber)}}
			r.ControllerID = 1
			if len(req.Topics) > 0 {
				if len(req.Topics) != 1 || req.Topics[0].Topic == nil || *req.Topics[0].Topic != "canary-fixture" || req.AllowAutoTopicCreation {
					return errors.New("unexpected review topic metadata request")
				}
				leader := int32(1)
				switch p.mutation {
				case "17-brokers":
					for id := int32(2); id <= 17; id++ {
						r.Brokers = append(r.Brokers, kmsg.MetadataResponseBroker{NodeID: id, Host: host, Port: int32(portNumber)})
					}
				case "duplicate-id":
					r.Brokers = append(r.Brokers, r.Brokers[0])
				case "unused-unconfigured", "leader-unconfigured":
					trapHost, trapPort, _ := net.SplitHostPort(p.trap)
					trapNumber, _ := strconv.Atoi(trapPort)
					r.Brokers = append(r.Brokers, kmsg.MetadataResponseBroker{NodeID: 2, Host: trapHost, Port: int32(trapNumber)})
					if p.mutation == "leader-unconfigured" {
						leader = 2
					}
				}
				r.Topics = []kmsg.MetadataResponseTopic{{Topic: req.Topics[0].Topic, Partitions: []kmsg.MetadataResponseTopicPartition{{Partition: 0, Leader: leader, Replicas: []int32{leader}, ISR: []int32{leader}}}}}
			}
		case *kmsg.ProduceRequest:
			if req.Acks != -1 || len(req.Topics) != 1 || req.Topics[0].Topic != "canary-fixture" || len(req.Topics[0].Partitions) != 1 || req.Topics[0].Partitions[0].Partition != 0 {
				return errors.New("review produce was not an all-ISR write to the selected partition")
			}
			var batch kmsg.RecordBatch
			if err := batch.ReadFrom(req.Topics[0].Partitions[0].Records); err != nil || batch.NumRecords != 1 {
				return errors.New("review produce missing actual record")
			}
			var record kmsg.Record
			if err := record.ReadFrom(batch.Records); err != nil || string(record.Value) != "review-message" {
				return errors.New("review record value mismatch")
			}
			p.produces.Add(1)
			r := response.(*kmsg.ProduceResponse)
			partition := int32(0)
			if p.mutation == "wrong-ack-partition" {
				partition = 1
			}
			if p.mutation != "empty-ack" {
				r.Topics = []kmsg.ProduceResponseTopic{{Topic: "canary-fixture", Partitions: []kmsg.ProduceResponseTopicPartition{{Partition: partition, BaseOffset: 42}}}}
			}
		default:
			return fmt.Errorf("unexpected review request %T", req)
		}
		out := append([]byte(nil), body[4:8]...)
		if response.IsFlexible() && key != 18 {
			out = append(out, 0)
		}
		out = response.AppendTo(out)
		packet := binary.BigEndian.AppendUint32(nil, uint32(len(out)))
		packet = append(packet, out...)
		if _, err := c.Write(packet); err != nil {
			return err
		}
	}
}

func TestKafkaReviewEveryMetadataResponseIsValidated(t *testing.T) {
	for _, mutation := range []string{"17-brokers", "duplicate-id", "unused-unconfigured", "leader-unconfigured"} {
		t.Run(mutation, func(t *testing.T) {
			var trapDials atomic.Int32
			trap := reviewListener(t, func(c net.Conn, _ string) error {
				trapDials.Add(1)
				return nil
			})
			peer := &reviewKafkaPeer{mutation: mutation, trap: trap}
			address := reviewListener(t, peer.serve)
			c := kafkaConfig(address)
			c.Timeout = 400 * time.Millisecond
			c.Kafka.Mode, c.Kafka.Topic = "produce", "canary-fixture"
			message := "review-message"
			c.Kafka.Message = &message
			r := Check(context.Background(), c)
			if peer.metadata.Load() < 2 {
				t.Fatal("fixture did not reach the post-validation topic metadata response")
			}
			if trapDials.Load() != 0 {
				t.Error("dialed unconfigured advertised endpoint")
			}
			if r.State != "down" || peer.produces.Load() != 0 {
				t.Errorf("invalid later metadata must fail before producing: result=%+v produces=%d metadata=%d", r, peer.produces.Load(), peer.metadata.Load())
			}
			if peer.dials.Load() > 4 {
				t.Errorf("per-address connection bound exceeded: %d", peer.dials.Load())
			}
		})
	}
}

func TestKafkaReviewAcknowledgementMustIdentifyProducedPartition(t *testing.T) {
	for _, mutation := range []string{"", "empty-ack", "wrong-ack-partition"} {
		t.Run(mutation, func(t *testing.T) {
			peer := &reviewKafkaPeer{mutation: mutation}
			address := reviewListener(t, peer.serve)
			c := kafkaConfig(address)
			c.Timeout = 250 * time.Millisecond
			c.Kafka.Mode, c.Kafka.Topic = "produce", "canary-fixture"
			message := "review-message"
			c.Kafka.Message = &message
			want := "down"
			if mutation == "" {
				want = "up"
			}
			expectState(t, c, want)
			if peer.produces.Load() != 1 {
				t.Errorf("expected exactly one actual produce, got %d", peer.produces.Load())
			}
		})
	}
}

// Alter a complete bounded packet from the existing authenticated 11g peer.
// This reuses its successful authentication, not its expected-result assertions.
type reviewOracleConn struct {
	net.Conn
	transform func(byte, []byte) []byte
}

func (c *reviewOracleConn) Write(b []byte) (int, error) {
	if len(b) < 8 || int(binary.BigEndian.Uint16(b)) != len(b) {
		return 0, errors.New("review Oracle packet framing")
	}
	body := c.transform(b[4], b[8:])
	if len(body) > 65527 {
		return 0, errors.New("review Oracle replacement packet too large")
	}
	err := tnsWrite(c.Conn, b[4], body)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func reviewOracleFixture(t *testing.T, transform func(byte, []byte) []byte) string {
	t.Helper()
	return reviewListener(t, func(c net.Conn, _ string) error {
		return serveOracleFixture(&reviewOracleConn{Conn: c, transform: transform}, oracleFixtureOptions{value: []byte("private-value")})
	})
}

func TestOracleReviewMalformedQueryAndLateErrorFailClosed(t *testing.T) {
	for _, fault := range []string{"negative-column-count", "late-error"} {
		t.Run(fault, func(t *testing.T) {
			var changed atomic.Int32
			address := reviewOracleFixture(t, func(kind byte, body []byte) []byte {
				if kind != 6 || len(body) < 3 || body[2] != 16 {
					return body
				}
				changed.Add(1)
				if fault == "negative-column-count" {
					// TTC describe: zero prefix, zero max row size, signed -1 columns.
					return []byte{0, 0, 16, 0, 0, 0x81, 1}
				}
				ending := oraSummary(1403, 1)
				if !bytes.HasSuffix(body, ending) {
					t.Error("review expected terminal row summary")
					return body
				}
				return append(append([]byte(nil), body[:len(body)-len(ending)]...), oraSummary(942, 1)...)
			})
			c := oracleConfig(address)
			c.Oracle.Condition = &Condition{Operator: "eq", Expected: json.RawMessage(`"private-value"`)}
			r := expectState(t, c, "down")
			if changed.Load() != 1 {
				t.Fatal("review query mutation was not consumed")
			}
			if fault == "negative-column-count" && r.Message != "Oracle driver rejected a malformed reply" {
				t.Errorf("unexpected malformed query outcome: %+v", r)
			}
		})
	}
}

func reviewWrongHostTLS(t *testing.T) (*tls.Config, *TLSConfig) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(22), Subject: pkix.Name{CommonName: "review-only.invalid"}, DNSNames: []string{"review-only.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, &TLSConfig{CAPEM: string(certPEM)}
}

func TestKafkaOracleReviewTrustedWrongHostnameSendsNoApplicationBytes(t *testing.T) {
	for _, typ := range []Type{Kafka, Oracle} {
		t.Run(string(typ), func(t *testing.T) {
			serverTLS, clientTLS := reviewWrongHostTLS(t)
			var applicationBytes atomic.Int32
			address := reviewListener(t, func(c net.Conn, _ string) error {
				tc := tls.Server(c, serverTLS)
				defer tc.Close()
				if tc.Handshake() != nil {
					return nil
				}
				var b [1]byte
				n, _ := tc.Read(b[:])
				applicationBytes.Add(int32(n))
				return nil
			})
			c := kafkaConfig(address)
			c.Kafka.TLSMode, c.Kafka.TLS = "tls", clientTLS
			if typ == Oracle {
				c = oracleConfig(address)
				c.Oracle.TLSMode, c.Oracle.TLS = "tls", clientTLS
			}
			expectState(t, c, "down")
			if applicationBytes.Load() != 0 {
				t.Error("application bytes sent despite hostname mismatch")
			}
		})
	}
}

// Run CPU-risk probes in a deadline-bounded child of this test executable. The
// child uses only 250,000 SHA-512 rounds, never the upstream 100,000,000 limit.
func TestOracleReviewAuthenticationCancellation(t *testing.T) {
	if os.Getenv("CANARY_ORACLE_REVIEW_CHILD") == "auth-cancel" {
		reviewOracleAuthenticationCancellationChild(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOracleReviewAuthenticationCancellation$", "-test.count=1", "-test.timeout=3s", "-test.v")
	cmd.Env = append(os.Environ(), "CANARY_ORACLE_REVIEW_CHILD=auth-cancel", "GOMAXPROCS=2")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("bounded auth child exceeded its 4s limit: %s", out)
	}
	if err != nil {
		t.Fatalf("authentication cancellation contract failed in bounded child (%v):\n%s", err, out)
	}
}

func reviewOracleAuthenticationCancellationChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var challenged atomic.Int32
	canceled := make(chan time.Time, 1)
	address := reviewOracleFixture(t, func(kind byte, body []byte) []byte {
		if kind != 6 || !bytes.Contains(body, []byte("AUTH_VFR_DATA")) {
			return body
		}
		challenged.Add(1)
		auth := append([]byte{0, 0, 8}, oraInt(4)...)
		auth = append(auth, oraKV("AUTH_SESSKEY", strings.Repeat("00", 48), 0)...)
		auth = append(auth, oraKV("AUTH_VFR_DATA", strings.Repeat("01", 16), 18453)...)
		auth = append(auth, oraKV("AUTH_PBKDF2_VGEN_COUNT", "250000", 0)...)
		auth = append(auth, oraKV("AUTH_PBKDF2_CSK_SALT", strings.Repeat("02", 16), 0)...)
		auth = append(auth, oraSummary(0, 0)...)
		time.AfterFunc(10*time.Millisecond, func() {
			canceled <- time.Now()
			cancel()
		})
		return auth
	})
	r := Check(ctx, oracleConfig(address))
	if challenged.Load() != 1 {
		t.Fatal("modern authentication challenge not reached")
	}
	select {
	case at := <-canceled:
		delay := time.Since(at)
		t.Logf("check returned %s after cancellation; result=%+v", delay, r)
		if delay > 50*time.Millisecond {
			t.Errorf("Oracle authentication continued CPU work beyond 50ms cancellation grace: %s", delay)
		}
		if r.State != "down" || r.Message != "service check canceled" {
			t.Errorf("unexpected canceled result: %+v", r)
		}
	default:
		t.Fatal("fixture did not keep authentication busy until cancellation")
	}
}
