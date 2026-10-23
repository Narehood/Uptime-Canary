package services

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

func snmpOptions(version, level, protocol string) SNMPConfig {
	c := SNMPConfig{Version: version, OID: "1.3.6.1.2.1.1.1.0", Community: "fixture-secret"}
	if version == "3" {
		c.Community = ""
		c.Username = "fixture-user"
		c.SecurityLevel = level
		c.ContextName = "monitor"
		if level != "noAuthNoPriv" {
			c.AuthProtocol = protocol
			c.AuthPassphrase = "fixture-secret"
		}
		if level == "authPriv" {
			c.PrivacyProtocol = "aes"
			c.PrivacyPassphrase = "private-value"
		}
	}
	return c
}

func snmpFixture(t *testing.T, c SNMPConfig, mutate func(*gosnmp.SnmpPacket), raw func([]byte) []byte) string {
	t.Helper()
	engine := "\x80\x00\x00\x00\x01fixture"
	sp := &gosnmp.UsmSecurityParameters{AuthoritativeEngineID: engine, AuthoritativeEngineBoots: 7, AuthoritativeEngineTime: 100, UserName: c.Username, AuthenticationProtocol: gosnmp.NoAuth, PrivacyProtocol: gosnmp.NoPriv}
	flags := gosnmp.NoAuthNoPriv
	if c.Version == "3" && c.SecurityLevel != "noAuthNoPriv" {
		flags = gosnmp.AuthNoPriv
		sp.AuthenticationProtocol = gosnmp.SHA
		if c.AuthProtocol == "sha256" {
			sp.AuthenticationProtocol = gosnmp.SHA256
		}
		sp.SecretKey, _ = snmpLocalizedKey(context.Background(), c.AuthPassphrase, engine, c.AuthProtocol)
	}
	if c.SecurityLevel == "authPriv" {
		flags = gosnmp.AuthPriv
		sp.PrivacyProtocol = gosnmp.AES
		key, _ := snmpLocalizedKey(context.Background(), c.PrivacyPassphrase, engine, c.AuthProtocol)
		sp.PrivacyKey = key[:16]
	}
	return udpServiceFixture(t, func(p []byte) ([]byte, error) {
		h, e := snmpHeader(p)
		if e != nil {
			return nil, e
		}
		if c.Version == "3" && h.engine == "" {
			d := &gosnmp.GoSNMP{Version: gosnmp.Version3, SecurityModel: gosnmp.UserSecurityModel, SecurityParameters: &gosnmp.UsmSecurityParameters{UserName: "discovery", AuthenticationProtocol: gosnmp.NoAuth, PrivacyProtocol: gosnmp.NoPriv}}
			request, e := d.SnmpDecodePacket(p)
			if e != nil {
				return nil, e
			}
			if h.flags&3 != 0 || h.username != "" || request.PDUType != gosnmp.GetRequest || len(request.Variables) != 0 {
				return nil, errors.New("incorrect discovery request")
			}
			report := &gosnmp.SnmpPacket{Version: gosnmp.Version3, SecurityModel: gosnmp.UserSecurityModel, SecurityParameters: &gosnmp.UsmSecurityParameters{AuthoritativeEngineID: engine, AuthoritativeEngineBoots: 7, AuthoritativeEngineTime: 100}, MsgID: h.msgID, RequestID: request.RequestID, MsgMaxSize: snmpMaxPacket, PDUType: gosnmp.Report, ContextEngineID: engine, Variables: []gosnmp.SnmpPDU{{Name: ".1.3.6.1.6.3.15.1.1.4.0", Type: gosnmp.Counter32, Value: uint32(1)}}}
			return report.MarshalMsg()
		}
		d := &gosnmp.GoSNMP{Version: gosnmp.SnmpVersion(h.version)}
		if c.Version != "3" && h.community != c.Community {
			return nil, nil
		}
		if c.Version == "3" {
			if h.username != c.Username || flags != 0 && !snmpVerifyMAC(p, h, sp.SecretKey, c.AuthProtocol) {
				return nil, nil // unknown user or wrong authentication key: silent discard
			}
			if h.flags&3 != flags || h.engine != engine {
				return nil, errors.New("incorrect authenticated request")
			}
			if flags == gosnmp.AuthPriv && bytes.Contains(p, []byte("monitor")) {
				return nil, errors.New("scoped request was not encrypted")
			}
			d.MsgFlags = flags
			d.SecurityModel = gosnmp.UserSecurityModel
			d.SecurityParameters = sp
		}
		request, e := d.SnmpDecodePacket(p)
		if e != nil {
			if c.Version == "3" {
				return nil, nil
			}
			return nil, e
		}
		if request.PDUType != gosnmp.GetRequest || len(request.Variables) != 1 || request.Variables[0].Name != "."+strings.TrimPrefix(c.OID, ".") {
			return nil, errors.New("incorrect SNMP GET")
		}
		response := &gosnmp.SnmpPacket{Version: request.Version, Community: request.Community, PDUType: gosnmp.GetResponse, RequestID: request.RequestID, Variables: []gosnmp.SnmpPDU{{Name: request.Variables[0].Name, Type: gosnmp.Integer, Value: 123}}}
		if c.Version == "3" {
			out := sp.Copy().(*gosnmp.UsmSecurityParameters)
			out.PrivacyParameters = []byte{1, 2, 3, 4, 5, 6, 7, 8}
			response.SecurityModel = gosnmp.UserSecurityModel
			response.SecurityParameters = out
			response.MsgFlags = flags
			response.MsgID = request.MsgID
			response.MsgMaxSize = snmpMaxPacket
			response.ContextEngineID = engine
			response.ContextName = c.ContextName
		}
		if mutate != nil {
			mutate(response)
		}
		out, e := response.MarshalMsg()
		if raw != nil && e == nil {
			out = raw(out)
		}
		return out, e
	})
}

func TestSNMPRFC3414LocalizedKeyAndBadClientCredentials(t *testing.T) {
	// RFC 3414 appendix A.3.2, independent of the fixture/library encoder.
	engine, _ := hex.DecodeString("000000000000000000000002")
	key, e := snmpLocalizedKey(context.Background(), "maplesyrup", string(engine), "sha")
	if e != nil || hex.EncodeToString(key) != "6695febc9288e36282235fc7151f128497b38f3f" {
		t.Fatal("RFC 3414 localized key vector failed")
	}
	for _, mode := range []string{"community", "username", "auth", "privacy"} {
		t.Run(mode, func(t *testing.T) {
			c := snmpOptions("3", "authPriv", "sha256")
			if mode == "community" {
				c = snmpOptions("2c", "", "")
			}
			c.Address = snmpFixture(t, c, nil, nil)
			switch mode {
			case "community":
				c.Community = "wrong-secret"
			case "username":
				c.Username = "wrong-user"
			case "auth":
				c.AuthPassphrase = "wrong-secret"
			case "privacy":
				c.PrivacyPassphrase = "wrong-secret"
			}
			expectState(t, Config{Type: SNMP, Timeout: 70 * time.Millisecond, SNMP: &c}, "down")
		})
	}
}

func TestSNMPLocalVersionsAuthenticationPrivacy(t *testing.T) {
	for _, tc := range []struct{ v, l, p string }{{"1", "", ""}, {"2c", "", ""}, {"3", "noAuthNoPriv", ""}, {"3", "authNoPriv", "sha"}, {"3", "authNoPriv", "sha256"}, {"3", "authPriv", "sha"}, {"3", "authPriv", "sha256"}} {
		t.Run(tc.v+tc.l+tc.p, func(t *testing.T) {
			c := snmpOptions(tc.v, tc.l, tc.p)
			c.Condition = &Condition{Operator: "eq", Expected: []byte(`123`)}
			c.Address = snmpFixture(t, c, nil, nil)
			expectState(t, Config{Type: SNMP, Timeout: time.Second, SNMP: &c}, "up")
		})
	}
}

func TestSNMPRejectsLocalInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*gosnmp.SnmpPacket)
		raw    func([]byte) []byte
	}{
		{"wrongID", func(p *gosnmp.SnmpPacket) { p.RequestID++ }, nil},
		{"zeroID", func(p *gosnmp.SnmpPacket) { p.RequestID = 0 }, nil},
		{"wrongOID", func(p *gosnmp.SnmpPacket) { p.Variables[0].Name = ".1.3.6.1.2.1.1.2.0" }, nil},
		{"error", func(p *gosnmp.SnmpPacket) { p.Error = gosnmp.AuthorizationError }, nil},
		{"noInstance", func(p *gosnmp.SnmpPacket) { p.Variables[0].Type = gosnmp.NoSuchInstance; p.Variables[0].Value = nil }, nil},
		{"extraValue", func(p *gosnmp.SnmpPacket) { p.Variables = append(p.Variables, p.Variables[0]) }, nil},
		{"wrongMsgID", func(p *gosnmp.SnmpPacket) { p.MsgID++ }, nil},
		{"wrongContext", func(p *gosnmp.SnmpPacket) { p.ContextName = "fixture-secret" }, nil},
		{"downgrade", func(p *gosnmp.SnmpPacket) { p.MsgFlags = 0 }, nil},
		{"wrongKey", func(p *gosnmp.SnmpPacket) {
			p.SecurityParameters.(*gosnmp.UsmSecurityParameters).SecretKey = bytes.Repeat([]byte{1}, 32)
		}, nil},
		{"badMAC", nil, func(p []byte) []byte { h, _ := snmpHeader(p); p[h.authOffset] ^= 1; return p }},
		{"truncated", nil, func(p []byte) []byte { return p[:len(p)-1] }},
		{"malformed", nil, func([]byte) []byte { return []byte{0x30, 0x84, 0xff, 0xff, 0xff, 0xff} }},
		{"oversized", nil, func([]byte) []byte { return make([]byte, snmpMaxPacket+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := snmpOptions("3", "authPriv", "sha256")
			c.Address = snmpFixture(t, c, tc.mutate, tc.raw)
			expectState(t, Config{Type: SNMP, Timeout: time.Second, SNMP: &c}, "down")
		})
	}
	for _, name := range []string{"wrongCommunity", "condition", "secretValue"} {
		t.Run(name, func(t *testing.T) {
			c := snmpOptions("2c", "", "")
			c.Address = snmpFixture(t, c, func(p *gosnmp.SnmpPacket) {
				if name == "wrongCommunity" {
					p.Community = "private-value"
				} else {
					p.Variables[0].Type = gosnmp.OctetString
					p.Variables[0].Value = []byte("fixture-secret")
				}
			}, nil)
			state := "down"
			if name == "secretValue" {
				state = "up"
			} else if name == "condition" {
				c.Condition = &Condition{Operator: "eq", Expected: []byte(`123`)}
			}
			expectState(t, Config{Type: SNMP, Timeout: time.Second, SNMP: &c}, state)
		})
	}
}

func TestSNMPTimeoutCancellationValidation(t *testing.T) {
	c := snmpOptions("2c", "", "")
	c.Address = udpServiceFixture(t, func([]byte) ([]byte, error) { return nil, nil })
	start := time.Now()
	expectState(t, Config{Type: SNMP, Timeout: 40 * time.Millisecond, SNMP: &c}, "down")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if r := Check(ctx, Config{Type: SNMP, Timeout: time.Second, SNMP: &c}); r.State != "down" || !strings.Contains(r.Message, "canceled") {
		t.Fatal(r)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline did not bound check")
	}
	for _, mutate := range []func(*SNMPConfig){func(c *SNMPConfig) { c.OID = "1.40.2" }, func(c *SNMPConfig) { c.OID = "1.3." }, func(c *SNMPConfig) { c.Version = "4" }, func(c *SNMPConfig) { c.Username = "fixture-secret" }, func(c *SNMPConfig) { c.Community = "" }} {
		bad := c
		mutate(&bad)
		e := Validate(Config{Type: SNMP, Timeout: time.Second, SNMP: &bad})
		if e == nil || strings.Contains(e.Error(), "fixture-secret") {
			t.Fatal("invalid configuration or secret validation")
		}
	}
}
