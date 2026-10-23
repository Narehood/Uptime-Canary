// Package services implements bounded application-protocol monitoring engines.
package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

type Type string

const (
	Docker    Type = "docker"
	Redis     Type = "redis"
	Postgres  Type = "postgres"
	MySQL     Type = "mysql"
	MQTT      Type = "mqtt"
	SFTP      Type = "sftp"
	MongoDB   Type = "mongodb"
	SQLServer Type = "sqlserver"
	NTP       Type = "ntp"
	GRPC      Type = "grpc"
	SNMP      Type = "snmp"
	RADIUS    Type = "radius"
	SIP       Type = "sip-options"
	RabbitMQ  Type = "rabbitmq"
	Kafka     Type = "kafka-producer"
	Oracle    Type = "oracledb"
	maxBody        = 1 << 20
	maxPEM         = 256 << 10
)

type Config struct {
	Type      Type            `json:"type"`
	Timeout   time.Duration   `json:"timeout"`
	Docker    *DockerConfig   `json:"docker,omitempty"`
	Redis     *RedisConfig    `json:"redis,omitempty"`
	Postgres  *SQLConfig      `json:"postgres,omitempty"`
	MySQL     *SQLConfig      `json:"mysql,omitempty"`
	MQTT      *MQTTConfig     `json:"mqtt,omitempty"`
	SFTP      *SFTPConfig     `json:"sftp,omitempty"`
	MongoDB   *MongoDBConfig  `json:"mongodb,omitempty"`
	SQLServer *SQLConfig      `json:"sqlserver,omitempty"`
	NTP       *NTPConfig      `json:"ntp,omitempty"`
	GRPC      *GRPCConfig     `json:"grpc,omitempty"`
	SNMP      *SNMPConfig     `json:"snmp,omitempty"`
	RADIUS    *RADIUSConfig   `json:"radius,omitempty"`
	SIP       *SIPConfig      `json:"sip,omitempty"`
	RabbitMQ  *RabbitMQConfig `json:"rabbitmq,omitempty"`
	Kafka     *KafkaConfig    `json:"kafka,omitempty"`
	Oracle    *SQLConfig      `json:"oracle,omitempty"`
}

type Result struct {
	State     string  `json:"state"`
	Message   string  `json:"message"`
	LatencyMs float64 `json:"latencyMs"`
}

type TLSConfig struct {
	ServerName         string `json:"serverName,omitempty"`
	CAPEM              string `json:"caPem,omitempty"`
	ClientCertPEM      string `json:"clientCertPem,omitempty"`
	ClientKeyPEM       string `json:"clientKeyPem,omitempty"`
	IgnoreVerification bool   `json:"ignoreVerification,omitempty"`
}

func Validate(c Config) error { return c.Validate() }
func (c Config) Validate() error {
	if c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return errors.New("timeout must be positive and at most five minutes")
	}
	n := 0
	for _, ok := range []bool{c.Docker != nil, c.Redis != nil, c.Postgres != nil, c.MySQL != nil, c.MQTT != nil, c.SFTP != nil, c.MongoDB != nil, c.SQLServer != nil, c.NTP != nil, c.GRPC != nil, c.SNMP != nil, c.RADIUS != nil, c.SIP != nil, c.RabbitMQ != nil, c.Kafka != nil, c.Oracle != nil} {
		if ok {
			n++
		}
	}
	if n != 1 {
		return errors.New("exactly one service option is required")
	}
	switch c.Type {
	case Docker:
		if c.Docker != nil {
			return c.Docker.validate()
		}
	case Redis:
		if c.Redis != nil {
			return c.Redis.validate()
		}
	case Postgres:
		if c.Postgres != nil {
			return c.Postgres.validate(Postgres)
		}
	case MySQL:
		if c.MySQL != nil {
			return c.MySQL.validate(MySQL)
		}
	case MQTT:
		if c.MQTT != nil {
			return c.MQTT.validate()
		}
	case SFTP:
		if c.SFTP != nil {
			return c.SFTP.validate()
		}
	case MongoDB:
		if c.MongoDB != nil {
			return c.MongoDB.validate()
		}
	case SQLServer:
		if c.SQLServer != nil {
			return validateSQLServer(*c.SQLServer)
		}
	case NTP:
		if c.NTP != nil {
			return c.NTP.validate()
		}
	case GRPC:
		if c.GRPC != nil {
			return c.GRPC.validate()
		}
	case SNMP:
		if c.SNMP != nil {
			return c.SNMP.validate()
		}
	case RADIUS:
		if c.RADIUS != nil {
			return c.RADIUS.validate()
		}
	case SIP:
		if c.SIP != nil {
			return c.SIP.validate()
		}
	case Kafka:
		if c.Kafka != nil {
			return c.Kafka.validate()
		}
	case Oracle:
		if c.Oracle != nil {
			return validateOracle(*c.Oracle)
		}
	case RabbitMQ:
		if c.RabbitMQ != nil {
			return c.RabbitMQ.validate()
		}
	}
	return errors.New("unsupported service type or mismatched options")
}

func (c Config) Check(ctx context.Context) Result { return Check(ctx, c) }
func Check(ctx context.Context, c Config) Result {
	if c.Type != Oracle {
		return CheckInWorker(ctx, c)
	}
	start := time.Now()
	r := process.RunConfig(ctx, c.Timeout, c)
	// Preserve the service API's total-deadline outcome even when startup uses
	// the budget. Admission/setup failures remain pending, with zero latency.
	if r.State == "pending" && r.Message == "service worker deadline before check" {
		return Result{State: "down", Message: "service check timed out", LatencyMs: float64(time.Since(start)) / float64(time.Millisecond)}
	}
	return Result(r)
}

// CheckInWorker is the explicit local engine entry for a resource-boundary
// worker. Oracle callers must use Check, which always creates a limited child.
// Only the early worker dispatcher may call this for Oracle; no context or
// environment marker silently disables isolation.
func CheckInWorker(ctx context.Context, c Config) Result {
	start := time.Now()
	r := Result{State: "down", Message: "invalid service configuration"}
	if c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		r.LatencyMs = float64(time.Since(start)) / float64(time.Millisecond)
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if c.Validate() != nil {
		r.LatencyMs = float64(time.Since(start)) / float64(time.Millisecond)
		return r
	}
	if ctx.Err() == nil {
		switch c.Type {
		case Docker:
			r = checkDocker(ctx, *c.Docker)
		case Redis:
			r = checkRedis(ctx, *c.Redis)
		case Postgres:
			r = checkPostgres(ctx, *c.Postgres)
		case MySQL:
			r = checkMySQL(ctx, *c.MySQL)
		case MQTT:
			r = checkMQTT(ctx, *c.MQTT)
		case SFTP:
			r = checkSFTP(ctx, *c.SFTP)
		case MongoDB:
			r = checkMongoDB(ctx, *c.MongoDB)
		case SQLServer:
			r = checkSQLServer(ctx, *c.SQLServer)
		case NTP:
			r = checkNTP(ctx, *c.NTP)
		case GRPC:
			r = checkGRPC(ctx, *c.GRPC)
		case SNMP:
			r = checkSNMP(ctx, *c.SNMP)
		case RADIUS:
			r = checkRADIUS(ctx, *c.RADIUS)
		case SIP:
			r = checkSIP(ctx, *c.SIP)
		case Kafka:
			r = checkKafka(ctx, *c.Kafka)
		case Oracle:
			r = checkOracle(ctx, *c.Oracle)
		case RabbitMQ:
			r = checkRabbitMQ(ctx, *c.RabbitMQ)
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) && r.State != "pending" {
		r = down("service check canceled")
	} else if ctx.Err() != nil && r.State != "pending" {
		r = down("service check timed out")
	}
	r.LatencyMs = float64(time.Since(start)) / float64(time.Millisecond)
	return r
}
func down(message string) Result { return Result{State: "down", Message: message} }
func up(message string) Result   { return Result{State: "up", Message: message} }

func tlsSettings(c *TLSConfig, host string) (*tls.Config, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if c == nil {
		return t, nil
	}
	if len(c.ServerName) > 253 || strings.ContainsAny(c.ServerName, "\x00\r\n/") {
		return nil, errors.New("invalid TLS server name")
	}
	if c.ServerName != "" {
		t.ServerName = c.ServerName
	}
	t.InsecureSkipVerify = c.IgnoreVerification // explicit unsafe operator opt-in
	if len(c.CAPEM) > maxPEM || len(c.ClientCertPEM) > maxPEM || len(c.ClientKeyPEM) > maxPEM {
		return nil, errors.New("TLS PEM exceeds limit")
	}
	if c.CAPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(c.CAPEM)) {
			return nil, errors.New("invalid TLS CA PEM")
		}
		t.RootCAs = pool
	}
	if (c.ClientCertPEM == "") != (c.ClientKeyPEM == "") {
		return nil, errors.New("TLS client certificate and key must be paired")
	}
	if c.ClientCertPEM != "" {
		cert, err := tls.X509KeyPair([]byte(c.ClientCertPEM), []byte(c.ClientKeyPEM))
		if err != nil {
			return nil, errors.New("invalid TLS client certificate or key")
		}
		t.Certificates = []tls.Certificate{cert}
	}
	return t, nil
}

func validAddress(s string) error {
	if len(s) > 512 || strings.ContainsAny(s, "\x00\r\n /?#@") {
		return errors.New("invalid service address")
	}
	h, p, e := net.SplitHostPort(s)
	n, err := strconv.Atoi(p)
	if e != nil || h == "" || err != nil || n < 1 || n > 65535 {
		return errors.New("service address must be host:port")
	}
	return nil
}
func validMode(mode string, t *TLSConfig) error {
	if mode != "" && mode != "tls" && mode != "plain" {
		return errors.New("TLS mode must be tls or plain")
	}
	if mode == "plain" && t != nil {
		return errors.New("TLS options require TLS mode")
	}
	return nil
}

// guardedConn enforces a total wire budget as well as the total deadline. Its
// AfterFunc closes blocked protocol handshakes, not only ordinary socket reads.
type guardedConn struct {
	net.Conn
	mu        sync.Mutex
	remaining int
	stop      func() bool
}

func (c *guardedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining <= 0 {
		return 0, errors.New("service wire limit exceeded")
	}
	if len(p) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= n
	return n, err
}
func (c *guardedConn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.Conn.Close()
}
func dial(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if d, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(d)
	}
	g := &guardedConn{Conn: c, remaining: 4 * maxBody}
	g.stop = context.AfterFunc(ctx, func() { _ = c.Close() })
	return g, nil
}
func dialService(ctx context.Context, address, mode string, t *TLSConfig) (net.Conn, error) {
	c, err := dial(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if mode == "plain" {
		return c, nil
	}
	h, _, _ := net.SplitHostPort(address)
	cfg, err := tlsSettings(t, h)
	if err != nil {
		c.Close()
		return nil, err
	}
	tc := tls.Client(c, cfg)
	if err = tc.HandshakeContext(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return tc, nil
}
func boundedRead(r io.Reader, limit int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("response limit exceeded")
	}
	return b, nil
}
func decodeOne(b []byte, v any) error { return json.Unmarshal(b, v) }
