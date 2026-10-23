package services

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

type MQTTConfig struct {
	Address   string            `json:"address"`
	Username  string            `json:"username,omitempty"`
	Password  string            `json:"password,omitempty"`
	Topic     string            `json:"topic"`               // MQTT subscription filter; + and terminal # supported
	Condition *Condition        `json:"condition,omitempty"` // raw payload scalar/text; nil means any message
	JSONata   *jsonquery.Config `json:"jsonata,omitempty"`
	TLSMode   string            `json:"tlsMode,omitempty"`
	TLS       *TLSConfig        `json:"tls,omitempty"`
}

func mqttString(s string) bool {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return false
	}
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF || r >= 0xFDD0 && r <= 0xFDEF || r&0xFFFF == 0xFFFF || r&0xFFFF == 0xFFFE {
			return false
		}
	}
	return true
}
func (c MQTTConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if len(c.Username) > 1024 || len(c.Password) > 4096 || !mqttString(c.Username) || (c.Password != "" && c.Username == "") {
		return errors.New("invalid MQTT credentials")
	}
	if len(c.Topic) == 0 || len(c.Topic) > 2048 || !mqttString(c.Topic) {
		return errors.New("invalid MQTT topic filter")
	}
	parts := strings.Split(c.Topic, "/")
	for i, p := range parts {
		if strings.Contains(p, "#") && (p != "#" || i != len(parts)-1) || strings.Contains(p, "+") && p != "+" {
			return errors.New("invalid MQTT topic wildcard")
		}
	}
	if err := validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	h, _, _ := net.SplitHostPort(c.Address)
	if _, err := tlsSettings(c.TLS, h); err != nil {
		return err
	}
	if c.Condition != nil {
		if err := c.Condition.validate(); err != nil {
			return err
		}
	}
	if c.JSONata != nil {
		return c.JSONata.Validate()
	}
	return nil
}
func mqttField(s string) []byte {
	b := make([]byte, len(s)+2)
	binary.BigEndian.PutUint16(b, uint16(len(s)))
	copy(b[2:], s)
	return b
}
func writeMQTTPacket(w io.Writer, header byte, body []byte) error {
	b := []byte{header}
	n := len(body)
	for {
		v := byte(n % 128)
		n /= 128
		if n > 0 {
			v |= 128
		}
		b = append(b, v)
		if n == 0 {
			break
		}
	}
	b = append(b, body...)
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func readMQTTPacket(r io.Reader) (byte, []byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(r, one[:]); err != nil {
		return 0, nil, err
	}
	header := one[0]
	n, mult := 0, 1
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(r, one[:]); err != nil {
			return 0, nil, err
		}
		v := one[0]
		n += int(v&127) * mult
		if n > 65536 {
			return 0, nil, errors.New("MQTT packet exceeds limit")
		}
		if v&128 == 0 {
			if i > 0 && v == 0 {
				return 0, nil, errors.New("noncanonical MQTT packet length")
			}
			b := make([]byte, n)
			_, err := io.ReadFull(r, b)
			return header, b, err
		}
		mult *= 128
	}
	return 0, nil, errors.New("invalid MQTT packet length")
}
func mqttTopicMatches(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") && !strings.HasPrefix(filter, "$") {
		return false
	}
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, p := range f {
		if p == "#" {
			return true
		}
		if i >= len(t) || p != "+" && p != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}
func checkMQTT(ctx context.Context, c MQTTConfig) Result {
	conn, err := dialService(ctx, c.Address, c.TLSMode, c.TLS)
	if err != nil {
		return down("MQTT connection or TLS failed")
	}
	defer conn.Close()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return down("MQTT client identity generation failed")
	}
	flags := byte(2) // clean session; no persistent subscription remains
	body := append(mqttField("MQTT"), 4, flags, 0, 0)
	// Keep the random clean-session ID within the mandatory 23-character,
	// alphanumeric subset supported by MQTT 3.1.1 brokers.
	body = append(body, mqttField("canary"+hex.EncodeToString(random[:]))...)
	if c.Username != "" {
		body[7] |= 128
		body = append(body, mqttField(c.Username)...)
	}
	if c.Password != "" {
		body[7] |= 64
		body = append(body, mqttField(c.Password)...)
	}
	if writeMQTTPacket(conn, 0x10, body) != nil {
		return down("MQTT CONNECT failed")
	}
	h, b, err := readMQTTPacket(conn)
	if err != nil || h != 0x20 || len(b) != 2 || b[0] != 0 || b[1] > 5 {
		return down("invalid MQTT CONNACK")
	}
	if b[1] != 0 {
		return down("MQTT connection or authentication refused")
	}
	body = append([]byte{0, 1}, mqttField(c.Topic)...)
	body = append(body, 0) // QoS 0 subscription, identifier 1
	if writeMQTTPacket(conn, 0x82, body) != nil {
		return down("MQTT SUBSCRIBE failed")
	}
	acked, matched := false, false
	total := 0
	for count := 0; count < 128; count++ {
		h, b, err = readMQTTPacket(conn)
		if err != nil {
			return down("MQTT message read failed or exceeds limit")
		}
		total += len(b)
		if total > maxBody {
			return down("MQTT messages exceed aggregate limit")
		}
		switch {
		case h == 0x90:
			if acked || len(b) != 3 || b[0] != 0 || b[1] != 1 || b[2] != 0 {
				return down("MQTT subscription refused or malformed SUBACK")
			}
			acked = true
		case h>>4 == 3:
			qos := (h >> 1) & 3
			if len(b) < 2 || qos > 1 || qos == 0 && h&8 != 0 {
				return down("invalid MQTT PUBLISH")
			}
			n := int(binary.BigEndian.Uint16(b))
			if n == 0 || n+2 > len(b) {
				return down("invalid MQTT topic length")
			}
			topic := string(b[2 : n+2])
			if !mqttString(topic) || strings.ContainsAny(topic, "+#") || !mqttTopicMatches(c.Topic, topic) {
				return down("MQTT publication has invalid or unrelated topic")
			}
			payload := b[n+2:]
			if qos == 1 {
				if len(payload) < 2 || binary.BigEndian.Uint16(payload) == 0 {
					return down("invalid MQTT publication identifier")
				}
				if writeMQTTPacket(conn, 0x40, payload[:2]) != nil {
					return down("MQTT PUBACK failed")
				}
				payload = payload[2:]
			}
			if c.Condition == nil || c.Condition.matches(payload, false) {
				if c.JSONata != nil {
					result := evaluateJSONQuery(ctx, *c.JSONata, payload, "MQTT")
					if result.State != "up" {
						if result.State == "pending" || ctx.Err() != nil {
							return result
						}
						// Like the scalar matcher, a nonmatching publication is
						// not the end of a subscription check. A later retained or
						// live message may satisfy the condition before the deadline.
						continue
					}
				}
				matched = true
			}
		default:
			return down("unexpected MQTT packet")
		}
		if acked && matched {
			_ = writeMQTTPacket(conn, 0xE0, nil)
			return up("MQTT subscription received a matching message")
		}
	}
	return down("MQTT packet count exceeds limit")
}
