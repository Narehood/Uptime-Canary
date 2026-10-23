package services

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func mqttFixture(t *testing.T, encrypted bool, variant string, payload []byte, early bool) (string, *TLSConfig) {
	tc, trust := testTLS(t)
	address := fixture(t, func(raw net.Conn) error {
		var c net.Conn = raw
		if encrypted {
			sc := tls.Server(raw, tc)
			if sc.Handshake() != nil {
				return nil
			}
			c = sc
		}
		h, b, e := readMQTTPacket(c)
		if e != nil {
			return e
		}
		if h != 0x10 || len(b) < 12 || !bytes.Equal(b[:10], []byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xc2, 0, 0}) {
			return fmt.Errorf("incorrect MQTT CONNECT")
		}
		i := 10
		n := int(binary.BigEndian.Uint16(b[i:]))
		i += 2 + n
		if i > len(b) || !bytes.Equal(b[i:], []byte("\x00\x07monitor\x00\x0efixture-secret")) {
			return fmt.Errorf("incorrect MQTT credentials")
		}
		if variant == "auth-refused" {
			return writeMQTTPacket(c, 0x20, []byte{0, 4})
		}
		if variant == "bad-connack" {
			return writeMQTTPacket(c, 0x20, []byte{1, 0})
		}
		if writeMQTTPacket(c, 0x20, []byte{0, 0}) != nil {
			return nil
		}
		h, b, e = readMQTTPacket(c)
		if e != nil {
			return e
		}
		if h != 0x82 || !bytes.Equal(b, []byte("\x00\x01\x00\x08status/+\x00")) {
			return fmt.Errorf("incorrect MQTT SUBSCRIBE")
		}
		if variant == "sub-refused" {
			return writeMQTTPacket(c, 0x90, []byte{0, 1, 0x80})
		}
		if variant == "bad-suback" {
			return writeMQTTPacket(c, 0x90, []byte{0, 2, 0})
		}
		if variant == "oversized" {
			_, _ = c.Write([]byte{0x30, 0xff, 0xff, 0x7f})
			return nil
		}
		if variant == "no-message" {
			if writeMQTTPacket(c, 0x90, []byte{0, 1, 0}) != nil {
				return nil
			}
			_, _ = io.Copy(io.Discard, c)
			return nil
		}
		pub := append([]byte{0, 11}, []byte("status/node")...)
		pub = append(pub, payload...)
		if variant == "wrong-topic" {
			pub = append([]byte{0, 5}, []byte("other")...)
			pub = append(pub, payload...)
		}
		if variant == "qos1" {
			pub = append([]byte{0, 11}, []byte("status/node")...)
			pub = append(pub, 0, 7)
			pub = append(pub, payload...)
		}
		if variant == "malformed" {
			pub = []byte{0, 100, 'x'}
		}
		publish := func() error {
			header := byte(0x31)
			if variant == "qos1" {
				header = 0x32
			}
			return writeMQTTPacket(c, header, pub)
		}
		if early {
			if e := publish(); e != nil {
				return e
			}
		}
		if e := writeMQTTPacket(c, 0x90, []byte{0, 1, 0}); e != nil {
			return e
		}
		if !early {
			if e := publish(); e != nil {
				return e
			}
		}
		if variant == "qos1" {
			h, b, e = readMQTTPacket(c)
			if e != nil || h != 0x40 || !bytes.Equal(b, []byte{0, 7}) {
				return fmt.Errorf("missing MQTT PUBACK")
			}
		}
		if variant == "success" || variant == "qos1" {
			h, b, e = readMQTTPacket(c)
			if e != nil || h != 0xe0 || len(b) != 0 {
				return fmt.Errorf("missing MQTT DISCONNECT")
			}
		}
		return nil
	})
	return address, trust
}
func TestMQTTSubscribeAuthTLSAndMessages(t *testing.T) {
	for _, tc := range []struct {
		name, payload, op, expected string
		encrypted, early            bool
	}{
		{"equals", "ready", "eq", `"ready"`, false, false},
		{"keyword", "service ready now", "contains", `"ready"`, true, false},
		{"early-retained", "42", "gt", `40`, false, true},
		{"qos1", "ready", "eq", `"ready"`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variant := "success"
			if tc.name == "qos1" {
				variant = "qos1"
			}
			address, trust := mqttFixture(t, tc.encrypted, variant, []byte(tc.payload), tc.early)
			c := &MQTTConfig{Address: address, Username: "monitor", Password: "fixture-secret", Topic: "status/+", TLSMode: "plain", Condition: &Condition{Operator: tc.op, Expected: []byte(tc.expected)}}
			if tc.encrypted {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			expectState(t, Config{Type: MQTT, Timeout: time.Second, MQTT: c}, "up")
		})
	}
}
func TestMQTTRefusalsMalformedTimeoutAndMismatch(t *testing.T) {
	for _, variant := range []string{"auth-refused", "sub-refused", "bad-connack", "bad-suback", "wrong-topic", "malformed", "oversized", "mismatch", "no-message"} {
		t.Run(variant, func(t *testing.T) {
			address, _ := mqttFixture(t, false, variant, []byte("private-value"), false)
			expectState(t, Config{Type: MQTT, Timeout: 100 * time.Millisecond, MQTT: &MQTTConfig{Address: address, Username: "monitor", Password: "fixture-secret", Topic: "status/+", TLSMode: "plain", Condition: &Condition{Operator: "eq", Expected: []byte(`"ready"`)}}}, "down")
		})
	}
	address, _ := mqttFixture(t, true, "untrusted", nil, false)
	expectState(t, Config{Type: MQTT, Timeout: time.Second, MQTT: &MQTTConfig{Address: address, Username: "monitor", Password: "fixture-secret", Topic: "status/+"}}, "down")
}
func TestMQTTTopicMatchingAndPacketBounds(t *testing.T) {
	for _, tc := range []struct {
		filter, topic string
		want          bool
	}{{"status/+", "status/node", true}, {"status/#", "status", true}, {"#", "$SYS/status", false}, {"$SYS/#", "$SYS/status", true}, {"status/+", "status/a/b", false}} {
		if mqttTopicMatches(tc.filter, tc.topic) != tc.want {
			t.Fatal(tc)
		}
	}
	for _, raw := range [][]byte{{0x30, 0x80, 0}, {0x30, 0xff, 0xff, 0xff, 0xff, 0xff}, {0x30, 2, 0}} {
		if _, _, e := readMQTTPacket(bytes.NewReader(raw)); e == nil {
			t.Fatal("accepted malformed MQTT framing")
		}
	}
	for _, topic := range []string{"", "a/#/b", "a+b", "a/ab#", "a\x00b"} {
		if (&MQTTConfig{Address: "localhost:1883", Topic: topic}).validate() == nil {
			t.Fatal("accepted invalid topic")
		}
	}
}
