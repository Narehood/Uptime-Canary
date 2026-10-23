package services

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
)

func sqlServerConfig(c SQLConfig) (msdsn.Config, error) {
	bad := errors.New("SQL Server DSN requires sqlserver://user:password@host:port/database without options or integrated authentication")
	if len(c.DSN) == 0 || len(c.DSN) > 16384 {
		return msdsn.Config{}, bad
	}
	u, err := url.Parse(c.DSN)
	if err != nil || u.Scheme != "sqlserver" || u.Opaque != "" || u.Hostname() == "" || u.User == nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return msdsn.Config{}, bad
	}
	port := u.Port()
	if port == "" {
		port = "1433"
	}
	if validAddress(net.JoinHostPort(u.Hostname(), port)) != nil || strings.ContainsAny(u.Hostname(), ",\\") {
		return msdsn.Config{}, bad
	}
	user := u.User.Username()
	password, _ := u.User.Password()
	database := strings.TrimPrefix(u.Path, "/")
	if user == "" || len(user) > 128 || strings.ContainsAny(user, "\\\x00\r\n") || len(password) > 4096 || strings.ContainsRune(password, 0) || database == "" || len(database) > 128 || strings.ContainsAny(database, "/\\\x00\r\n") {
		return msdsn.Config{}, bad
	}
	n, _ := strconv.ParseUint(port, 10, 16)
	// Construct directly: msdsn.Parse permits files, session parameters, password
	// changes, integrated/federated auth, failover and instance discovery.
	p := msdsn.Config{Host: u.Hostname(), Port: n, User: user, Password: password, Database: database, Encryption: msdsn.EncryptionRequired, PacketSize: 4096, Protocols: []string{"tcp"}, AppName: "uptime-canary", Workstation: "uptime-canary", DisableRetry: true, DialTimeout: -1, ReadOnlyIntent: true}
	if err = validMode(c.TLSMode, c.TLS); err != nil {
		return msdsn.Config{}, err
	}
	if c.TLSMode == "plain" {
		p.Encryption = msdsn.EncryptionDisabled
	} else {
		p.TLSConfig, err = tlsSettings(c.TLS, p.Host)
	}
	return p, err
}

func validateSQLServer(c SQLConfig) error {
	if _, err := sqlServerConfig(c); err != nil {
		return err
	}
	if err := validateQuery(c.Query); err != nil {
		return err
	}
	if c.Query != "" && strings.ToUpper(strings.Fields(strings.TrimSpace(c.Query))[0]) != "SELECT" {
		return errors.New("SQL Server query must start with SELECT")
	}
	if c.Condition != nil {
		return c.Condition.validate()
	}
	return nil
}

type sqlServerDialer struct {
	ctx           context.Context
	host, address string
	attempts      atomic.Int32
}

func (d *sqlServerDialer) HostName() string { return d.host }
func (d *sqlServerDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	// HostDialer prevents the driver's separate DNS resolution. Reject SQL
	// Browser, routing, alternate ports and recursive login redirects.
	if network != "tcp" || address != d.address || d.attempts.Add(1) > 1 {
		return nil, errors.New("SQL Server endpoint or connection limit")
	}
	return dial(d.ctx, "tcp", d.address)
}

func checkSQLServer(ctx context.Context, c SQLConfig) (result Result) {
	// The upstream parser has goroutine-based token readers with its own error
	// conversion. This contains synchronous malformed-configuration/parser panics.
	defer func() {
		if recover() != nil {
			result = down("SQL Server driver rejected a malformed reply")
		}
	}()
	p, err := sqlServerConfig(c)
	if err != nil {
		return down("invalid SQL Server configuration")
	}
	connector := mssql.NewConnectorConfig(p)
	connector.Dialer = &sqlServerDialer{ctx: ctx, host: p.Host, address: net.JoinHostPort(p.Host, strconv.FormatUint(p.Port, 10))}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	rows, err := db.QueryContext(ctx, sqlQuery(c))
	if err != nil {
		return down("SQL Server connection, TLS, authentication, or query failed")
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || len(cols) == 0 || len(cols) > 64 {
		return down("SQL Server result columns invalid or exceed limit")
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
			return down("SQL Server query result exceeds limit")
		}
		if rows.Scan(dest...) != nil {
			return down("SQL Server query result decoding failed")
		}
		for _, v := range values {
			total += len(v)
			if total > maxBody {
				return down("SQL Server query result exceeds limit")
			}
		}
		if n == 1 {
			isNull = values[0] == nil
			first = append([]byte(nil), values[0]...)
		}
	}
	if rows.Err() != nil {
		return down("SQL Server query failed or malformed reply")
	}
	if rows.NextResultSet() {
		return down("SQL Server returned multiple result sets")
	}
	if rows.Err() != nil {
		return down("SQL Server query failed or malformed reply")
	}
	if n == 0 {
		return down("SQL Server query returned no rows")
	}
	if c.Condition != nil && !c.Condition.matches(first, isNull) {
		return down("SQL Server scalar condition did not match")
	}
	return up("SQL Server monitoring query succeeded")
}
