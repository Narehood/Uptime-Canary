package canary

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"uptime-canary/apps/node-go/internal/globalping"
	"uptime-canary/apps/node-go/internal/steam"
)

const maxBody = 1_048_576

// Allows bounded request bodies, certificate chains and reusable templates.
// Identity endpoints retain their smaller dedicated input limit.
const maxAPIBody = 262_144
const maxMonitors = 200
const maxGroups = 200

// Local admission counts retained IDs. A valid partition-local union can be
// larger than one node's admission limit, across the fixed membership.
const maxClusterMembers = 32
const maxSafe int64 = 9_007_199_254_740_991

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$`)
var labelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

func validText(v, field string, limit int) error {
	if strings.TrimSpace(v) == "" || len(v) > limit || strings.ContainsFunc(v, func(c rune) bool { return c < 32 || c == 127 }) {
		return fail(400, "Invalid "+field)
	}
	return nil
}
func identifier(v, field string) error {
	if err := validText(v, field, 128); err != nil {
		return err
	}
	if !idPattern.MatchString(v) || slices.Contains([]string{"constructor", "prototype", "__proto__"}, v) {
		return fail(400, "Invalid "+field)
	}
	return nil
}
func bounded(v int64, field string, lo, hi int64) error {
	if v < lo || v > hi {
		return fail(400, fmt.Sprintf("%s must be an integer between %d and %d", field, lo, hi))
	}
	return nil
}
func httpURL(raw, field string) (string, error) {
	if err := validText(raw, field, 2048); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if u != nil {
		u.Scheme = strings.ToLower(u.Scheme)
	}
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(u.Host, " \\\t\r\n") {
		return "", fail(400, "Invalid "+field+": use HTTP or HTTPS without URL credentials or fragments")
	}
	port := u.Port()
	if port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", fail(400, "Invalid "+field)
		}
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	hostname := strings.ToLower(u.Hostname())
	u.Host = hostname
	if strings.Contains(hostname, ":") {
		u.Host = "[" + hostname + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(hostname, port)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}
func baseURL(raw, field string) (string, error) {
	v, err := httpURL(raw, field)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(v)
	if u.Path != "/" || u.RawQuery != "" {
		return "", fail(400, field+" must be a base URL")
	}
	return strings.TrimSuffix(v, "/"), nil
}
func pingTarget(raw string) error {
	if err := validText(raw, "ping target", 253); err != nil {
		return err
	}
	if net.ParseIP(raw) != nil && !strings.Contains(raw, "%") {
		return nil
	}
	for _, label := range strings.Split(strings.TrimSuffix(raw, "."), ".") {
		if len(label) > 63 || !labelPattern.MatchString(label) {
			return fail(400, "Invalid ping target: use a hostname or IP address")
		}
	}
	return nil
}
func tokenValue(v string) error {
	seen := map[rune]bool{}
	for _, c := range v {
		if c < 33 || c > 126 {
			return fail(400, "Tokens require 24-256 high entropy printable characters")
		}
		seen[c] = true
	}
	if len(v) < 24 || len(v) > 256 || len(seen) < 8 {
		return fail(400, "Tokens require 24-256 high entropy printable characters")
	}
	return nil
}
func validRole(v string) bool { return v == "admin" || v == "editor" || v == "viewer" }
func placement(ids []string, known map[string]bool) error {
	if len(ids) < 1 || len(ids) > 32 {
		return fail(400, "Invalid nodeIds")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if err := identifier(id, "nodeIds"); err != nil {
			return err
		}
		if seen[id] || !known[id] {
			return fail(400, "nodeIds must be unique configured node IDs")
		}
		seen[id] = true
	}
	return nil
}
func validateMonitor(m *MonitorInput, known map[string]bool) error {
	if m.Type != "steam" && m.Steam != nil {
		return fail(400, "Steam options do not match monitor type")
	}
	if m.Type != "globalping" && m.Globalping != nil {
		return fail(400, "Globalping options do not match monitor type")
	}
	if m.Type != "push" && m.Push != nil {
		return fail(400, "Push options do not match monitor type")
	}
	if len(m.Tags) > 32 {
		return fail(400, "At most 32 tags may be assigned to a monitor")
	}
	seenTags := map[string]bool{}
	for _, tag := range m.Tags {
		if identifier(tag.TagID, "tagId") != nil || seenTags[tag.TagID] || tag.Value != "" && validText(tag.Value, "tag value", 128) != nil {
			return fail(400, "Invalid or duplicate monitor tag")
		}
		seenTags[tag.TagID] = true
	}
	if err := validText(m.Name, "name", 128); err != nil {
		return err
	}
	var err error
	switch m.Type {
	case "http":
		m.Target, err = httpURL(m.Target, "target")
	case "ping":
		err = pingTarget(m.Target)
	case "manual":
		m.Target = "manual"
	case "push":
		m.Target = "push"
	case "tcp", "tls", "dns", "websocket", "smtp", "system-service":
	case "globalping", "steam":
	case "docker", "redis", "postgres", "mysql", "mqtt", "sftp", "mongodb", "sqlserver", "ntp", "grpc", "snmp", "radius", "sip-options", "rabbitmq", "kafka-producer", "oracledb":
	default:
		return fail(400, "Invalid monitor type")
	}
	if err != nil {
		return err
	}
	if err = bounded(m.IntervalMs, "intervalMs", 100, 86_400_000); err != nil {
		return err
	}
	if err = bounded(m.TimeoutMs, "timeoutMs", 25, 120_000); err != nil {
		return err
	}
	if m.TimeoutMs > m.IntervalMs {
		return fail(400, "timeoutMs must not exceed intervalMs")
	}
	if err = bounded(int64(m.Retries), "retries", 0, 100); err != nil {
		return err
	}
	if m.RetryIntervalMs != nil {
		if err = bounded(*m.RetryIntervalMs, "retryIntervalMs", 100, 86400000); err != nil {
			return err
		}
		if m.TimeoutMs > *m.RetryIntervalMs {
			return fail(400, "timeoutMs must not exceed retryIntervalMs")
		}
	}
	if err = placement(m.NodeIDs, known); err != nil {
		return err
	}
	m.NodeIDs = append([]string{}, m.NodeIDs...)
	slices.Sort(m.NodeIDs)
	if m.ExpectedStatusCodes != nil {
		if m.Type != "http" || len(m.ExpectedStatusCodes) < 1 || len(m.ExpectedStatusCodes) > 100 {
			return fail(400, "Invalid expectedStatusCodes")
		}
		for _, c := range m.ExpectedStatusCodes {
			if c < 100 || c > 599 {
				return fail(400, "Invalid HTTP status")
			}
		}
		m.ExpectedStatusCodes = unique(m.ExpectedStatusCodes)
	}
	if m.WebhookURL != "" {
		m.WebhookURL, err = httpURL(m.WebhookURL, "webhookUrl")
		if err != nil {
			return err
		}
	}
	if m.RepeatAfterMs != nil {
		if err = bounded(*m.RepeatAfterMs, "repeatAfterMs", 0, 604_800_000); err != nil {
			return err
		}
		if *m.RepeatAfterMs > 0 && *m.RepeatAfterMs < 1000 {
			return fail(400, "repeatAfterMs must be zero or at least 1000")
		}
	}
	if m.GroupID != "" {
		if err = identifier(m.GroupID, "groupId"); err != nil {
			return err
		}
	}
	if m.TLS != nil && (!finite(m.TLS.MinValidityDays) || m.TLS.MinValidityDays < 0 || m.TLS.MinValidityDays > 36500) {
		return fail(400, "minValidityDays must be between 0 and 36500")
	}
	if m.Type != "manual" && m.Manual != nil {
		return fail(400, "Manual options do not match monitor type")
	}
	if m.Type != "ping" && m.Ping != nil {
		return fail(400, "Ping options do not match monitor type")
	}
	if m.Type == "steam" {
		if m.Steam == nil || m.Service != nil || m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil || m.Manual != nil || m.Ping != nil {
			return fail(400, "Options do not match Steam monitor")
		}
		if steam.Validate(steamConfig(*m)) != nil {
			return fail(400, "Invalid Steam configuration")
		}
	} else if m.Type == "system-service" {
		if m.Service != nil || m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil || m.Manual != nil || m.Ping != nil {
			return fail(400, "Options do not match system service monitor")
		}
		if systemServiceConfig(*m).Validate() != nil {
			return fail(400, "Invalid internal service name")
		}
	} else if m.Type == "globalping" {
		if m.Globalping == nil || m.Service != nil || m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil || m.Manual != nil || m.Ping != nil || m.Push != nil {
			return fail(400, "Options do not match Globalping monitor")
		}
		if globalping.Validate(globalpingConfig(*m)) != nil {
			return fail(400, "Invalid Globalping configuration")
		}
	} else if m.Type == "push" {
		if m.Push == nil || !validPushSecret(m.Push.Token) {
			return fail(400, "Push requires a private 64-character lowercase hexadecimal token")
		}
		if m.Service != nil || m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil || m.Manual != nil || m.Ping != nil {
			return fail(400, "Options do not match push monitor")
		}
	} else if m.Type == "manual" {
		if m.Service != nil || m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil {
			return fail(400, "Options do not match manual monitor")
		}
		if m.Manual != nil {
			if m.Manual.State != "" && m.Manual.State != "up" && m.Manual.State != "down" && m.Manual.State != "pending" {
				return fail(400, "Manual state must be up, down or pending")
			}
			if m.Manual.Message != "" && validText(m.Manual.Message, "manual message", 256) != nil {
				return fail(400, "Invalid manual message")
			}
		}
	} else if isServiceType(m.Type) {
		if m.Service != nil && m.Service.NTP != nil {
			n := m.Service.NTP
			if bounded(n.OffsetThresholdMs, "offsetThresholdMs", 0, 86400000) != nil || bounded(n.RootDispersionThresholdMs, "rootDispersionThresholdMs", 0, 86400000) != nil || bounded(n.LatencyThresholdMs, "latencyThresholdMs", 0, 300000) != nil {
				return fail(400, "NTP thresholds exceed their limits")
			}
		}
		if m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil {
			return fail(400, "Network options do not match service monitor")
		}
		c := serviceCheckConfig(*m)
		if err = c.Validate(); err != nil {
			return fail(400, err.Error())
		}
		m.Target = serviceDisplayTarget(c)
	} else if m.Service != nil {
		return fail(400, "Service options do not match monitor type")
	} else if m.Type == "ping" {
		if err = validatePing(*m); err != nil {
			return err
		}
		if m.HTTP != nil || m.TLS != nil || m.DNS != nil || m.WebSocket != nil || m.SMTP != nil {
			return fail(400, "Options do not match ping monitor")
		}
	} else {
		if m.HTTP != nil && len(m.HTTP.StatusRanges) > 0 && len(m.ExpectedStatusCodes) > 0 {
			return fail(400, "Choose statusRanges or expectedStatusCodes, not both")
		}
		if err = activeCheckConfig(*m).Validate(); err != nil {
			return fail(400, err.Error())
		}
	}
	return nil
}
func validateGroup(g *GroupInput, known map[string]bool) error {
	if err := validText(g.Name, "name", 128); err != nil {
		return err
	}
	if err := placement(g.NodeIDs, known); err != nil {
		return err
	}
	g.NodeIDs = append([]string{}, g.NodeIDs...)
	slices.Sort(g.NodeIDs)
	if err := bounded(g.GraceMs, "graceMs", 0, 120000); err != nil {
		return err
	}
	if g.ParentID != "" {
		if err := identifier(g.ParentID, "parentId"); err != nil {
			return err
		}
	}
	if g.OutageMonitorID != "" {
		return identifier(g.OutageMonitorID, "outageMonitorId")
	}
	return nil
}
func jsonObject(b []byte) (map[string]json.RawMessage, error) {
	var o map[string]json.RawMessage
	if json.Unmarshal(b, &o) != nil || o == nil {
		return nil, fail(400, "Expected a JSON object")
	}
	return o, nil
}
func required(o map[string]json.RawMessage, fields ...string) error {
	for _, f := range fields {
		b, ok := o[f]
		if !ok || string(b) == "null" {
			return fail(400, "Invalid "+f)
		}
	}
	return nil
}
func optionalNonNull(o map[string]json.RawMessage, fields ...string) error {
	for _, f := range fields {
		if b, ok := o[f]; ok && string(b) == "null" {
			return fail(400, "Invalid "+f)
		}
	}
	return nil
}
func parseMonitor(b []byte, known map[string]bool) (MonitorInput, error) {
	var m MonitorInput
	o, err := jsonObject(b)
	if err != nil {
		return m, err
	}
	if err = required(o, "name", "type", "target", "intervalMs", "timeoutMs", "nodeIds", "enabled"); err != nil {
		return m, err
	}
	if err = optionalNonNull(o, "expectedStatusCodes", "webhookUrl", "notificationIds", "repeatAfterMs", "groupId", "http", "tls", "dns", "websocket", "smtp", "upsideDown", "service", "retries", "retryIntervalMs", "manual", "ping", "tags", "push", "globalping", "steam"); err != nil {
		return m, err
	}
	if json.Unmarshal(b, &m) != nil {
		return m, fail(400, "Invalid monitor input")
	}
	if m.NotificationIDs != nil && (!validIDs(m.NotificationIDs, 16) || len(unique(m.NotificationIDs)) != len(m.NotificationIDs)) {
		return m, fail(400, "notificationIds must contain at most 16 unique channel IDs")
	}
	err = validateMonitor(&m, known)
	return m, err
}
func parseGroup(b []byte, known map[string]bool) (GroupInput, error) {
	var g GroupInput
	// The existing exact-object reader rejects duplicate and differently cased
	// keys as well as nulls; metadata permits management responses to round-trip.
	o, err := statusObject(b, "name", "nodeIds", "outageMonitorId", "graceMs", "parentId", "id", "revision", "updatedAt", "deleted", "revisions")
	if err != nil {
		return g, fail(400, "Invalid group input")
	}
	if err = required(o, "name", "nodeIds", "graceMs"); err != nil {
		return g, err
	}
	if err = optionalNonNull(o, "outageMonitorId", "parentId"); err != nil {
		return g, err
	}
	if json.Unmarshal(b, &g) != nil {
		return g, fail(400, "Invalid group input")
	}
	err = validateGroup(&g, known)
	return g, err
}
func safeMessage(s string) string {
	v := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(v) > 256 {
		v = v[:256]
	}
	if v == "" {
		v = "Probe completed"
	}
	return v
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
