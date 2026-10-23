package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
	xscram "github.com/xdg-go/scram"
)

type kafkaFixtureOptions struct {
	tls         *tls.Config
	auth        string
	produceCode int16
	fault       string
	message     string
	auto        bool
}
type kafkaFixtureStats struct{ produces, metadata, auth atomic.Int32 }

// A loopback wire peer, not an injected successful client. It independently
// decodes each Kafka request/header/batch/CRC/record and only acks valid writes.
func kafkaFixture(t *testing.T, o kafkaFixtureOptions) (string, *kafkaFixtureStats) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stats := new(kafkaFixtureStats)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var connections []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
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
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				if o.tls != nil {
					tc := tls.Server(c, o.tls)
					if tc.Handshake() != nil {
						return
					}
					c = tc
				}
				if err := serveKafkaFixture(c, ln.Addr().String(), o, stats); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					var ne net.Error
					if !errors.As(err, &ne) {
						t.Errorf("Kafka wire fixture: %v", err)
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
		mu.Lock()
		for _, c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String(), stats
}

func serveKafkaFixture(c net.Conn, address string, o kafkaFixtureOptions, stats *kafkaFixtureStats) error {
	authenticated := o.auth == ""
	var conversation *xscram.ServerConversation
	if strings.HasPrefix(o.auth, "scram-") {
		hash := xscram.SHA256
		if o.auth == "scram-sha-512" {
			hash = xscram.SHA512
		}
		client, err := hash.NewClient("fixture-user", "fixture-secret", "")
		if err != nil {
			return err
		}
		credentials := client.GetStoredCredentials(xscram.KeyFactors{Salt: "independent-fixture-salt", Iters: 4096})
		server, err := hash.NewServer(func(user string) (xscram.StoredCredentials, error) {
			if user != "fixture-user" {
				return xscram.StoredCredentials{}, errors.New("unknown fixture user")
			}
			return credentials, nil
		})
		if err != nil {
			return err
		}
		conversation = server.NewConversation()
	}
	for {
		var header [4]byte
		if _, err := io.ReadFull(c, header[:]); err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint32(header[:]))
		if n < 10 || n > maxBody {
			return errors.New("invalid request length")
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		key := int16(binary.BigEndian.Uint16(b))
		version := int16(binary.BigEndian.Uint16(b[2:]))
		correlation := binary.BigEndian.Uint32(b[4:])
		request := kmsg.RequestForKey(key)
		if request == nil {
			return fmt.Errorf("unexpected API key %d", key)
		}
		request.SetVersion(version)
		clientLen := int(int16(binary.BigEndian.Uint16(b[8:])))
		if clientLen < 0 || 10+clientLen > len(b) {
			return errors.New("invalid client ID")
		}
		offset := 10 + clientLen
		if request.IsFlexible() {
			if offset >= len(b) || b[offset] != 0 {
				return errors.New("unexpected header tags")
			}
			offset++
		}
		if err := request.ReadFrom(b[offset:]); err != nil {
			return err
		}
		response := request.ResponseKind()
		response.SetVersion(version)
		switch req := request.(type) {
		case *kmsg.ApiVersionsRequest:
			r := response.(*kmsg.ApiVersionsResponse)
			for _, api := range []struct{ key, max int16 }{{18, 3}, {17, 1}, {36, 1}, {3, 7}, {0, 7}} {
				r.ApiKeys = append(r.ApiKeys, kmsg.ApiVersionsResponseApiKey{ApiKey: api.key, MaxVersion: api.max})
			}
		case *kmsg.SASLHandshakeRequest:
			r := response.(*kmsg.SASLHandshakeResponse)
			r.SupportedMechanisms = []string{strings.ToUpper(o.auth)}
			if req.Mechanism != strings.ToUpper(o.auth) {
				r.ErrorCode = 33
			}
		case *kmsg.SASLAuthenticateRequest:
			stats.auth.Add(1)
			r := response.(*kmsg.SASLAuthenticateResponse)
			if o.auth == "plain" {
				authenticated = bytes.Equal(req.SASLAuthBytes, []byte("\x00fixture-user\x00fixture-secret"))
			} else if conversation != nil {
				out, err := conversation.Step(string(req.SASLAuthBytes))
				r.SASLAuthBytes = []byte(out)
				if err != nil {
					r.ErrorCode = 58
				}
				authenticated = conversation.Done() && conversation.Valid()
				if o.fault == "scram-cost" {
					r.SASLAuthBytes = []byte(strings.Replace(out, "i=4096", "i=100000001", 1))
				}
			} else {
				return errors.New("unexpected auth")
			}
			if o.auth == "plain" && !authenticated {
				r.ErrorCode = 58
			}
			if r.ErrorCode != 0 {
				msg := "fixture-secret private-value authentication failed"
				r.ErrorMessage = &msg
			}
		case *kmsg.MetadataRequest:
			if !authenticated {
				return errors.New("metadata before authentication")
			}
			stats.metadata.Add(1)
			if o.fault == "malformed" {
				_, err := c.Write([]byte{0x7f, 0xff, 0xff, 0xff})
				return err
			}
			if o.fault == "timeout" {
				_, _ = io.Copy(io.Discard, c)
				return nil
			}
			r := response.(*kmsg.MetadataResponse)
			h, p, _ := net.SplitHostPort(address)
			port, _ := strconv.Atoi(p)
			r.Brokers = []kmsg.MetadataResponseBroker{{NodeID: 1, Host: h, Port: int32(port)}}
			r.ControllerID = 1
			if o.fault == "broker-limit" {
				for i := 2; i <= 17; i++ {
					r.Brokers = append(r.Brokers, kmsg.MetadataResponseBroker{NodeID: int32(i), Host: h, Port: int32(port)})
				}
			}
			for _, topic := range req.Topics {
				if topic.Topic == nil || *topic.Topic != "canary-fixture" || req.AllowAutoTopicCreation != o.auto {
					return errors.New("incorrect topic metadata or auto-creation flag")
				}
				r.Topics = append(r.Topics, kmsg.MetadataResponseTopic{Topic: topic.Topic, Partitions: []kmsg.MetadataResponseTopicPartition{{Partition: 0, Leader: 1, Replicas: []int32{1}, ISR: []int32{1}}}})
			}
		case *kmsg.ProduceRequest:
			if !authenticated || req.Acks != -1 || len(req.Topics) != 1 || req.Topics[0].Topic != "canary-fixture" || len(req.Topics[0].Partitions) != 1 {
				return errors.New("invalid produce or authentication")
			}
			partition := req.Topics[0].Partitions[0]
			if partition.Partition != 0 {
				return errors.New("wrong partition")
			}
			var batch kmsg.RecordBatch
			if err := batch.ReadFrom(partition.Records); err != nil {
				return err
			}
			if len(partition.Records) < 21 || crc32.Checksum(partition.Records[21:], crc32.MakeTable(crc32.Castagnoli)) != uint32(batch.CRC) || batch.NumRecords != 1 || batch.Attributes&7 != 0 {
				return errors.New("invalid record batch CRC/count/compression")
			}
			var record kmsg.Record
			if err := record.ReadFrom(batch.Records); err != nil {
				return err
			}
			if record.Key != nil || string(record.Value) != o.message {
				return errors.New("incorrect produced message")
			}
			stats.produces.Add(1)
			if o.fault == "no-ack" {
				_, _ = io.Copy(io.Discard, c)
				return nil
			}
			response.(*kmsg.ProduceResponse).Topics = []kmsg.ProduceResponseTopic{{Topic: "canary-fixture", Partitions: []kmsg.ProduceResponseTopicPartition{{Partition: 0, ErrorCode: o.produceCode, BaseOffset: 42}}}}
		default:
			return fmt.Errorf("unexpected decoded request %T", req)
		}
		out := binary.BigEndian.AppendUint32(nil, correlation)
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

func kafkaConfig(address string) Config {
	return Config{Type: Kafka, Timeout: 2 * time.Second, Kafka: &KafkaConfig{Brokers: []string{address}, Mode: "metadata", TLSMode: "plain"}}
}

func TestKafkaWireMetadataAndAcknowledgedProduce(t *testing.T) {
	for _, auth := range []string{"", "plain", "scram-sha-256", "scram-sha-512"} {
		for _, mode := range []string{"metadata", "produce"} {
			t.Run(auth+"/"+mode, func(t *testing.T) {
				msg := "private-value fixture message"
				address, stats := kafkaFixture(t, kafkaFixtureOptions{auth: auth, message: msg, auto: true})
				c := kafkaConfig(address)
				c.Kafka.Mode = mode
				if auth != "" {
					c.Kafka.SASL = &KafkaSASLConfig{Mechanism: auth, Username: "fixture-user", Password: "fixture-secret"}
				}
				if mode == "produce" {
					c.Kafka.Topic = "canary-fixture"
					c.Kafka.Message = &msg
					c.Kafka.AllowAutoTopicCreation = true
				}
				r := expectState(t, c, "up")
				want := int32(0)
				if mode == "produce" {
					want = 1
				}
				if stats.produces.Load() != want || stats.metadata.Load() == 0 || (auth != "" && stats.auth.Load() == 0) {
					t.Fatal("missing actual wire exchanges")
				}
				if mode == "metadata" && !strings.Contains(r.Message, "no message produced") {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestKafkaWireFailuresTLSAndCancellation(t *testing.T) {
	for _, fault := range []string{"bad-auth", "produce-refused", "malformed", "broker-limit", "timeout", "no-ack", "scram-cost", "cancel", "untrusted", "tls", "mtls"} {
		t.Run(fault, func(t *testing.T) {
			o := kafkaFixtureOptions{message: ""}
			var clientTLS *TLSConfig
			if fault == "tls" || fault == "untrusted" {
				o.tls, clientTLS = testTLS(t)
			}
			if fault == "mtls" {
				o.tls, clientTLS = specialistMTLS(t)
			}
			if fault == "bad-auth" {
				o.auth = "plain"
			}
			if fault == "scram-cost" {
				o.auth = "scram-sha-256"
				o.fault = fault
			}
			if fault == "produce-refused" {
				o.produceCode = 29
			}
			if fault == "malformed" || fault == "timeout" || fault == "no-ack" || fault == "broker-limit" {
				o.fault = fault
			}
			if fault == "cancel" {
				o.fault = "timeout"
			}
			address, stats := kafkaFixture(t, o)
			c := kafkaConfig(address)
			if clientTLS != nil {
				c.Kafka.TLSMode = "tls"
				c.Kafka.TLS = clientTLS
			}
			if fault == "untrusted" {
				c.Kafka.TLS = nil
			}
			if fault == "bad-auth" {
				c.Kafka.SASL = &KafkaSASLConfig{Mechanism: o.auth, Username: "fixture-user", Password: "private-value wrong"}
			}
			if fault == "scram-cost" {
				c.Kafka.SASL = &KafkaSASLConfig{Mechanism: o.auth, Username: "fixture-user", Password: "fixture-secret"}
			}
			if fault == "produce-refused" || fault == "no-ack" {
				c.Kafka.Mode = "produce"
				c.Kafka.Topic = "canary-fixture"
				c.Kafka.Message = &o.message
			}
			if fault == "timeout" || fault == "no-ack" {
				c.Timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancel" {
				go func() {
					deadline := time.Now().Add(time.Second)
					for stats.metadata.Load() == 0 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					cancel()
				}()
			}
			start := time.Now()
			r := Check(ctx, c)
			want := "down"
			if fault == "tls" || fault == "mtls" {
				want = "up"
			}
			if r.State != want || time.Since(start) > 3*time.Second || strings.Contains(r.Message, "private-value") || strings.Contains(r.Message, "fixture-secret") {
				t.Fatal(r)
			}
			if fault == "cancel" && r.Message != "service check canceled" {
				t.Fatal(r)
			}
			if (fault == "no-ack" || fault == "produce-refused") && stats.produces.Load() != 1 {
				t.Fatal("produce missing or retried")
			}
			if fault == "bad-auth" && stats.metadata.Load() != 0 {
				t.Fatal("authentication bypass")
			}
		})
	}
}

func TestKafkaValidation(t *testing.T) {
	for _, mutate := range []func(*KafkaConfig){
		func(c *KafkaConfig) { c.Mode = "" }, func(c *KafkaConfig) { c.Brokers = nil }, func(c *KafkaConfig) { c.Brokers = make([]string, 17) }, func(c *KafkaConfig) { c.Brokers = append(c.Brokers, c.Brokers[0]) },
		func(c *KafkaConfig) { c.Topic = "secret" }, func(c *KafkaConfig) { c.AllowAutoTopicCreation = true }, func(c *KafkaConfig) { c.Mode = "produce" },
		func(c *KafkaConfig) {
			c.SASL = &KafkaSASLConfig{Mechanism: "aws", Username: "private-value", Password: "fixture-secret"}
		}, func(c *KafkaConfig) { c.SASL = &KafkaSASLConfig{Mechanism: "plain"} },
		func(c *KafkaConfig) { c.TLS = &TLSConfig{CAPEM: "private-value"} },
	} {
		c := kafkaConfig("127.0.0.1:1")
		mutate(c.Kafka)
		err := Validate(c)
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("validation=%v", err)
		}
	}
}
