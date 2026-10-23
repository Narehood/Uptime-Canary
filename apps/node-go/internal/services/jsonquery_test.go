package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

func queryCondition(expression string, expected string) *jsonquery.Config {
	return &jsonquery.Config{Expression: expression, Operator: jsonquery.Equal, Expected: json.RawMessage(expected)}
}

func TestMQTTJSONataEvaluatesActualPayload(t *testing.T) {
	for _, tc := range []struct{ payload, expression, expected, state string }{
		{`{"items":[2,5]}`, `$sum(items)`, `7`, "up"},
		{"ready", `$uppercase($)`, `"READY"`, "up"},
		{`{"state":"failed"}`, `state`, `"ready"`, "down"},
	} {
		variant := "success"
		if tc.state == "down" {
			variant = "query-condition-failed"
		}
		address, _ := mqttFixture(t, false, variant, []byte(tc.payload), false)
		c := Config{Type: MQTT, Timeout: 4 * time.Second, MQTT: &MQTTConfig{Address: address, Username: "monitor", Password: "fixture-secret", Topic: "status/+", TLSMode: "plain", JSONata: queryCondition(tc.expression, tc.expected)}}
		if r := Check(context.Background(), c); r.State != tc.state {
			t.Fatal("MQTT JSONata result", r)
		}
	}
}

func TestSNMPJSONataFollowsAuthenticatedValue(t *testing.T) {
	for _, expected := range []string{`7`, `8`} {
		c := snmpOptions("3", "authPriv", "sha256")
		c.Address = snmpFixture(t, c, func(packet *gosnmp.SnmpPacket) {
			packet.Variables[0].Type = gosnmp.OctetString
			packet.Variables[0].Value = []byte(`{"values":[2,5]}`)
		}, nil)
		c.JSONata = queryCondition(`$sum(values)`, expected)
		want := "down"
		if expected == `7` {
			want = "up"
		}
		if r := Check(context.Background(), Config{Type: SNMP, Timeout: 4 * time.Second, SNMP: &c}); r.State != want || strings.Contains(r.Message, "fixture-secret") {
			t.Fatal("authenticated SNMP JSONata result", r)
		}
	}
}

func TestMongoDBJSONataReceivesCompleteReturnedDocument(t *testing.T) {
	for _, expected := range []string{`"READY"`, `"FAILED"`} {
		address, _ := mongoFixture(t, false, "string")
		c := &MongoDBConfig{DSN: "mongodb://" + address + "/monitor", TLSMode: "plain", Collection: "status", Filter: []byte(`{"key":"ready"}`), JSONata: queryCondition(`$uppercase(value)`, expected)}
		want := "down"
		if expected == `"READY"` {
			want = "up"
		}
		if r := Check(context.Background(), Config{Type: MongoDB, Timeout: 4 * time.Second, MongoDB: c}); r.State != want {
			t.Fatal("MongoDB document JSONata result", r)
		}
	}
}
