package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"uptime-canary/apps/node-go/internal/jsonquery"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/x/bsonx/bsoncore"
)

// MongoDBConfig pings one explicit endpoint. Optional Collection performs a
// find-one with a flat equality Filter, then checks a scalar field path.
type MongoDBConfig struct {
	DSN        string            `json:"dsn"`
	TLSMode    string            `json:"tlsMode,omitempty"`
	TLS        *TLSConfig        `json:"tls,omitempty"`
	Collection string            `json:"collection,omitempty"`
	Filter     json.RawMessage   `json:"filter,omitempty"`
	ScalarPath []string          `json:"scalarPath,omitempty"`
	Condition  *Condition        `json:"condition,omitempty"`
	JSONata    *jsonquery.Config `json:"jsonata,omitempty"`
}

type mongoTarget struct{ address, database, authSource, mechanism, user, password string }

func mongoEndpoint(c MongoDBConfig) (mongoTarget, error) {
	bad := errors.New("MongoDB DSN requires one mongodb URL endpoint and only explicit SCRAM authentication options")
	if len(c.DSN) == 0 || len(c.DSN) > 16384 {
		return mongoTarget{}, bad
	}
	u, err := url.Parse(c.DSN)
	if err != nil || u.Scheme != "mongodb" || u.Opaque != "" || u.Hostname() == "" || u.Fragment != "" || strings.ContainsAny(u.Hostname(), ",/\\\x00") {
		return mongoTarget{}, bad
	}
	port := u.Port()
	if port == "" {
		port = "27017"
	}
	t := mongoTarget{address: net.JoinHostPort(u.Hostname(), port), database: strings.TrimPrefix(u.Path, "/"), mechanism: "SCRAM-SHA-256"}
	if validAddress(t.address) != nil || len(t.database) > 128 || strings.ContainsAny(t.database, "/\\\x00\r\n \".$") {
		return mongoTarget{}, bad
	}
	if t.database == "" {
		t.database = "admin"
	}
	t.authSource = t.database
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return mongoTarget{}, bad
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" || len(v[0]) > 128 {
			return mongoTarget{}, bad
		}
		switch k {
		case "authSource":
			if strings.ContainsAny(v[0], "/\\\x00\r\n \".$") {
				return mongoTarget{}, bad
			}
			t.authSource = v[0]
		case "authMechanism":
			if v[0] != "SCRAM-SHA-256" && v[0] != "SCRAM-SHA-1" {
				return mongoTarget{}, bad
			}
			t.mechanism = v[0]
		default:
			return mongoTarget{}, bad
		}
	}
	if u.User != nil {
		t.user = u.User.Username()
		t.password, _ = u.User.Password()
		if t.user == "" || len(t.user) > 1024 || len(t.password) > 4096 || strings.ContainsRune(t.user, 0) || strings.ContainsRune(t.password, 0) {
			return mongoTarget{}, bad
		}
	} else if len(q) > 0 {
		return mongoTarget{}, bad
	}
	return t, nil
}

func (c MongoDBConfig) validate() error {
	t, err := mongoEndpoint(c)
	if err != nil {
		return err
	}
	if err = validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	h, _, _ := net.SplitHostPort(t.address)
	if c.TLSMode != "plain" {
		if _, err = tlsSettings(c.TLS, h); err != nil {
			return err
		}
	}
	if len(c.Collection) > 128 || strings.ContainsAny(c.Collection, "\x00\r\n$") || strings.HasPrefix(c.Collection, "system.") {
		return errors.New("invalid MongoDB collection")
	}
	if c.Collection == "" && (len(c.Filter) > 0 || len(c.ScalarPath) > 0 || c.Condition != nil || c.JSONata != nil) {
		return errors.New("MongoDB query options require collection")
	}
	if (c.Condition == nil) != (len(c.ScalarPath) == 0) {
		return errors.New("MongoDB scalar path and condition must be paired")
	}
	if err = validateScalarPath(c.ScalarPath); err != nil {
		return err
	}
	if c.Condition != nil {
		if err = c.Condition.validate(); err != nil {
			return err
		}
	}
	if c.JSONata != nil && c.JSONata.Validate() != nil {
		return errors.New("invalid MongoDB JSONata condition")
	}
	_, err = mongoFilter(c.Filter)
	return err
}

func validateScalarPath(path []string) error {
	if len(path) > 16 {
		return errors.New("scalar path exceeds limit")
	}
	for _, s := range path {
		if s == "" || len(s) > 128 || strings.ContainsAny(s, "\x00\r\n") {
			return errors.New("invalid scalar path")
		}
	}
	return nil
}
func mongoFilter(raw json.RawMessage) (bson.D, error) {
	if len(raw) == 0 {
		return bson.D{}, nil
	}
	if len(raw) > 16384 {
		return nil, errors.New("MongoDB filter exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	// Stream keys to reject duplicate fields as well as operators and nested data.
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("MongoDB filter must be a flat equality object")
	}
	filter := bson.D{}
	seen := map[string]bool{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return nil, errors.New("invalid MongoDB filter")
		}
		key, ok := tok.(string)
		if !ok || len(key) == 0 || len(key) > 128 || strings.ContainsAny(key, "$\x00\r\n.") || seen[key] || len(filter) >= 16 {
			return nil, errors.New("invalid MongoDB filter field")
		}
		seen[key] = true
		var v any
		if d.Decode(&v) != nil {
			return nil, errors.New("invalid MongoDB filter")
		}
		switch n := v.(type) {
		case nil, bool, string:
		case json.Number:
			i, e := n.Int64()
			if e != nil {
				return nil, errors.New("MongoDB filter numbers must be signed 64-bit integers")
			}
			v = i
		default:
			return nil, errors.New("MongoDB filter values must be scalar")
		}
		filter = append(filter, bson.E{Key: key, Value: v})
	}
	if _, err = d.Token(); err != nil {
		return nil, errors.New("invalid MongoDB filter")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid MongoDB filter")
	}
	return filter, nil
}

type mongoDialer struct {
	ctx      context.Context
	config   MongoDBConfig
	address  string
	attempts atomic.Int32
}

type silentMongoLogger struct{}

func (silentMongoLogger) Info(int, string, ...any)    {}
func (silentMongoLogger) Error(error, string, ...any) {}
func mongoLoggerOptions() *options.LoggerOptions {
	// Supplying every level, the sink and a nonzero width prevents environment
	// defaults from enabling command logs or opening MONGODB_LOG_PATH files.
	l := options.Logger().SetSink(silentMongoLogger{}).SetMaxDocumentLength(1)
	for _, component := range []options.LogComponent{options.LogComponentAll, options.LogComponentCommand, options.LogComponentTopology, options.LogComponentServerSelection, options.LogComponentConnection} {
		l.SetComponentLevel(component, options.LogLevel(0))
	}
	return l
}

func (d *mongoDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != d.address || d.attempts.Add(1) > 4 {
		return nil, errors.New("MongoDB endpoint or connection limit")
	}
	c, err := dialService(d.ctx, d.address, d.config.TLSMode, d.config.TLS)
	if err != nil {
		return nil, err
	}
	return &mongoFrameConn{Conn: c}, nil
}

// Inspect the entire fixed header before returning the length to the driver,
// which otherwise allocates from that length before reading the body. Compression
// is disabled and rejected so a small message cannot inflate outside the budget.
type mongoFrameConn struct {
	net.Conn
	frame []byte
}

func (c *mongoFrameConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.frame) == 0 {
		var header [16]byte
		if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
			return 0, err
		}
		n := binary.LittleEndian.Uint32(header[:4])
		opcode := binary.LittleEndian.Uint32(header[12:])
		if n < 16 || n > maxBody || (opcode != 1 && opcode != 2013) {
			return 0, errors.New("MongoDB wire frame exceeds limit or uses unsupported opcode")
		}
		frame := make([]byte, int(n))
		copy(frame, header[:])
		if _, err := io.ReadFull(c.Conn, frame[16:]); err != nil {
			return 0, err
		}
		if !validMongoFrame(frame, opcode) {
			return 0, errors.New("MongoDB malformed frame or authentication cost limit")
		}
		c.frame = frame
	}
	n := copy(p, c.frame)
	c.frame = c.frame[n:]
	return n, nil
}

func validMongoFrame(frame []byte, opcode uint32) bool {
	var doc []byte
	if opcode == 1 {
		if len(frame) < 41 || binary.LittleEndian.Uint32(frame[32:36]) != 1 {
			return false
		}
		doc = frame[36:]
	} else {
		// A command response is one kind-0 document. Checksums are allowed;
		// document sequences and exhaust streaming are outside this subset.
		if len(frame) < 26 || frame[20] != 0 {
			return false
		}
		flags := binary.LittleEndian.Uint32(frame[16:20])
		if flags & ^uint32(1) != 0 {
			return false
		}
		doc = frame[21:]
		if flags&1 != 0 {
			if len(doc) < 9 {
				return false
			}
			doc = doc[:len(doc)-4]
		}
	}
	nodes := 0
	return boundedMongoDocument(doc, 0, &nodes)
}
func boundedMongoDocument(doc []byte, depth int, nodes *int) bool {
	if depth > 32 || len(doc) < 5 || int64(int32(binary.LittleEndian.Uint32(doc))) != int64(len(doc)) || doc[len(doc)-1] != 0 {
		return false
	}
	rest := doc[4 : len(doc)-1]
	for len(rest) > 0 {
		*nodes++
		if *nodes > 4096 {
			return false
		}
		e, next, ok := bsoncore.ReadElement(rest)
		if !ok {
			return false
		}
		rest = next
		v, err := e.ValueErr()
		if err != nil {
			return false
		}
		switch v.Type {
		case bsoncore.TypeEmbeddedDocument, bsoncore.TypeArray:
			if !boundedMongoDocument(v.Data, depth+1, nodes) {
				return false
			}
		case bsoncore.TypeCodeWithScope:
			return false // no executable BSON or recursively scoped code
		default:
			if e.Validate() != nil {
				return false
			}
		}
		if e.CompareKey([]byte("payload")) && v.Type == bsoncore.TypeBinary {
			_, payload, ok := v.BinaryOK()
			if !ok || len(payload) > 8192 {
				return false
			}
			for _, part := range strings.Split(string(payload), ",") {
				if strings.HasPrefix(part, "i=") {
					n, err := strconv.Atoi(part[2:])
					if err != nil || n < 4096 || n > 100000 {
						return false
					}
				}
			}
		}
	}
	return true
}

func checkMongoDB(ctx context.Context, c MongoDBConfig) Result {
	t, err := mongoEndpoint(c)
	if err != nil {
		return down("invalid MongoDB configuration")
	}
	d := &mongoDialer{ctx: ctx, config: c, address: t.address}
	opts := options.Client().SetHosts([]string{t.address}).SetDirect(true).SetDialer(d).SetRetryReads(false).SetRetryWrites(false).SetMaxPoolSize(1).SetMinPoolSize(0).SetMaxConnecting(1).SetServerMonitoringMode("poll").SetAppName("uptime-canary").SetLoggerOptions(mongoLoggerOptions())
	if t.user != "" {
		opts.SetAuth(options.Credential{AuthMechanism: t.mechanism, AuthSource: t.authSource, Username: t.user, Password: t.password, PasswordSet: true})
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return down("MongoDB client configuration failed")
	}
	defer client.Disconnect(ctx)
	if client.Ping(ctx, readpref.Primary()) != nil {
		return down("MongoDB connection, TLS, authentication, or ping failed")
	}
	if c.Collection == "" {
		return up("MongoDB ping succeeded")
	}
	filter, _ := mongoFilter(c.Filter)
	cmd := bson.D{{Key: "find", Value: c.Collection}, {Key: "filter", Value: filter}, {Key: "limit", Value: int64(1)}, {Key: "batchSize", Value: int32(1)}, {Key: "singleBatch", Value: true}}
	raw, err := client.Database(t.database).RunCommand(ctx, cmd).Raw()
	if err != nil || len(raw) > maxBody {
		return down("MongoDB monitoring query failed or exceeded limit")
	}
	batch, err := raw.LookupErr("cursor", "firstBatch")
	if err != nil || batch.Type != bson.TypeArray {
		return down("MongoDB query returned malformed result")
	}
	values, err := batch.Array().Values()
	if err != nil || len(values) != 1 || values[0].Type != bson.TypeEmbeddedDocument {
		return down("MongoDB query returned no unique bounded document")
	}
	if id, ok := raw.Lookup("cursor", "id").Int64OK(); !ok || id != 0 {
		return down("MongoDB query returned an unexpected open cursor")
	}
	if c.Condition != nil {
		v, err := values[0].Document().LookupErr(c.ScalarPath...)
		if err != nil {
			return down("MongoDB scalar field was not found")
		}
		value, isNull, ok := mongoScalar(v)
		if !ok || !c.Condition.matches(value, isNull) {
			return down("MongoDB scalar condition did not match")
		}
	}
	if c.JSONata != nil {
		document, err := bson.MarshalExtJSON(values[0].Document(), false, false)
		if err != nil {
			return down("MongoDB document cannot be represented as JSON")
		}
		return evaluateJSONQuery(ctx, *c.JSONata, document, "MongoDB")
	}
	return up("MongoDB monitoring query succeeded")
}

func mongoScalar(v bson.RawValue) ([]byte, bool, bool) {
	var s string
	switch v.Type {
	case bson.TypeNull:
		return nil, true, true
	case bson.TypeString:
		x, ok := v.StringValueOK()
		if !ok {
			return nil, false, false
		}
		s = x
	case bson.TypeBoolean:
		x, ok := v.BooleanOK()
		if !ok {
			return nil, false, false
		}
		if x {
			s = "true"
		} else {
			s = "false"
		}
	case bson.TypeInt32:
		x, ok := v.Int32OK()
		if !ok {
			return nil, false, false
		}
		s = strconv.FormatInt(int64(x), 10)
	case bson.TypeInt64:
		x, ok := v.Int64OK()
		if !ok {
			return nil, false, false
		}
		s = strconv.FormatInt(x, 10)
	case bson.TypeDouble:
		x, ok := v.DoubleOK()
		if !ok {
			return nil, false, false
		}
		b, err := json.Marshal(x)
		if err != nil {
			return nil, false, false
		}
		s = string(b)
	case bson.TypeDecimal128:
		x, ok := v.Decimal128OK()
		if !ok {
			return nil, false, false
		}
		s = x.String()
	default:
		return nil, false, false
	}
	if len(s) > 65536 {
		return nil, false, false
	}
	return []byte(s), false, true
}
