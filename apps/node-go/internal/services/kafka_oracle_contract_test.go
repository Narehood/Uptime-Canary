package services

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKafkaOracleTypedContractRoundTrip(t *testing.T) {
	message := ""
	kafka := kafkaConfig("127.0.0.1:1")
	kafka.Kafka.Mode = "produce"
	kafka.Kafka.Topic = "canary-fixture"
	kafka.Kafka.Message = &message
	kafka.Kafka.SASL = &KafkaSASLConfig{Mechanism: "plain", Username: "fixture-user", Password: "fixture-secret"}
	oracle := oracleConfig("127.0.0.1:1")
	oracle.Oracle.Query = "SELECT 1 FROM DUAL"
	oracle.Oracle.Condition = &Condition{Operator: "eq", Expected: json.RawMessage(`1`)}
	for _, c := range []Config{kafka, oracle} {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Config
		if err = json.Unmarshal(b, &decoded); err != nil || !reflect.DeepEqual(c, decoded) || Validate(decoded) != nil {
			t.Fatal("typed options did not survive JSON roundtrip", err)
		}
		mixed := decoded
		mixed.Redis = &RedisConfig{Address: "127.0.0.1:1"}
		if Validate(mixed) == nil {
			t.Fatal("accepted mixed old/new service options")
		}
		decoded.Type = Redis
		if Validate(decoded) == nil {
			t.Fatal("accepted mismatched dispatch type")
		}
	}
}

func TestKafkaOracleEarlierDeadlineAndPreCanceled(t *testing.T) {
	for _, typ := range []Type{Kafka, Oracle} {
		t.Run(string(typ), func(t *testing.T) {
			address := fixture(t, func(c net.Conn) error { _, _ = io.Copy(io.Discard, c); return nil })
			c := kafkaConfig(address)
			if typ == Oracle {
				c = oracleConfig(address)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			start := time.Now()
			r := Check(ctx, c)
			cancel()
			if r.State != "down" || time.Since(start) > time.Second || strings.Contains(r.Message, "fixture-secret") {
				t.Fatal(r)
			}
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
			r = Check(ctx, c)
			if r.Message != "service check canceled" {
				t.Fatal(r)
			}
		})
	}
}
