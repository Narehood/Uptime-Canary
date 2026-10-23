package services

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

type SQLConfig struct {
	DSN       string     `json:"dsn"`
	Query     string     `json:"query,omitempty"`     // defaults to SELECT 1
	Condition *Condition `json:"condition,omitempty"` // first row, first column; remaining rows are still bounded/drained
	TLSMode   string     `json:"tlsMode,omitempty"`
	TLS       *TLSConfig `json:"tls,omitempty"`
}

func (c SQLConfig) validate(t Type) error {
	if len(c.DSN) == 0 || len(c.DSN) > 16384 {
		return errors.New("SQL DSN is missing or exceeds limit")
	}
	if err := validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	if c.Condition != nil {
		if err := c.Condition.validate(); err != nil {
			return err
		}
	}
	if err := validateQuery(c.Query); err != nil {
		return err
	}
	if t == Postgres {
		_, err := postgresConfig(c)
		return err
	}
	_, err := mysqlConfig(c)
	return err
}
func validateQuery(q string) error {
	if q == "" {
		return nil
	}
	if len(q) > 16384 || strings.ContainsRune(q, 0) {
		return errors.New("SQL query is invalid or exceeds limit")
	}
	q = strings.TrimSpace(q)
	q = strings.TrimSuffix(q, ";")
	if strings.TrimSpace(q) == "" {
		return errors.New("SQL query must not be blank")
	}
	if strings.Contains(q, ";") {
		return errors.New("SQL query must be a single monitoring statement")
	}
	word := strings.ToUpper(strings.Fields(q + " ")[0])
	switch word {
	case "SELECT", "WITH", "SHOW", "VALUES", "EXPLAIN":
		return nil
	}
	return errors.New("SQL query must start with SELECT, WITH, SHOW, VALUES, or EXPLAIN")
}
func sqlQuery(c SQLConfig) string {
	if c.Query == "" {
		return "SELECT 1"
	}
	return c.Query
}

func postgresConfig(c SQLConfig) (*pgconn.Config, error) {
	u, err := url.Parse(c.DSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Opaque != "" || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Hostname(), ",/\x00") {
		return nil, errors.New("PostgreSQL DSN must be a single-host URL with explicit user and no query options")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	if validAddress(net.JoinHostPort(u.Hostname(), port)) != nil {
		return nil, errors.New("invalid PostgreSQL address")
	}
	// Override every pgx environment/default credential/file parameter before
	// parsing. Only this constant string reaches pgx's libpq option parser.
	// pgx treats even service='' as a request to read a service file. Refuse
	// PGSERVICE rather than mutating process environment or reading that file.
	if os.Getenv("PGSERVICE") != "" {
		return nil, errors.New("PostgreSQL monitor requires PGSERVICE to be unset")
	}
	p, err := pgconn.ParseConfig(postgresBaseDSN)
	if err != nil {
		return nil, errors.New("PostgreSQL driver configuration failed")
	}
	p.Host = u.Hostname()
	p.User = u.User.Username()
	p.Password, _ = u.User.Password()
	p.Database = strings.TrimPrefix(u.Path, "/")
	// Port was validated above, so this conversion cannot fail.
	n, _ := strconv.Atoi(port)
	p.Port = uint16(n)
	p.Fallbacks = nil
	p.RuntimeParams = map[string]string{"application_name": "uptime-canary", "default_transaction_read_only": "on"}
	p.MaxProtocolMessageBodyLen = maxBody
	if c.TLSMode != "plain" {
		p.TLSConfig, err = tlsSettings(c.TLS, p.Host)
	} else {
		p.TLSConfig = nil
	}
	return p, err
}

const postgresBaseDSN = "host=localhost port=5432 user=canary password=canary dbname=canary passfile='' servicefile='' sslmode=disable sslcert='' sslkey='' sslrootcert='' sslpassword='' sslsni=1 sslnegotiation=postgres target_session_attrs=any connect_timeout=0 application_name=canary timezone='' options='' min_protocol_version=3.0 max_protocol_version=3.0 channel_binding=prefer require_auth=''"

type silentMySQLLogger struct{}

func (silentMySQLLogger) Print(...any) {}
func mysqlConfig(c SQLConfig) (*mysql.Config, error) {
	// DSN options can run SET statements, enable local files, select global TLS
	// registries, or compress unbounded packets. Accept only core DSN fields.
	end := strings.LastIndex(c.DSN, ")/")
	if end < 0 || strings.Contains(c.DSN[end+2:], "?") {
		return nil, errors.New("MySQL DSN requires explicit tcp(host:port)/database without query options")
	}
	p, err := mysql.ParseDSN(c.DSN)
	if err != nil || p.Net != "tcp" || p.User == "" || validAddress(p.Addr) != nil {
		return nil, errors.New("invalid MySQL TCP DSN")
	}
	cfg := mysql.NewConfig()
	cfg.User = p.User
	cfg.Passwd = p.Passwd
	cfg.Net = "tcp"
	cfg.Addr = p.Addr
	cfg.DBName = p.DBName
	cfg.MaxAllowedPacket = maxBody
	cfg.Logger = silentMySQLLogger{}
	cfg.AllowNativePasswords = true
	if c.TLSMode != "plain" {
		h, _, _ := net.SplitHostPort(cfg.Addr)
		cfg.TLS, err = tlsSettings(c.TLS, h)
	} else {
		cfg.TLSConfig = "false"
	}
	return cfg, err
}
func checkPostgres(ctx context.Context, c SQLConfig) Result {
	cfg, err := postgresConfig(c)
	if err != nil {
		return down("invalid PostgreSQL configuration")
	}
	cfg.DialFunc = func(_ context.Context, network, address string) (net.Conn, error) { return dial(ctx, network, address) }
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return down("PostgreSQL connection, TLS, or authentication failed")
	}
	defer conn.Close(ctx)
	m := conn.Exec(ctx, sqlQuery(c))
	rows, bytesRead := 0, 0
	var first []byte
	isNull := true
	results := 0
	for m.NextResult() {
		results++
		if results > 1 {
			return down("PostgreSQL returned multiple result sets")
		}
		r := m.ResultReader()
		for r.NextRow() {
			rows++
			values := r.Values()
			if rows > 100 || len(values) > 64 {
				return down("PostgreSQL query result exceeds limit")
			}
			for _, v := range values {
				bytesRead += len(v)
				if bytesRead > maxBody {
					return down("PostgreSQL query result exceeds limit")
				}
			}
			if rows == 1 && len(values) > 0 {
				isNull = values[0] == nil
				first = append([]byte(nil), values[0]...)
			}
		}
		if _, err := r.Close(); err != nil {
			return down("PostgreSQL query failed or malformed reply")
		}
	}
	if m.Close() != nil {
		return down("PostgreSQL query failed or malformed reply")
	}
	if rows == 0 {
		return down("PostgreSQL query returned no rows")
	}
	if c.Condition != nil && !c.Condition.matches(first, isNull) {
		return down("PostgreSQL scalar condition did not match")
	}
	return up("PostgreSQL monitoring query succeeded")
}
func checkMySQL(ctx context.Context, c SQLConfig) (result Result) {
	// The pinned upstream driver can panic on a truncated ERR packet. Contain
	// malformed-server parser panics at this boundary; never expose panic text.
	defer func() {
		if recover() != nil {
			result = down("MySQL driver rejected a malformed reply")
		}
	}()
	cfg, err := mysqlConfig(c)
	if err != nil {
		return down("invalid MySQL configuration")
	}
	cfg.DialFunc = func(_ context.Context, network, address string) (net.Conn, error) { return dial(ctx, network, address) }
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return down("MySQL driver configuration failed")
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Minute)
	rows, err := db.QueryContext(ctx, sqlQuery(c))
	if err != nil {
		return down("MySQL connection, authentication, or query failed")
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || len(cols) == 0 || len(cols) > 64 {
		return down("MySQL result columns invalid or exceed limit")
	}
	values := make([]sql.RawBytes, len(cols))
	dest := make([]any, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}
	n, total := 0, 0
	var first []byte
	isNull := true
	for rows.Next() {
		n++
		if n > 100 {
			return down("MySQL query result exceeds limit")
		}
		if rows.Scan(dest...) != nil {
			return down("MySQL query result decoding failed")
		}
		for _, v := range values {
			total += len(v)
			if total > maxBody {
				return down("MySQL query result exceeds limit")
			}
		}
		if n == 1 {
			isNull = values[0] == nil
			first = append([]byte(nil), values[0]...)
		}
	}
	if rows.Err() != nil {
		return down("MySQL query failed or malformed reply")
	}
	if rows.NextResultSet() {
		return down("MySQL returned multiple result sets")
	}
	if rows.Err() != nil {
		return down("MySQL query failed or malformed reply")
	}
	if n == 0 {
		return down("MySQL query returned no rows")
	}
	if c.Condition != nil && !c.Condition.matches(first, isNull) {
		return down("MySQL scalar condition did not match")
	}
	return up("MySQL monitoring query succeeded")
}
