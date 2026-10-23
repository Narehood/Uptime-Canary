package services

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	goora "github.com/sijms/go-ora/v3"
)

func oracleURL(c SQLConfig) (*url.URL, error) {
	bad := errors.New("Oracle DSN requires oracle://user:password@host:port/service without options")
	if len(c.DSN) == 0 || len(c.DSN) > 16384 {
		return nil, bad
	}
	u, err := url.Parse(c.DSN)
	if err != nil || u.Scheme != "oracle" || u.Opaque != "" || u.Hostname() == "" || u.User == nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, bad
	}
	port := u.Port()
	if port == "" {
		port = "1521"
	}
	if validAddress(net.JoinHostPort(u.Hostname(), port)) != nil || strings.ContainsAny(u.Hostname(), ",\\()=") {
		return nil, bad
	}
	password, _ := u.User.Password()
	service := strings.TrimPrefix(u.Path, "/")
	if u.User.Username() == "" || len(u.User.Username()) > 128 || strings.ContainsAny(u.User.Username(), "\x00\r\n") || strings.EqualFold(u.User.Username(), "SYS") || password == "" || len(password) > 4096 || strings.ContainsRune(password, 0) || len(service) == 0 || len(service) > 128 {
		return nil, bad
	}
	for _, ch := range service {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-') {
			return nil, bad
		}
	}
	u.Host = net.JoinHostPort(u.Hostname(), port)
	return u, nil
}

func validateOracle(c SQLConfig) error {
	u, err := oracleURL(c)
	if err != nil {
		return err
	}
	if err = validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	if c.TLS != nil {
		if c.TLS.IgnoreVerification {
			return errors.New("Oracle requires verified TLS")
		}
		// v3.0.1 unconditionally overwrites tls.Config.ServerName with DSN host.
		if c.TLS.ServerName != "" && c.TLS.ServerName != u.Hostname() {
			return errors.New("Oracle TLS server name must match DSN hostname")
		}
	}
	if c.TLSMode != "plain" {
		if _, err = tlsSettings(c.TLS, u.Hostname()); err != nil {
			return err
		}
	}
	if err = validateQuery(c.Query); err != nil {
		return err
	}
	if c.Query != "" && strings.ToUpper(strings.Fields(strings.TrimSpace(c.Query))[0]) != "SELECT" {
		return errors.New("Oracle query must start with SELECT")
	}
	if c.Condition != nil {
		return c.Condition.validate()
	}
	return nil
}

type oracleDialer struct {
	ctx      context.Context
	address  string
	attempts atomic.Int32
	conn     net.Conn // accessed synchronously; upstream context watcher is disabled
}

func (d *oracleDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != d.address || d.attempts.Add(1) != 1 {
		return nil, errors.New("Oracle endpoint or connection limit")
	}
	c, err := dial(d.ctx, "tcp", d.address)
	if err != nil {
		return nil, err
	}
	d.conn = &serviceDeadlineConn{Conn: c, ctx: d.ctx}
	return d.conn, nil
}

func checkOracle(ctx context.Context, c SQLConfig) (result Result) {
	defer func() {
		if recover() != nil {
			result = down("Oracle driver rejected a malformed reply")
		}
	}()
	u, err := oracleURL(c)
	if err != nil {
		return down("invalid Oracle configuration")
	}
	// No user-provided URI option reaches the driver. These fixed options disable
	// login-cookie reuse, local files, implicit LOB reads and the timezone query.
	options := url.Values{"FAST LOGIN": {"false"}, "PREFETCH_ROWS": {"25"}, "LOB READ": {"NONE"}, "SERVER LOCATION": {"UTC"}, "PROGRAM": {"uptime-canary"}}
	if c.TLSMode != "plain" {
		options.Set("SSL", "enable")
		options.Set("SSL VERIFY", "enable")
	}
	u.RawQuery = options.Encode()
	connector := goora.NewConnector(u.String()).(*goora.OracleConnector)
	d := &oracleDialer{ctx: ctx, address: u.Host}
	connector.Dialer(d)
	if c.TLSMode != "plain" {
		t, e := tlsSettings(c.TLS, u.Hostname())
		if e != nil {
			return down("invalid Oracle TLS configuration")
		}
		connector.WithTLSConfig(t)
	}
	// The socket guard owns cancellation, including startup and row fetching.
	// Avoid upstream StartContext's concurrent mutation of session connections.
	// One synchronous connection/query also avoids database/sql reconnect retries.
	defer func() {
		if d.conn != nil {
			_ = d.conn.Close()
		}
	}()
	conn, err := connector.Connect(context.Background())
	if err != nil {
		return down("Oracle connection, TLS, or authentication failed")
	}
	defer func() {
		if d.conn != nil {
			_ = d.conn.Close()
		}
		_ = conn.Close()
	}()
	query := c.Query
	if query == "" {
		query = "SELECT 1 FROM DUAL"
	}
	statement := goora.NewStmt(query, conn.(*goora.Connection))
	defer statement.Close()
	// Query (rather than QueryContext) keeps decoding in this goroutine, where
	// malformed-driver panics are contained; socket cancellation still applies.
	rows, err := statement.Query(nil)
	if err != nil {
		return down("Oracle monitoring query failed")
	}
	defer rows.Close()
	columns := rows.Columns()
	if len(columns) == 0 || len(columns) > 64 {
		return down("Oracle result columns invalid or exceed limit")
	}
	values := make([]driver.Value, len(columns))
	n, total := 0, 0
	var first []byte
	isNull := true
	for {
		err = rows.Next(values)
		if err == io.EOF {
			break
		}
		if err != nil {
			return down("Oracle query failed or malformed reply")
		}
		n++
		if n > 100 {
			return down("Oracle query result exceeds limit")
		}
		for i, value := range values {
			b, null, ok := oracleScalar(value)
			if !ok {
				return down("Oracle returned an unsupported scalar type")
			}
			total += len(b)
			if total > maxBody {
				return down("Oracle query result exceeds limit")
			}
			if n == 1 && i == 0 {
				first = append([]byte(nil), b...)
				isNull = null
			}
		}
	}
	if multiple, ok := rows.(driver.RowsNextResultSet); ok && multiple.HasNextResultSet() {
		return down("Oracle returned multiple result sets")
	}
	if n == 0 {
		return down("Oracle query returned no rows")
	}
	if c.Condition != nil && !c.Condition.matches(first, isNull) {
		return down("Oracle scalar condition did not match")
	}
	return up("Oracle monitoring query succeeded")
}

func oracleScalar(v driver.Value) ([]byte, bool, bool) {
	switch v := v.(type) {
	case nil:
		return nil, true, true
	case string:
		return []byte(v), false, true
	case []byte:
		return v, false, true
	case int64:
		return strconv.AppendInt(nil, v, 10), false, true
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, false, false
		}
		return strconv.AppendFloat(nil, v, 'g', -1, 64), false, true
	case bool:
		return strconv.AppendBool(nil, v), false, true
	default:
		return nil, false, false
	}
}
