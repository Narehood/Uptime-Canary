package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/pbkdf2"
)

func readMongoRequest(c net.Conn) (int32, uint32, bson.Raw, error) {
	var h [16]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return 0, 0, nil, err
	}
	n := int(binary.LittleEndian.Uint32(h[:]))
	if n < 16 || n > maxBody {
		return 0, 0, nil, fmt.Errorf("invalid fixture request size")
	}
	body := make([]byte, n-16)
	if _, err := io.ReadFull(c, body); err != nil {
		return 0, 0, nil, err
	}
	op := binary.LittleEndian.Uint32(h[12:])
	var doc []byte
	if op == 2004 {
		i := 4
		for i < len(body) && body[i] != 0 {
			i++
		}
		i += 9
		if i >= len(body) {
			return 0, 0, nil, fmt.Errorf("invalid OP_QUERY")
		}
		doc = body[i:]
	} else if op == 2013 && len(body) > 5 && body[4] == 0 {
		doc = body[5:]
	} else {
		return 0, 0, nil, fmt.Errorf("unexpected MongoDB opcode")
	}
	return int32(binary.LittleEndian.Uint32(h[4:])), op, bson.Raw(doc), nil
}
func writeMongoReply(c net.Conn, id int32, requestOp uint32, doc bson.D) error {
	b, err := bson.Marshal(doc)
	if err != nil {
		return err
	}
	offset := 21
	op := uint32(2013)
	if requestOp == 2004 {
		offset = 36
		op = 1
	}
	p := make([]byte, offset+len(b))
	binary.LittleEndian.PutUint32(p, uint32(len(p)))
	binary.LittleEndian.PutUint32(p[4:], 123)
	binary.LittleEndian.PutUint32(p[8:], uint32(id))
	binary.LittleEndian.PutUint32(p[12:], op)
	if op == 1 {
		binary.LittleEndian.PutUint32(p[32:], 1)
	}
	copy(p[offset:], b)
	_, err = c.Write(p)
	return err
}
func mongoFixture(t *testing.T, encrypted bool, variant string) (string, *TLSConfig) {
	t.Helper()
	tc, trust := testTLS(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns[c] = true
			mu.Unlock()
			wg.Add(1)
			go func(raw net.Conn) {
				defer wg.Done()
				defer raw.Close()
				_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
				var c net.Conn = raw
				if encrypted {
					sc := tls.Server(c, tc)
					if sc.Handshake() != nil {
						return
					}
					c = sc
				}
				var firstBare, serverFirst string
				for {
					id, op, doc, err := readMongoRequest(c)
					if err != nil {
						return
					}
					elements, err := doc.Elements()
					if err != nil || len(elements) == 0 {
						t.Error("invalid MongoDB request")
						return
					}
					command := elements[0].Key()
					reply := bson.D{{Key: "ok", Value: int32(1)}}
					switch strings.ToLower(command) {
					case "ismaster", "hello":
						reply = append(reply, bson.E{Key: "ismaster", Value: true}, bson.E{Key: "isWritablePrimary", Value: true}, bson.E{Key: "helloOk", Value: true}, bson.E{Key: "minWireVersion", Value: int32(0)}, bson.E{Key: "maxWireVersion", Value: int32(25)}, bson.E{Key: "maxBsonObjectSize", Value: int32(16777216)}, bson.E{Key: "maxMessageSizeBytes", Value: int32(1000000000)}, bson.E{Key: "hosts", Value: bson.A{"192.0.2.1:1"}})
					case "saslstart":
						if doc.Lookup("mechanism").StringValue() != "SCRAM-SHA-256" {
							t.Error("incorrect SCRAM mechanism")
							return
						}
						_, payload := doc.Lookup("payload").Binary()
						first := string(payload)
						if !strings.HasPrefix(first, "n,,n=monitor,r=") {
							t.Error("incorrect SCRAM first message")
							return
						}
						firstBare = first[3:]
						nonce := strings.Split(firstBare, ",r=")[1]
						if variant == "auth-refused" {
							reply = bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: 18}, {Key: "errmsg", Value: "fixture-secret private-value"}}
							break
						}
						iterations := "4096"
						if variant == "auth-cost" {
							iterations = "2147483647"
						}
						serverFirst = "r=" + nonce + "fixture,s=" + base64.StdEncoding.EncodeToString([]byte("fixture-salt")) + ",i=" + iterations
						reply = append(reply, bson.E{Key: "conversationId", Value: int32(1)}, bson.E{Key: "done", Value: false}, bson.E{Key: "payload", Value: bson.Binary{Data: []byte(serverFirst)}})
					case "saslcontinue":
						_, payload := doc.Lookup("payload").Binary()
						final := string(payload)
						i := strings.LastIndex(final, ",p=")
						if i < 0 {
							t.Error("missing SCRAM proof")
							return
						}
						authMessage := firstBare + "," + serverFirst + "," + final[:i]
						salted := pbkdf2.Key([]byte("fixture-secret"), []byte("fixture-salt"), 4096, 32, sha256.New)
						hash := func(key []byte, s string) []byte {
							h := hmac.New(sha256.New, key)
							h.Write([]byte(s))
							return h.Sum(nil)
						}
						clientKey := hash(salted, "Client Key")
						stored := sha256.Sum256(clientKey)
						signature := hash(stored[:], authMessage)
						for i := range signature {
							signature[i] ^= clientKey[i]
						}
						proof, err := base64.StdEncoding.DecodeString(final[i+3:])
						if err != nil || subtle.ConstantTimeCompare(proof, signature) != 1 {
							t.Error("incorrect SCRAM client proof")
							return
						}
						serverFinal := "v=" + base64.StdEncoding.EncodeToString(hash(hash(salted, "Server Key"), authMessage))
						reply = append(reply, bson.E{Key: "conversationId", Value: int32(1)}, bson.E{Key: "done", Value: true}, bson.E{Key: "payload", Value: bson.Binary{Data: []byte(serverFinal)}})
					case "ping":
						if variant == "ping-error" {
							reply = bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: 13}, {Key: "errmsg", Value: "fixture-secret private-value"}}
						}
						if variant == "oversize" {
							var h [16]byte
							binary.LittleEndian.PutUint32(h[:], 0x7fffffff)
							binary.LittleEndian.PutUint32(h[12:], 2013)
							_, _ = c.Write(h[:])
							return
						}
						if variant == "compressed" {
							var h [16]byte
							binary.LittleEndian.PutUint32(h[:], 16)
							binary.LittleEndian.PutUint32(h[12:], 2012)
							_, _ = c.Write(h[:])
							return
						}
						if variant == "deadline" {
							_, _ = io.Copy(io.Discard, c)
							return
						}
					case "find":
						if doc.Lookup("find").StringValue() != "status" || doc.Lookup("limit").Int64() != 1 || !doc.Lookup("singleBatch").Boolean() {
							t.Error("incorrect bounded MongoDB find")
							return
						}
						if doc.Lookup("filter").Document().Lookup("key").StringValue() != "ready" {
							t.Error("incorrect MongoDB equality filter")
							return
						}
						batch := bson.A{bson.D{{Key: "value", Value: int64(9007199254740993)}}}
						if variant == "no-rows" {
							batch = bson.A{}
						}
						if variant == "null" {
							batch = bson.A{bson.D{{Key: "value", Value: nil}}}
						}
						switch variant {
						case "string":
							batch = bson.A{bson.D{{Key: "value", Value: "ready"}}}
						case "boolean":
							batch = bson.A{bson.D{{Key: "value", Value: true}}}
						case "double":
							batch = bson.A{bson.D{{Key: "value", Value: 1.25}}}
						case "int32":
							batch = bson.A{bson.D{{Key: "value", Value: int32(42)}}}
						case "decimal":
							decimal, _ := bson.ParseDecimal128("9007199254740993.125")
							batch = bson.A{bson.D{{Key: "value", Value: decimal}}}
						case "scalar-limit":
							batch = bson.A{bson.D{{Key: "value", Value: strings.Repeat("x", 65537)}}}
						case "document-limit":
							batch = bson.A{bson.D{{Key: "value", Value: strings.Repeat("x", maxBody)}}}
						case "depth-limit":
							var nested any = "leaf"
							for i := 0; i < 34; i++ {
								nested = bson.D{{Key: "next", Value: nested}}
							}
							batch = bson.A{bson.D{{Key: "value", Value: nested}}}
						}
						reply = append(reply, bson.E{Key: "cursor", Value: bson.D{{Key: "id", Value: int64(0)}, {Key: "ns", Value: "monitor.status"}, {Key: "firstBatch", Value: batch}}})
						if variant == "query-error" {
							reply = bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: 13}, {Key: "errmsg", Value: "fixture-secret private-value"}}
						}
					case "endsessions":
					default:
						t.Errorf("unexpected MongoDB command %q", command)
						return
					}
					if writeMongoReply(c, id, op, reply) != nil {
						return
					}
				}
			}(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String(), trust
}
func TestMongoDBProtocol(t *testing.T) {
	for _, tc := range []struct {
		variant   string
		auth, tls bool
		want      string
	}{{"ping", false, false, "up"}, {"ping", true, true, "up"}, {"auth-refused", true, false, "down"}, {"auth-cost", true, false, "down"}, {"ping-error", false, false, "down"}, {"oversize", false, false, "down"}, {"compressed", false, false, "down"}, {"deadline", false, false, "down"}} {
		t.Run(fmt.Sprintf("%s/auth=%v/tls=%v", tc.variant, tc.auth, tc.tls), func(t *testing.T) {
			a, trust := mongoFixture(t, tc.tls, tc.variant)
			dsn := "mongodb://"
			if tc.auth {
				dsn += "monitor:fixture-secret@"
			}
			dsn += a + "/monitor"
			c := MongoDBConfig{DSN: dsn, TLSMode: "plain"}
			if tc.tls {
				c.TLSMode = "tls"
				c.TLS = trust
			}
			expectState(t, Config{Type: MongoDB, Timeout: 400 * time.Millisecond, MongoDB: &c}, tc.want)
		})
	}
}
func TestMongoDBQueryAndTLS(t *testing.T) {
	for _, variant := range []string{"query", "no-rows", "null", "query-error", "mismatch", "string", "boolean", "double", "int32", "decimal", "scalar-limit", "document-limit", "depth-limit"} {
		t.Run(variant, func(t *testing.T) {
			a, _ := mongoFixture(t, false, variant)
			c := MongoDBConfig{DSN: "mongodb://" + a + "/monitor", TLSMode: "plain", Collection: "status", Filter: []byte(`{"key":"ready"}`), ScalarPath: []string{"value"}, Condition: &Condition{Operator: "eq", Expected: []byte(`9007199254740993`)}}
			want := "down"
			if variant == "query" {
				want = "up"
			}
			if variant == "null" {
				c.Condition.Expected = []byte(`null`)
				want = "up"
			}
			if variant == "mismatch" {
				c.Condition.Expected = []byte(`1`)
			}
			switch variant {
			case "string":
				c.Condition.Expected = []byte(`"ready"`)
				want = "up"
			case "boolean":
				c.Condition.Expected = []byte(`true`)
				want = "up"
			case "double":
				c.Condition.Expected = []byte(`1.25`)
				want = "up"
			case "int32":
				c.Condition.Expected = []byte(`42`)
				want = "up"
			case "decimal":
				c.Condition.Expected = []byte(`9007199254740993.125`)
				want = "up"
			}
			expectState(t, Config{Type: MongoDB, Timeout: time.Second, MongoDB: &c}, want)
		})
	}
	a, _ := mongoFixture(t, true, "ping")
	expectState(t, Config{Type: MongoDB, Timeout: 300 * time.Millisecond, MongoDB: &MongoDBConfig{DSN: "mongodb://" + a + "/monitor"}}, "down")
}
func TestMongoDBValidationAndCancellation(t *testing.T) {
	for _, dsn := range []string{"mongodb+srv://monitor:secret@example.com/db", "mongodb://host1,host2/db", "mongodb://host/db?tlsCAFile=secret", "mongodb://host/db?authMechanism=MONGODB-AWS", "mongodb://u:p@host/db?authSource=a&authSource=b", "mongodb://host/db?replicaSet=test", "mongodb://host/db?directConnection=false"} {
		if _, err := mongoEndpoint(MongoDBConfig{DSN: dsn}); err == nil {
			t.Fatalf("accepted unsafe MongoDB DSN %q", dsn)
		}
	}
	for _, filter := range []string{`{"$where":"secret"}`, `{"x":{"$gt":1}}`, `{"x":1.1}`, `{"x":1,"x":2}`, `[]`, `{"x":1} {}`} {
		if _, err := mongoFilter([]byte(filter)); err == nil {
			t.Fatal("accepted unsafe filter")
		}
	}
	a := fixture(t, func(c net.Conn) error { _, _ = io.Copy(io.Discard, c); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	r := Check(ctx, Config{Type: MongoDB, Timeout: time.Second, MongoDB: &MongoDBConfig{DSN: "mongodb://" + a + "/monitor"}})
	if r.Message != "service check canceled" {
		t.Fatal(r)
	}
}

func TestMongoDBIgnoresAmbientLoggingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-open.log")
	t.Setenv("MONGODB_LOG_PATH", path)
	t.Setenv("MONGODB_LOG_ALL", "debug")
	t.Setenv("MONGODB_LOG_MAX_DOCUMENT_LENGTH", "2147483647")
	a, _ := mongoFixture(t, false, "ping")
	expectState(t, Config{Type: MongoDB, Timeout: time.Second, MongoDB: &MongoDBConfig{DSN: "mongodb://" + a + "/monitor", TLSMode: "plain"}}, "up")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("MongoDB driver opened ambient log path")
	}
}
