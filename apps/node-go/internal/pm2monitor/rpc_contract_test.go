package pm2monitor

// Independent source-only fixture: root places this in internal/pm2monitor
// for bounded validation. It never contacts a manager or starts a worker.
// Assumes check(ctx, Config, func(context.Context, Config) (net.Conn, error))
// process.Result and a flat WorkerHandler request with type/name/timeout/options.
// Wire references (pinned, read directly; no production codec helpers used):
// github.com/Unitech/pm2-axon-rpc/blob/a040e0233649147894e1f5f2b0a24fd02ebb8675/lib/server.js
// github.com/Unitech/pm2-axon/blob/e012e208f82b697fd8c0fa9795602cb3a7c82f54/lib/sockets/req.js
// github.com/Unitech/pm2-axon/blob/e012e208f82b697fd8c0fa9795602cb3a7c82f54/lib/sockets/rep.js
// github.com/tj/node-amp/blob/f48c5bd66b0ec39f03658ba3d427d0161502bb21/lib/encode.js
// github.com/tj/node-amp-message/blob/bc46f9855a48d1076d057f0ce12fcb49c850de3c/index.js
// github.com/Unitech/pm2/blob/3b3b54727dab5acf6b43778314cd26b84ffa94d3/lib/God/ActionMethods.js
// github.com/Unitech/pm2/blob/3b3b54727dab5acf6b43778314cd26b84ffa94d3/lib/Daemon.js

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

const pm2ContractOnline = `{"name":"fixture-app","pm_id":0,"pm2_env":{"namespace":"default","status":"online"}}`

func pm2ContractConfig() Config {
	return Config{Name: "fixture-app", Timeout: 2 * time.Second, Options: Options{Transport: "unix", Home: "/private-fixture"}}
}

func pm2ContractResult(t *testing.T, got process.Result, state, message string) {
	t.Helper()
	want := process.Result{State: state, Message: message, LatencyMs: 0}
	if got != want {
		t.Fatalf("fixed result mismatch: got %#v, want %#v", got, want)
	}
}

// AMP has no aggregate length field: one version/argc byte, then a big-endian
// uint32 length and bytes for each argument. JSON and correlation are typed.
func pm2ContractAMP(parts ...[]byte) []byte {
	b := []byte{0x10 | byte(len(parts))}
	for _, part := range parts {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(part)))
		b = append(b, size[:]...)
		b = append(b, part...)
	}
	return b
}

func pm2ContractReply(body, correlation string) []byte {
	return pm2ContractAMP([]byte("j:"+body), []byte("s:"+correlation))
}

func pm2ContractList(entries ...string) string {
	return `{"args":[[` + strings.Join(entries, ",") + `]]}`
}

// Read outbound bytes independently and reject any bootstrap, enumeration,
// control command, extra argument or nonempty argument object.
func pm2ContractReadCall(conn net.Conn, method, previous string) (string, error) {
	var head [1]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return "", err
	}
	if head[0] != 0x12 {
		return "", fmt.Errorf("request metadata = %x, want AMP1/two args", head[0])
	}
	var parts [2][]byte
	total := 1
	for i := range parts {
		var size [4]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint32(size[:])
		if n < 2 || uint64(total)+4+uint64(n) > 512 {
			return "", errors.New("request exceeds 512 bytes or lacks a type prefix")
		}
		total += 4 + int(n)
		parts[i] = make([]byte, int(n))
		if _, err := io.ReadFull(conn, parts[i]); err != nil {
			return "", err
		}
	}
	if !bytes.HasPrefix(parts[0], []byte("j:")) || !bytes.HasPrefix(parts[1], []byte("s:")) {
		return "", errors.New("request arguments must be j: JSON and s: correlation")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(parts[0][2:], &envelope) != nil || len(envelope) != 3 {
		return "", errors.New("unexpected request envelope")
	}
	var kind, actual string
	var args []json.RawMessage
	if json.Unmarshal(envelope["type"], &kind) != nil || kind != "call" ||
		json.Unmarshal(envelope["method"], &actual) != nil || actual != method ||
		json.Unmarshal(envelope["args"], &args) != nil || len(args) != 1 {
		return "", fmt.Errorf("only %s with one empty-object argument is permitted", method)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(args[0], &object) != nil || object == nil || len(object) != 0 {
		return "", errors.New("RPC argument was not an empty object")
	}
	correlation := string(parts[1][2:])
	if correlation == "" || correlation == previous || !utf8.ValidString(correlation) {
		return "", errors.New("missing, reused or invalid correlation")
	}
	return correlation, nil
}

func pm2ContractEnd(conn net.Conn) error {
	var b [1]byte
	n, err := conn.Read(b[:]) // Stay open until the client finishes; never supply EOF as framing.
	if n != 0 || err == nil {
		return errors.New("unexpected extra RPC bytes after the permitted calls")
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return fmt.Errorf("client did not finish/close its manager connection: %w", err)
}

type pm2ContractWriteObserver struct {
	net.Conn
	started chan struct{}
	seen    atomic.Bool
}

func (c *pm2ContractWriteObserver) Write(b []byte) (int, error) {
	if c.seen.CompareAndSwap(false, true) {
		close(c.started)
	}
	return c.Conn.Write(b)
}

func pm2ContractRun(t *testing.T, parent context.Context, c Config, script func(net.Conn) error, observeWrite ...chan struct{}) process.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	var connection net.Conn = client
	if len(observeWrite) != 0 {
		connection = &pm2ContractWriteObserver{Conn: client, started: observeWrite[0]}
	}
	_ = server.SetDeadline(time.Now().Add(3 * time.Second)) // Fixture watchdog, not reply EOF.
	serverDone := make(chan error, 1)
	var dials atomic.Int32
	resultDone := make(chan process.Result, 1)
	go func() {
		resultDone <- check(ctx, c, func(_ context.Context, got Config) (net.Conn, error) {
			if dials.Add(1) != 1 {
				return nil, errors.New("fixture forbids reconnect")
			}
			if got.Name != c.Name || got.Timeout != c.Timeout || got.Options.Transport != c.Options.Transport || got.Options.Home != c.Options.Home || got.Options.Namespace != c.Options.Namespace {
				return nil, errors.New("configuration changed before dial")
			}
			// Start after dial was observed so write-cancellation is deterministic.
			go func() { defer server.Close(); serverDone <- script(server) }()
			return connection, nil
		})
	}()
	var result process.Result
	select {
	case result = <-resultDone:
	case <-time.After(4 * time.Second):
		t.Fatal("check ignored fixture context/deadline")
	}
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("independent manager fixture: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager fixture failed to finish")
	}
	if dials.Load() != 1 {
		t.Fatalf("dial count = %d, want exactly one", dials.Load())
	}
	return result
}

func pm2ContractRPC(t *testing.T, c Config, stage string, reply func(string) []byte, fragmented bool) process.Result {
	t.Helper()
	return pm2ContractRun(t, context.Background(), c, func(conn net.Conn) error {
		previous := ""
		for _, method := range []string{"getVersion", "getMonitorData"} {
			id, err := pm2ContractReadCall(conn, method, previous)
			if err != nil {
				return fmt.Errorf("%s request: %w", method, err)
			}
			previous = id
			wire := pm2ContractReply(`{"args":["6.0.8"]}`, id)
			if method == stage {
				wire = reply(id)
			}
			for len(wire) != 0 {
				n := len(wire)
				if fragmented && n > 3 {
					n = 3 // Split length fields, type prefixes and UTF-8 sequences.
				}
				if _, err = conn.Write(wire[:n]); err != nil {
					// A malformed frame can be rejected before all its bytes are read.
					if method != stage {
						return err
					}
					return pm2ContractEnd(conn)
				}
				wire = wire[n:]
			}
			if method == stage {
				return pm2ContractEnd(conn)
			}
		}
		return errors.New("fixture stage was not a permitted method")
	})
}

func TestPM2ContractProcessSelection(t *testing.T) {
	entry := func(name, namespace, status string, id int) string {
		b, _ := json.Marshal(map[string]any{"name": name, "pm_id": id, "pm2_env": map[string]any{"namespace": namespace, "status": status}})
		return string(b)
	}
	online := pm2ContractOnline
	unknown := entry("fixture-app", "default", "future-status", 1)
	stopped := entry("fixture-app", "default", "stopped", 2)
	cases := []struct {
		name, target, namespace, body, state, message string
	}{
		{"fragmented-kept-open", "fixture-app", "", pm2ContractList(online), "up", "PM2 process is online"},
		{"all-matches-online", "fixture-app", "", pm2ContractList(online, entry("fixture-app", "other", "online", 1)), "up", "PM2 process is online"},
		{"namespace-selects", "fixture-app", "default", pm2ContractList(online, entry("fixture-app", "other", "stopped", 1)), "up", "PM2 process is online"},
		{"namespace-exact", "fixture-app", "DEFAULT", pm2ContractList(online), "down", "PM2 process was not found"},
		{"name-exact", "FIXTURE-APP", "", pm2ContractList(online), "down", "PM2 process was not found"},
		{"empty-list", "fixture-app", "", pm2ContractList(), "down", "PM2 process was not found"},
		{"numeric-id-not-name", "0", "", pm2ContractList(online), "down", "PM2 process was not found"},
		{"numeric-literal-name", "0", "", pm2ContractList(entry("0", "default", "online", 4)), "up", "PM2 process is online"},
		{"unknown-selected", "fixture-app", "", pm2ContractList(online, unknown), "pending", "PM2 status unavailable"},
		{"unknown-unselected", "fixture-app", "", pm2ContractList(online, entry("other", "default", "future-status", 1)), "up", "PM2 process is online"},
		{"known-down-after-unknown", "fixture-app", "", pm2ContractList(unknown, stopped), "down", "PM2 process is not online"},
		{"known-down-before-unknown", "fixture-app", "", pm2ContractList(stopped, unknown), "down", "PM2 process is not online"},
		{"duplicate-selected-id", "fixture-app", "", pm2ContractList(online, online), "pending", "PM2 status unavailable"},
		{"duplicate-unselected-id", "fixture-app", "", pm2ContractList(online, entry("other", "other", "online", 0)), "pending", "PM2 status unavailable"},
		{"malformed-unselected", "fixture-app", "", pm2ContractList(online, `{"name":"other","pm_id":8}`), "pending", "PM2 status unavailable"},
		{"down-does-not-hide-malformed", "fixture-app", "", pm2ContractList(stopped, `null`), "pending", "PM2 status unavailable"},
		{"valid-additive-private-metadata", "fixture-app", "", pm2ContractList(strings.Replace(online, `"status":"online"`, `"status":"online","env":{"TOKEN":"private-secret\npath"},"metadata":[1,true,null,{"ok":"yes"}]`, 1)), "up", "PM2 process is online"},
		{"uppercase-env-metadata-accepted", "fixture-app", "default", pm2ContractList(strings.Replace(online, `"status":"online"`, `"status":"online","HOME":"/private-secret","NAME":"other","STATUS":"stopped","NAMESPACE":"other","env":{"HOME":"/private-secret","NAME":"other","STATUS":"stopped"}`, 1)), "up", "PM2 process is online"},
		{"uppercase-status-cannot-promote", "fixture-app", "", pm2ContractList(strings.Replace(stopped, `"status":"stopped"`, `"status":"stopped","STATUS":"online"`, 1)), "down", "PM2 process is not online"},
		{"uppercase-name-cannot-select", "fixture-app", "", pm2ContractList(strings.Replace(entry("other", "default", "online", 3), `"namespace":"default"`, `"namespace":"default","NAME":"fixture-app"`, 1)), "down", "PM2 process was not found"},
		{"uppercase-namespace-cannot-select", "fixture-app", "default", pm2ContractList(strings.Replace(entry("fixture-app", "other", "online", 3), `"namespace":"other"`, `"namespace":"other","NAMESPACE":"default"`, 1)), "down", "PM2 process was not found"},
		{"valid-private-string-escapes", "fixture-app", "", pm2ContractList(strings.Replace(online, `"status":"online"`, `"status":"online","env":{"ESCAPED":"\\ud800","UNICODE":"\ud83d\ude00"}`, 1)), "up", "PM2 process is online"},
		{"valid-unicode", "\u00e9\u96ea", "prod-\u00e9", pm2ContractList(entry("\u00e9\u96ea", "prod-\u00e9", "online", 1)), "up", "PM2 process is online"},
	}
	for _, status := range []string{"stopped", "stopping", "launching", "errored", "waiting restart", "one-launch-status"} {
		cases = append(cases, struct{ name, target, namespace, body, state, message string }{"status-" + status, "fixture-app", "", pm2ContractList(online, entry("fixture-app", "default", status, 1)), "down", "PM2 process is not online"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := pm2ContractConfig()
			c.Name, c.Options.Namespace = tc.target, tc.namespace
			r := pm2ContractRPC(t, c, "getMonitorData", func(id string) []byte { return pm2ContractReply(tc.body, id) }, true)
			pm2ContractResult(t, r, tc.state, tc.message)
		})
	}
}

func TestPM2ContractMalformedReplies(t *testing.T) {
	list := pm2ContractList(pm2ContractOnline)
	cases := []struct{ name, body string }{
		{"null-envelope", `null`},
		{"bare-list", `[` + pm2ContractOnline + `]`},
		{"missing-args", `{}`},
		{"null-args", `{"args":null}`},
		{"extra-callback-result", `{"args":[[],[]]}`},
		{"list-is-object", `{"args":[{}]}`},
		{"rpc-error-redacted", `{"error":"private-secret\n/private-fixture","stack":"private-stack"}`},
		{"error-with-success", `{"error":"private-secret","args":[[]]}`},
		{"wrong-method-envelope", `{"method":"restartProcessId","args":[[]]}`},
		{"duplicate-envelope-key", `{"args":[[]],"args":[[]]}`},
		{"trailing-json", list + `{}`},
		{"duplicate-name", pm2ContractList(strings.Replace(pm2ContractOnline, `"name":"fixture-app"`, `"name":"other","name":"fixture-app"`, 1))},
		{"duplicate-env-key", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"stopped","status":"online"`, 1))},
		{"duplicate-decoded-private-key", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","env":{"TOKEN":"a","\u0054OKEN":"b"}`, 1))},
		{"case-confusable-name", pm2ContractList(strings.Replace(pm2ContractOnline, `"name":`, `"Name":`, 1))},
		{"case-only-status-has-no-authority", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":`, `"STATUS":`, 1))},
		{"null-name", pm2ContractList(strings.Replace(pm2ContractOnline, `"name":"fixture-app"`, `"name":null`, 1))},
		{"null-id", pm2ContractList(strings.Replace(pm2ContractOnline, `"pm_id":0`, `"pm_id":null`, 1))},
		{"null-status", pm2ContractList(strings.Replace(pm2ContractOnline, `"online"`, `null`, 1))},
		{"null-namespace", pm2ContractList(strings.Replace(pm2ContractOnline, `"namespace":"default"`, `"namespace":null`, 1))},
		{"null-optional-env-name", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","name":null`, 1))},
		{"null-optional-env-id", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","pm_id":null`, 1))},
		{"string-id", pm2ContractList(strings.Replace(pm2ContractOnline, `"pm_id":0`, `"pm_id":"0"`, 1))},
		{"fractional-id", pm2ContractList(strings.Replace(pm2ContractOnline, `"pm_id":0`, `"pm_id":0.5`, 1))},
		{"negative-id", pm2ContractList(strings.Replace(pm2ContractOnline, `"pm_id":0`, `"pm_id":-1`, 1))},
		{"invalid-utf8-private-metadata", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, "\"status\":\"online\",\"private\":\"\xff\"", 1))},
		{"unpaired-surrogate-private-metadata", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":"\ud800"`, 1))},
		{"unpaired-low-surrogate", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":"\udfff"`, 1))},
		{"invalid-json-private-metadata", pm2ContractList(strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":NaN`, 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", func(id string) []byte { return pm2ContractReply(tc.body, id) }, false)
			pm2ContractResult(t, r, "pending", "PM2 status unavailable")
		})
	}
	for _, tc := range []struct {
		name string
		wire func(string) []byte
	}{
		{"wrong-correlation", func(id string) []byte { return pm2ContractReply(list, id+"-wrong") }},
		{"json-correlation", func(id string) []byte { return pm2ContractAMP([]byte("j:"+list), []byte(`j:"`+id+`"`)) }},
		{"wrong-payload-prefix", func(id string) []byte { return pm2ContractAMP([]byte("s:"+list), []byte("s:"+id)) }},
		{"reversed-arguments", func(id string) []byte { return pm2ContractAMP([]byte("s:"+id), []byte("j:"+list)) }},
		{"wrong-version", func(id string) []byte { b := pm2ContractReply(list, id); b[0] = 0x22; return b }},
		{"wrong-argc", func(id string) []byte { return pm2ContractAMP([]byte("j:" + list)) }},
		{"zero-length-argument", func(id string) []byte { return pm2ContractAMP(nil, []byte("s:"+id)) }},
		{"uint32-length-overflow", func(id string) []byte { return []byte{0x12, 0xff, 0xff, 0xff, 0xff} }},
		{"monitor-over-cap-announced", func(id string) []byte { return []byte{0x12, 0, 0x40, 0, 1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", tc.wire, false)
			pm2ContractResult(t, r, "pending", "PM2 status unavailable")
		})
	}
}

func TestPM2ContractVersionAndBounds(t *testing.T) {
	for _, body := range []string{`{"args":["6.0.7"]}`, `{"args":["6.0.8-dev"]}`, `{"args":[6.08]}`, `{"args":["6.0.8",[]]}`, `{"args":["6.0.8"],"error":"private-secret"}`} {
		t.Run(body, func(t *testing.T) {
			r := pm2ContractRPC(t, pm2ContractConfig(), "getVersion", func(id string) []byte { return pm2ContractReply(body, id) }, false)
			pm2ContractResult(t, r, "pending", "PM2 status unavailable")
		})
	}
	t.Run("version-over-cap-announced", func(t *testing.T) {
		r := pm2ContractRPC(t, pm2ContractConfig(), "getVersion", func(string) []byte { return []byte{0x12, 0, 0, 0x10, 1} }, false)
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	})
	for _, count := range []int{4096, 4097} {
		t.Run(fmt.Sprintf("entry-count-%d", count), func(t *testing.T) {
			entries := make([]string, count)
			for i := range entries {
				entries[i] = strings.Replace(pm2ContractOnline, `"pm_id":0`, fmt.Sprintf(`"pm_id":%d`, i), 1)
			}
			r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", func(id string) []byte { return pm2ContractReply(pm2ContractList(entries...), id) }, false)
			if count == 4096 {
				pm2ContractResult(t, r, "up", "PM2 process is online")
			} else {
				pm2ContractResult(t, r, "pending", "PM2 status unavailable")
			}
		})
	}
	// Count all JSON containers, including envelope/args/list/entry/pm2_env.
	for _, depth := range []int{32, 33} {
		t.Run(fmt.Sprintf("json-depth-%d", depth), func(t *testing.T) {
			private := strings.Repeat("[", depth-5) + "0" + strings.Repeat("]", depth-5)
			entry := strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":`+private, 1)
			r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", func(id string) []byte { return pm2ContractReply(pm2ContractList(entry), id) }, false)
			if depth == 32 {
				pm2ContractResult(t, r, "up", "PM2 process is online")
			} else {
				pm2ContractResult(t, r, "pending", "PM2 status unavailable")
			}
		})
	}
	t.Run("depth-33-empty-container", func(t *testing.T) {
		private := strings.Repeat("[", 28) + strings.Repeat("]", 28)
		entry := strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":`+private, 1)
		r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", func(id string) []byte { return pm2ContractReply(pm2ContractList(entry), id) }, false)
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	})
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("complete-frame-4MiB-plus-%d", extra), func(t *testing.T) {
			r := pm2ContractRPC(t, pm2ContractConfig(), "getMonitorData", func(id string) []byte {
				entry := strings.Replace(pm2ContractOnline, `"status":"online"`, `"status":"online","private":""`, 1)
				body := pm2ContractList(entry)
				padding := (4 << 20) + extra - len(pm2ContractReply(body, id))
				body = strings.Replace(body, `"private":""`, `"private":"`+strings.Repeat("p", padding)+`"`, 1)
				return pm2ContractReply(body, id)
			}, false)
			if extra == 0 {
				pm2ContractResult(t, r, "up", "PM2 process is online")
			} else {
				pm2ContractResult(t, r, "pending", "PM2 status unavailable")
			}
		})
	}
}

func TestPM2ContractUnavailableAndCancellation(t *testing.T) {
	c := pm2ContractConfig()
	t.Run("dial-error-no-reconnect-redacted", func(t *testing.T) {
		calls := 0
		r := check(context.Background(), c, func(context.Context, Config) (net.Conn, error) {
			calls++
			return nil, errors.New("private-secret: /private-fixture rpc.sock unavailable\nstack")
		})
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
		if calls != 1 {
			t.Fatalf("dial count = %d", calls)
		}
	})
	t.Run("already-cancelled-no-dial", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := check(ctx, c, func(context.Context, Config) (net.Conn, error) {
			t.Error("canceled check attempted manager connection")
			return nil, errors.New("forbidden")
		})
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	})
	for _, phase := range []string{"write", "version-read", "monitor-read"} {
		t.Run("cancel-"+phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			writeStarted := make(chan struct{})
			r := pm2ContractRun(t, ctx, c, func(conn net.Conn) error {
				if phase == "write" {
					select {
					case <-writeStarted:
					case <-time.After(time.Second):
						return errors.New("client never entered its first write")
					}
				} else {
					id, err := pm2ContractReadCall(conn, "getVersion", "")
					if err != nil {
						return err
					}
					if phase == "monitor-read" {
						if _, err = conn.Write(pm2ContractReply(`{"args":["6.0.8"]}`, id)); err != nil {
							return err
						}
						if _, err = pm2ContractReadCall(conn, "getMonitorData", id); err != nil {
							return err
						}
					}
				}
				cancel()
				if phase == "write" {
					// Never read: doing so would release the very write being tested.
					// This opposite-direction write stays blocked until client close.
					_, err := conn.Write([]byte{0x12})
					if err == nil {
						return errors.New("client read a reply after write cancellation")
					}
					return nil
				}
				return pm2ContractEnd(conn)
			}, writeStarted)
			pm2ContractResult(t, r, "pending", "PM2 status unavailable")
			if time.Since(start) >= 1500*time.Millisecond {
				t.Fatal("cancellation waited for the ordinary two-second timeout")
			}
		})
	}
	t.Run("partial-reply-eof", func(t *testing.T) {
		r := pm2ContractRun(t, context.Background(), c, func(conn net.Conn) error {
			id, err := pm2ContractReadCall(conn, "getVersion", "")
			if err != nil {
				return err
			}
			wire := pm2ContractReply(`{"args":["6.0.8"]}`, id)
			_, err = conn.Write(wire[:len(wire)-1])
			return err // Script closes only this deliberately truncated reply.
		})
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	})
	t.Run("config-timeout-with-manager-kept-open", func(t *testing.T) {
		c.Timeout = 80 * time.Millisecond
		r := pm2ContractRun(t, context.Background(), c, func(conn net.Conn) error {
			if _, err := pm2ContractReadCall(conn, "getVersion", ""); err != nil {
				return err
			}
			return pm2ContractEnd(conn)
		})
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	})
}

func TestPM2ContractConfiguration(t *testing.T) {
	if Type != "pm2" {
		t.Fatalf("Type = %q", Type)
	}
	base := pm2ContractConfig()
	for _, tc := range []struct {
		name  string
		edit  func(*Config)
		valid bool
	}{
		{"base", func(*Config) {}, true},
		{"numeric-name", func(c *Config) { c.Name = "0" }, true},
		{"interior-space", func(c *Config) { c.Name = "fixture app" }, true},
		{"name-256-bytes", func(c *Config) { c.Name = strings.Repeat("\u00e9", 128) }, true},
		{"namespace-256-bytes", func(c *Config) { c.Options.Namespace = strings.Repeat("n", 256) }, true},
		{"root-home", func(c *Config) { c.Options.Home = "/" }, true},
		{"socket-path-107-bytes", func(c *Config) { c.Options.Home = "/" + strings.Repeat("a", 97) }, true},
		{"windows-no-home", func(c *Config) { c.Options = Options{Transport: "windows-pipe"} }, true},
		{"timeout-two-minutes", func(c *Config) { c.Timeout = 2 * time.Minute }, true},
		{"missing-name", func(c *Config) { c.Name = "" }, false},
		{"name-over-256-bytes", func(c *Config) { c.Name = strings.Repeat("\u00e9", 129) }, false},
		{"outer-unicode-space", func(c *Config) { c.Name = "\u2003fixture-app" }, false},
		{"name-control", func(c *Config) { c.Name = "fixture\x7fapp" }, false},
		{"name-invalid-utf8", func(c *Config) { c.Name = "fixture\xff" }, false},
		{"namespace-over-256", func(c *Config) { c.Options.Namespace = strings.Repeat("n", 257) }, false},
		{"namespace-trim", func(c *Config) { c.Options.Namespace = "default " }, false},
		{"namespace-control", func(c *Config) { c.Options.Namespace = "a\u0085b" }, false},
		{"namespace-invalid-utf8", func(c *Config) { c.Options.Namespace = "\xff" }, false},
		{"zero-timeout", func(c *Config) { c.Timeout = 0 }, false},
		{"negative-timeout", func(c *Config) { c.Timeout = -1 }, false},
		{"timeout-over-two-minutes", func(c *Config) { c.Timeout = 2*time.Minute + 1 }, false},
		{"missing-transport", func(c *Config) { c.Options.Transport = "" }, false},
		{"transport-case", func(c *Config) { c.Options.Transport = "Unix" }, false},
		{"unapproved-tcp-transport", func(c *Config) { c.Options.Transport = "tcp" }, false},
		{"unix-missing-home", func(c *Config) { c.Options.Home = "" }, false},
		{"unix-relative-home", func(c *Config) { c.Options.Home = ".pm2" }, false},
		{"unix-unexpanded-home", func(c *Config) { c.Options.Home = "~/.pm2" }, false},
		{"unix-parent-home", func(c *Config) { c.Options.Home = "/tmp/../pm2" }, false},
		{"unix-repeated-slash", func(c *Config) { c.Options.Home = "/tmp//pm2" }, false},
		{"unix-trailing-slash", func(c *Config) { c.Options.Home = "/tmp/pm2/" }, false},
		{"unix-backslash", func(c *Config) { c.Options.Home = "/tmp\\pm2" }, false},
		{"unix-control", func(c *Config) { c.Options.Home = "/tmp/\x00pm2" }, false},
		{"unix-trim", func(c *Config) { c.Options.Home = "/tmp/pm2 " }, false},
		{"unix-invalid-utf8", func(c *Config) { c.Options.Home = "/tmp/\xff" }, false},
		{"socket-path-108-bytes", func(c *Config) { c.Options.Home = "/" + strings.Repeat("a", 98) }, false},
		{"socket-path-utf8-byte-overflow", func(c *Config) { c.Options.Home = "/" + strings.Repeat("\u00e9", 49) }, false},
		{"windows-home", func(c *Config) { c.Options = Options{Transport: "windows-pipe", Home: "/tmp"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.edit(&c)
			if got := Validate(c); (got == nil) != tc.valid {
				t.Fatalf("Validate valid=%v: %v", tc.valid, got)
			}
			if !tc.valid {
				r := check(context.Background(), c, func(context.Context, Config) (net.Conn, error) {
					t.Error("invalid configuration attempted dial")
					return nil, errors.New("forbidden")
				})
				pm2ContractResult(t, r, "down", "invalid PM2 configuration")
			}
		})
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"transport":null}`, `{"Transport":"unix","home":"/tmp"}`,
		`{"transport":"unix","home":"/tmp","extra":1}`, `{"transport":"unix","transport":"unix","home":"/tmp"}`,
		`{"transport":"unix","home":"/tmp","home":"/tmp"}`, `{"transport":"unix","home":"/tmp","namespace":null}`,
		`{"transport":7,"home":"/tmp"}`, `{"transport":"unix","home":false}`, `{"transport":"unix","home":"/tmp","namespace":[]}`,
		`{"transport":"unix","home":"/tmp","Namespace":"default"}`, `{"transport":"unix","home":"/tmp","namespace":"a","namespace":"a"}`,
		`{"transport":"unix","home":"/tmp"} {}`,
	} {
		t.Run("strict-options-"+raw, func(t *testing.T) {
			var o Options
			if err := json.Unmarshal([]byte(raw), &o); err == nil {
				t.Fatal("Options.UnmarshalJSON accepted an invalid shape/key/type")
			}
		})
	}
	for _, raw := range []string{`{"transport":"windows-pipe","home":""}`, `{"transport":"unix","home":"/tmp","namespace":""}`, `{"transport":"unix","home":"/tmp","namespace":"\ud800"}`} {
		var o Options
		err := json.Unmarshal([]byte(raw), &o)
		c := base
		c.Options = o
		if err == nil && Validate(c) == nil {
			t.Fatalf("accepted forbidden option presence/value: %s", raw)
		}
	}
	pm2ContractResult(t, Check(context.Background(), Config{}), "down", "invalid PM2 configuration")
	pm2ContractResult(t, CheckInWorker(context.Background(), Config{}), "down", "invalid PM2 configuration")
}

func TestPM2ContractWorkerDecoder(t *testing.T) {
	good := `{"type":"pm2","name":"fixture-app","timeout":1000000000,"options":{"transport":"unix","home":"/private-fixture"}}`
	h := WorkerHandler()
	if h.Validate == nil || h.Check == nil {
		t.Fatal("incomplete WorkerHandler")
	}
	if err := h.Validate([]byte(good)); err != nil {
		t.Fatalf("valid worker config: %v", err)
	}
	// JSON escaping alone can put legal name/namespace above the 2 KiB result
	// cap; the independent worker configuration ceiling is 8 KiB.
	c := pm2ContractConfig()
	c.Name, c.Options.Namespace = strings.Repeat("<", 256), strings.Repeat("<", 256)
	large, err := json.Marshal(struct {
		Type string `json:"type"`
		Config
	}{Type: Type, Config: c})
	if err != nil || len(large) <= 2048 || len(large) > 8192 {
		t.Fatal("bad escaped-config fixture")
	}
	if err := h.Validate(large); err != nil {
		t.Fatalf("valid configuration above 2 KiB rejected: %v", err)
	}
	for _, raw := range []string{
		`null`, `{}`, strings.Replace(good, `"type":"pm2"`, `"type":"system-service"`, 1),
		strings.Replace(good, `"name":"fixture-app"`, `"Name":"fixture-app"`, 1),
		strings.Replace(good, `"name":"fixture-app"`, `"name":"fixture-app","name":"fixture-app"`, 1),
		strings.Replace(good, `"type":"pm2"`, `"type":"pm2","type":"pm2"`, 1),
		strings.Replace(good, `"timeout":1000000000`, `"timeout":null`, 1),
		strings.Replace(good, `"timeout":1000000000`, `"timeout":"1000000000"`, 1),
		strings.Replace(good, `"options":`, `"unknown":false,"options":`, 1),
		strings.Replace(good, `"options":`, `"options":null,"options":`, 1),
		strings.Replace(good, `"transport":"unix","home":"/private-fixture"`, `"transport":"windows-pipe","home":""`, 1),
		good + `{}`, strings.Repeat(" ", 8193-len(good)) + good,
	} {
		if err := h.Validate([]byte(raw)); err == nil {
			t.Fatalf("worker accepted malformed config: %.200s", raw)
		}
		// A failed decode must clear previously accepted private request state.
		pm2ContractResult(t, h.Check(context.Background()), "down", "invalid PM2 configuration")
	}
}
