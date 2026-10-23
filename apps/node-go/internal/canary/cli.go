package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var digits = regexp.MustCompile(`^\d+$`)

// OptionsFromEnvironment never includes environment values in validation errors.
func OptionsFromEnvironment(get func(string) string) (Options, error) {
	o := Options{NodeID: get("CANARY_NODE_ID"), NodeName: get("CANARY_NODE_NAME"), Host: get("CANARY_HOST"), DBPath: get("CANARY_DB"), AdminToken: get("CANARY_ADMIN_TOKEN"), ClusterToken: get("CANARY_CLUSTER_TOKEN")}
	o.IdentityDirectory = get("CANARY_IDENTITY_DIRECTORY")
	for _, setting := range []struct {
		name     string
		min, max int64
		apply    func(int64)
	}{
		{"CANARY_HISTORY_LIMIT", 1, 100000, func(n int64) { o.HistoryLimit = int(n) }},
		{"CANARY_HISTORY_RETENTION_MS", 1000, 31536000000, func(n int64) { o.HistoryRetention = time.Duration(n) * time.Millisecond }},
	} {
		if value := get(setting.name); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if !digits.MatchString(value) || err != nil || n < setting.min || n > setting.max {
				return o, errors.New("Invalid " + setting.name)
			}
			setting.apply(n)
		}
	}
	for _, v := range []struct {
		name   string
		target **int64
	}{{"CANARY_SYNC_INTERVAL_MS", &o.SyncIntervalMs}, {"CANARY_NOTIFICATION_DELAY_MS", &o.NotificationDelayMs}, {"CANARY_PEER_TIMEOUT_MS", &o.PeerTimeoutMs}} {
		if s := get(v.name); s != "" {
			if !digits.MatchString(s) {
				return o, errors.New("Invalid " + v.name)
			}
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return o, errors.New("Invalid " + v.name)
			}
			*v.target = Ptr(n)
		}
	}
	if s := get("CANARY_PORT"); s != "" {
		if !digits.MatchString(s) {
			return o, errors.New("Invalid CANARY_PORT")
		}
		p, err := strconv.ParseInt(s, 10, 32)
		if err != nil || p > 65535 {
			return o, errors.New("Invalid CANARY_PORT")
		}
		o.Port = Ptr(int(p))
	}
	if s := get("CANARY_PEERS"); s != "" {
		if json.Unmarshal([]byte(s), &o.Peers) != nil || o.Peers == nil {
			return o, errors.New("CANARY_PEERS must be a JSON array")
		}
	}
	if s := get("CANARY_CORS_ORIGINS"); s != "" {
		o.CORSOrigins = []string{}
		for _, v := range strings.Split(s, ",") {
			if v = strings.TrimSpace(v); v != "" {
				o.CORSOrigins = append(o.CORSOrigins, v)
			}
		}
	}
	return o, nil
}

const usage = "Commands: serve, status, monitors, add <json-file>, update <id> <json-file>, delete <id> <revision>, history <id>, statistics <id>, accounts, add-account <json-file>, conflicts <id>, resolve <id> <json-file>, groups, add-group <json-file>, update-group <id> <json-file>, delete-group <id> <revision>, group-conflicts <id>, resolve-group <id> <json-file>, channels, add-channel <json-file>, update-channel <id> <json-file>, delete-channel <id> <revision>, channel-conflicts <id>, resolve-channel <id> <json-file>, test-channel <id> <revision>, deliveries [monitor-id], maintenance, add-maintenance <json-file>, update-maintenance <id> <json-file>, delete-maintenance <id> <revision>, maintenance-conflicts <id>, resolve-maintenance <id> <json-file>, recover-account <database-file> <account-name> <password-file> [--reset-mfa] [--revoke-token]"

func Usage() string {
	return usage + ", availability <id>, status-pages, add-status-page <json-file>, update-status-page <id> <json-file>, delete-status-page <id> <revision>, status-page-conflicts <id>, resolve-status-page <id> <json-file>, tags, add-tag <json-file>, update-tag <id> <json-file>, delete-tag <id> <revision>, tag-conflicts <id>, resolve-tag <id> <json-file>, " + databaseCopyUsage + ", activate-restore <quarantined-database-file> [--identity-directory <retained-directory>]"
}
func RunCLI(ctx context.Context, args []string, get func(string) string, client Doer) (any, error) {
	if len(args) > 0 && args[0] == "activate-restore" {
		return activateRestoreCLI(ctx, args)
	}
	if len(args) > 0 && (args[0] == "backup-database" || args[0] == "prepare-restore") {
		return databaseCopyCLI(ctx, args)
	}
	if len(args) > 0 && args[0] == "recover-account" {
		return recoverCLI(ctx, args)
	}
	token := get("CANARY_TOKEN")
	if token == "" {
		token = get("CANARY_ADMIN_TOKEN")
	}
	if token == "" {
		return nil, errors.New("Set CANARY_TOKEN or CANARY_ADMIN_TOKEN")
	}
	raw := get("CANARY_URL")
	if raw == "" {
		raw = "http://127.0.0.1:3100"
	}
	base, err := baseURL(raw, "CANARY_URL")
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New(Usage())
	}
	command := args[0]
	first, second := "", ""
	if len(args) > 1 {
		first = args[1]
	}
	if len(args) > 2 {
		second = args[2]
	}
	method, path := "GET", ""
	var body any
	load := func(file string) (any, error) {
		if file == "" {
			return nil, errors.New(Usage())
		}
		f, err := os.Open(file)
		if err != nil {
			return nil, errors.New("Cannot read valid JSON from the input file")
		}
		defer f.Close()
		b, err := readBounded(f, maxAPIBody)
		var v any
		if err != nil || json.Unmarshal(b, &v) != nil {
			return nil, errors.New("Cannot read valid JSON from the input file")
		}
		return v, nil
	}
	group := command == "update-group" || command == "delete-group" || command == "group-conflicts" || command == "resolve-group"
	resource := "monitors"
	if group {
		resource = "groups"
	}
	if command == "update-channel" || command == "delete-channel" || command == "channel-conflicts" || command == "resolve-channel" || command == "test-channel" {
		resource = "notification-channels"
	}
	if command == "update-maintenance" || command == "delete-maintenance" || command == "maintenance-conflicts" || command == "resolve-maintenance" {
		resource = "maintenance"
	}
	if command == "update-status-page" || command == "delete-status-page" || command == "status-page-conflicts" || command == "resolve-status-page" {
		resource = "status-pages"
	}
	if command == "update-tag" || command == "delete-tag" || command == "tag-conflicts" || command == "resolve-tag" {
		resource = "tags"
	}
	idPath := func() (string, error) {
		if err := identifier(first, "ID"); err != nil {
			return "", err
		}
		return "/api/" + resource + "/" + url.PathEscape(first), nil
	}
	switch command {
	case "status":
		path = "/api/overview"
	case "monitors":
		path = "/api/monitors"
	case "groups":
		path = "/api/groups"
	case "accounts":
		path = "/api/accounts"
	case "maintenance":
		path = "/api/maintenance"
	case "channels":
		path = "/api/notification-channels"
	case "status-pages":
		path = "/api/status-pages"
	case "tags":
		path = "/api/tags"
	case "deliveries":
		path = "/api/notification-deliveries"
		if first != "" {
			if err = identifier(first, "monitor ID"); err == nil {
				path += "?monitorId=" + url.QueryEscape(first)
			}
		}
	case "test-channel":
		path, err = idPath()
		if err == nil {
			err = identifier(second, "revision")
		}
		path += "/test"
		method, body = "POST", map[string]string{"revision": second}
	case "add", "add-account", "add-group", "add-channel", "add-maintenance", "add-status-page", "add-tag":
		method = "POST"
		path = "/api/monitors"
		if command == "add-account" {
			path = "/api/accounts"
		}
		if command == "add-group" {
			path = "/api/groups"
		}
		if command == "add-channel" {
			path = "/api/notification-channels"
		}
		if command == "add-maintenance" {
			path = "/api/maintenance"
		}
		if command == "add-status-page" {
			path = "/api/status-pages"
		}
		if command == "add-tag" {
			path = "/api/tags"
		}
		body, err = load(first)
	case "update", "update-group", "update-channel", "resolve", "resolve-group", "resolve-channel", "update-maintenance", "resolve-maintenance", "update-status-page", "resolve-status-page", "update-tag", "resolve-tag":
		path, err = idPath()
		if err == nil {
			body, err = load(second)
		}
		method = "PUT"
		if command == "resolve" || command == "resolve-group" || command == "resolve-channel" || command == "resolve-maintenance" || command == "resolve-status-page" || command == "resolve-tag" {
			method = "POST"
			path += "/resolve"
		}
	case "delete", "delete-group", "delete-channel", "delete-maintenance", "delete-status-page", "delete-tag":
		path, err = idPath()
		if err == nil {
			err = identifier(second, "revision")
		}
		method = "DELETE"
		body = map[string]string{"revision": second}
	case "history", "statistics", "availability", "conflicts", "group-conflicts", "channel-conflicts", "maintenance-conflicts", "status-page-conflicts", "tag-conflicts":
		path, err = idPath()
		if command == "history" {
			path += "/history"
		} else if command == "statistics" {
			path += "/statistics"
		} else if command == "availability" {
			path += "/availability"
		} else {
			path += "/conflicts"
		}
	default:
		return nil, errors.New(Usage())
	}
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("Invalid command input")
		}
		reader = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, errors.New("Cannot reach Canary API")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = newHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Cannot reach Canary API")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("Canary API returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	if resp.StatusCode == 204 {
		return map[string]bool{"ok": true}, nil
	}
	b, err := readBounded(resp.Body, maxBody)
	var result any
	if err != nil || json.Unmarshal(b, &result) != nil {
		return nil, errors.New("Invalid or oversized JSON response from Canary API")
	}
	tokens := []string{token}
	if m, ok := body.(map[string]any); ok {
		if t, ok := m["token"].(string); ok {
			tokens = append(tokens, t)
		}
	}
	return redact(result, tokens), nil
}
func redact(v any, tokens []string) any {
	switch v := v.(type) {
	case string:
		for _, token := range tokens {
			if token != "" {
				v = strings.ReplaceAll(v, token, "[redacted]")
			}
		}
		return v
	case []any:
		for i := range v {
			v[i] = redact(v[i], tokens)
		}
		return v
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "token") || strings.Contains(lower, "hash") || strings.Contains(lower, "password") || strings.Contains(lower, "apikey") || strings.Contains(lower, "privatekey") || lower == "dsn" || lower == "webhookurl" || lower == "headers" || lower == "body" || lower == "basicauth" || lower == "clientkeypem" {
				continue
			}
			if lower == "config" {
				if config, ok := child.(map[string]any); ok {
					if kind, ok := config["kind"].(string); ok {
						out[k] = map[string]any{"kind": kind, "redacted": true}
						continue
					}
				}
			}
			out[k] = redact(child, tokens)
		}
		return out
	default:
		return v
	}
}
