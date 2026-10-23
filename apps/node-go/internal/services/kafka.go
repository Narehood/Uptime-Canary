package services

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

type KafkaConfig struct {
	Brokers                []string         `json:"brokers"`
	Mode                   string           `json:"mode"` // metadata or produce; never implicit
	Topic                  string           `json:"topic,omitempty"`
	Message                *string          `json:"message,omitempty"`
	AllowAutoTopicCreation bool             `json:"allowAutoTopicCreation,omitempty"`
	TLSMode                string           `json:"tlsMode,omitempty"`
	TLS                    *TLSConfig       `json:"tls,omitempty"`
	SASL                   *KafkaSASLConfig `json:"sasl,omitempty"`
}

type KafkaSASLConfig struct {
	Mechanism string `json:"mechanism"` // plain, scram-sha-256, scram-sha-512
	Username  string `json:"username"`
	Password  string `json:"password"`
}

func (c KafkaConfig) validate() error {
	if len(c.Brokers) == 0 || len(c.Brokers) > 16 {
		return errors.New("Kafka requires 1..16 explicit brokers")
	}
	seen := make(map[string]bool, len(c.Brokers))
	for _, b := range c.Brokers {
		if validAddress(b) != nil || seen[b] {
			return errors.New("invalid or duplicate Kafka broker")
		}
		seen[b] = true
	}
	switch c.Mode {
	case "metadata":
		if c.Topic != "" || c.Message != nil || c.AllowAutoTopicCreation {
			return errors.New("Kafka metadata mode forbids produce options")
		}
	case "produce":
		if len(c.Topic) == 0 || len(c.Topic) > 249 || c.Topic == "." || c.Topic == ".." || c.Message == nil || len(*c.Message) > 64<<10 {
			return errors.New("Kafka produce requires topic and explicit message of at most 64 KiB")
		}
		for _, ch := range c.Topic {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
				return errors.New("invalid Kafka topic")
			}
		}
	default:
		return errors.New("Kafka mode must be metadata or produce")
	}
	if err := validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	if c.TLSMode != "plain" {
		h, _, _ := net.SplitHostPort(c.Brokers[0])
		if _, err := tlsSettings(c.TLS, h); err != nil {
			return err
		}
	}
	if s := c.SASL; s != nil {
		if s.Mechanism != "plain" && s.Mechanism != "scram-sha-256" && s.Mechanism != "scram-sha-512" {
			return errors.New("unsupported Kafka SASL mechanism")
		}
		if s.Username == "" || s.Password == "" || len(s.Username) > 1024 || len(s.Password) > 4096 || strings.ContainsRune(s.Username+s.Password, 0) {
			return errors.New("Kafka SASL requires bounded explicit username and password")
		}
	}
	return nil
}

// Bound the upstream SCRAM KDF before it starts. Closing the socket cannot
// interrupt an already running PBKDF2 operation.
type boundedKafkaSASL struct{ sasl.Mechanism }
type boundedKafkaSession struct {
	sasl.Session
	ctx   context.Context
	scram bool
	step  int
}

func (m boundedKafkaSASL) Authenticate(ctx context.Context, host string) (sasl.Session, []byte, error) {
	s, b, err := m.Mechanism.Authenticate(ctx, host)
	return &boundedKafkaSession{Session: s, ctx: ctx, scram: strings.HasPrefix(m.Name(), "SCRAM-")}, b, err
}
func (s *boundedKafkaSession) Challenge(b []byte) (bool, []byte, error) {
	if s.ctx.Err() != nil || len(b) > 8192 {
		return false, nil, errors.New("Kafka SASL challenge limit")
	}
	if s.scram && s.step == 0 {
		parts := strings.Split(string(b), ",")
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "i=") {
			return false, nil, errors.New("invalid Kafka SCRAM challenge")
		}
		n, err := strconv.Atoi(strings.TrimPrefix(parts[2], "i="))
		if err != nil || n < 4096 || n > 100000 {
			return false, nil, errors.New("Kafka SCRAM iteration limit")
		}
	}
	s.step++
	return s.Session.Challenge(b)
}

func checkKafka(ctx context.Context, c KafkaConfig) (result Result) {
	defer func() {
		if recover() != nil {
			result = down("Kafka driver rejected a malformed reply")
		}
	}()
	// Metadata may choose leaders, but cannot expand the configured endpoint set.
	var mu sync.Mutex
	attempts := make(map[string]int, len(c.Brokers))
	for _, b := range c.Brokers {
		attempts[b] = 0
	}
	dialer := func(_ context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		n, ok := attempts[address]
		if ok {
			attempts[address] = n + 1
		}
		mu.Unlock()
		if !ok || n >= 4 || network != "tcp" {
			return nil, errors.New("Kafka endpoint or connection limit")
		}
		conn, err := dialService(ctx, address, c.TLSMode, c.TLS)
		if err != nil {
			return nil, err
		}
		return &serviceDeadlineConn{Conn: conn, ctx: ctx}, nil
	}
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return down("Kafka check timed out")
	}
	opts := []kgo.Opt{kgo.SeedBrokers(c.Brokers...), kgo.ClientID("uptime-canary"), kgo.Dialer(dialer),
		kgo.BrokerMaxReadBytes(maxBody), kgo.BrokerMaxWriteBytes(maxBody), kgo.FetchMaxBytes(maxBody), kgo.ProducerBatchMaxBytes(128 << 10), kgo.RequestRetries(0),
		kgo.RetryTimeout(remaining), kgo.RequestTimeoutOverhead(100 * time.Millisecond), kgo.DisableIdempotentWrite(),
		kgo.RequiredAcks(kgo.AllISRAcks()), kgo.RecordRetries(0), kgo.ProduceRequestTimeout(max(remaining, 100*time.Millisecond)),
		kgo.RecordDeliveryTimeout(max(remaining, time.Second)), kgo.ProducerBatchCompression(kgo.NoCompression()),
		kgo.MaxBufferedRecords(1), kgo.MaxBufferedBytes(128 << 10), kgo.MetadataMaxAge(time.Hour)}
	if c.AllowAutoTopicCreation {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}
	if s := c.SASL; s != nil {
		var mechanism sasl.Mechanism
		switch s.Mechanism {
		case "plain":
			mechanism = plain.Auth{User: s.Username, Pass: s.Password}.AsMechanism()
		case "scram-sha-256":
			mechanism = scram.Auth{User: s.Username, Pass: s.Password}.AsSha256Mechanism()
		case "scram-sha-512":
			mechanism = scram.Auth{User: s.Username, Pass: s.Password}.AsSha512Mechanism()
		}
		opts = append(opts, kgo.SASL(boundedKafkaSASL{mechanism}))
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return down("Kafka driver configuration failed")
	}
	defer client.Close()
	// Ask for no topics: cluster metadata/auth availability, without enumerating
	// the topic catalogue or accidentally causing topic creation.
	request := kmsg.NewPtrMetadataRequest()
	request.Topics = []kmsg.MetadataRequestTopic{}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		return down("Kafka connection, TLS, authentication, or metadata failed")
	}
	if kafkaMetadataBrokers(c, response) == nil || len(response.Topics) != 0 {
		return down("Kafka metadata is invalid or exceeds limit")
	}
	if c.Mode == "metadata" {
		return up("Kafka authenticated metadata check succeeded; no message produced")
	}
	// Validate the topic metadata as well as the initial cluster response. The
	// high-level producer fetches more metadata internally, beyond the initial
	// validation boundary. A single explicit partition write avoids that hidden
	// refresh path and gives every consumed metadata response the same checks.
	topicRequest := kmsg.NewPtrMetadataRequest()
	topicRequest.Topics = []kmsg.MetadataRequestTopic{{Topic: &c.Topic}}
	topicRequest.AllowAutoTopicCreation = c.AllowAutoTopicCreation
	topicResponse, err := topicRequest.RequestWith(ctx, client)
	brokers := kafkaMetadataBrokers(c, topicResponse)
	if err != nil || brokers == nil || len(topicResponse.Topics) != 1 {
		return down("Kafka topic metadata is invalid or unavailable")
	}
	topic := topicResponse.Topics[0]
	if topic.Topic == nil || *topic.Topic != c.Topic || topic.ErrorCode != 0 || len(topic.Partitions) < 1 || len(topic.Partitions) > 1024 {
		return down("Kafka topic metadata is invalid or unavailable")
	}
	partition := int32(-1)
	leader := int32(-1)
	partitions := map[int32]bool{}
	for _, p := range topic.Partitions {
		if p.Partition < 0 || partitions[p.Partition] || p.ErrorCode != 0 || !brokers[p.Leader] || !kafkaReplicaIDs(p.Replicas, brokers) || !kafkaReplicaIDs(p.ISR, brokers) || !slices.Contains(p.Replicas, p.Leader) || !slices.Contains(p.ISR, p.Leader) {
			return down("Kafka partition metadata is invalid or unavailable")
		}
		for _, id := range p.ISR {
			if !slices.Contains(p.Replicas, id) {
				return down("Kafka partition metadata is invalid or unavailable")
			}
		}
		partitions[p.Partition] = true
		if partition == -1 || p.Partition < partition {
			partition, leader = p.Partition, p.Leader
		}
	}
	produce := kmsg.NewPtrProduceRequest()
	produce.Acks = -1
	produce.TimeoutMillis = int32(max(1, time.Until(deadline).Milliseconds()))
	produce.Topics = []kmsg.ProduceRequestTopic{{Topic: c.Topic, Partitions: []kmsg.ProduceRequestTopicPartition{{Partition: partition, Records: kafkaSingleRecord(*c.Message)}}}}
	raw, err := client.Broker(int(leader)).Request(ctx, produce)
	ack, ok := raw.(*kmsg.ProduceResponse)
	if err != nil || !ok || ack == nil || len(ack.Topics) != 1 || ack.Topics[0].Topic != c.Topic || len(ack.Topics[0].Partitions) != 1 {
		return down("Kafka produce failed or acknowledgement was not received")
	}
	p := ack.Topics[0].Partitions[0]
	if p.Partition != partition || p.ErrorCode != 0 || p.BaseOffset < 0 {
		return down("Kafka produce failed or acknowledgement was not received")
	}
	return up("Kafka message acknowledged by all in-sync replicas")
}

func kafkaMetadataBrokers(c KafkaConfig, response *kmsg.MetadataResponse) map[int32]bool {
	if response == nil || response.ErrorCode != 0 || len(response.Brokers) < 1 || len(response.Brokers) > 16 {
		return nil
	}
	ids := make(map[int32]bool, len(response.Brokers))
	for _, b := range response.Brokers {
		address := net.JoinHostPort(b.Host, strconv.Itoa(int(b.Port)))
		if b.NodeID < 0 || ids[b.NodeID] || validAddress(address) != nil || !slices.Contains(c.Brokers, address) {
			return nil
		}
		ids[b.NodeID] = true
	}
	return ids
}

func kafkaReplicaIDs(ids []int32, brokers map[int32]bool) bool {
	if len(ids) == 0 || len(ids) > 16 {
		return false
	}
	seen := map[int32]bool{}
	for _, id := range ids {
		if !brokers[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func kafkaSingleRecord(message string) []byte {
	record := kmsg.Record{Value: []byte(message)}
	// AppendTo encodes the supplied length; the record's initial zero length
	// takes one byte. Measure its following fields, then encode the final size.
	record.Length = int32(len(record.AppendTo(nil)) - 1)
	records := record.AppendTo(nil)
	now := time.Now().UnixMilli()
	batch := kmsg.RecordBatch{Length: int32(49 + len(records)), PartitionLeaderEpoch: -1, Magic: 2, FirstTimestamp: now, MaxTimestamp: now, ProducerID: -1, ProducerEpoch: -1, FirstSequence: -1, NumRecords: 1, Records: records}
	encoded := batch.AppendTo(nil)
	binary.BigEndian.PutUint32(encoded[17:21], crc32.Checksum(encoded[21:], crc32.MakeTable(crc32.Castagnoli)))
	return encoded
}

// Drivers may try to clear/extend socket deadlines. Preserve the total check
// deadline even when their own context or idle timeout differs.
type serviceDeadlineConn struct {
	net.Conn
	ctx context.Context
}

func (c *serviceDeadlineConn) clamp(t time.Time) time.Time {
	if d, ok := c.ctx.Deadline(); ok && (t.IsZero() || d.Before(t)) {
		return d
	}
	return t
}
func (c *serviceDeadlineConn) SetDeadline(t time.Time) error { return c.Conn.SetDeadline(c.clamp(t)) }
func (c *serviceDeadlineConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(c.clamp(t))
}
func (c *serviceDeadlineConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(c.clamp(t))
}
