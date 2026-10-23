package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

type RedisConfig struct {
	Address  string     `json:"address"`
	Username string     `json:"username,omitempty"`
	Password string     `json:"password,omitempty"`
	Database int        `json:"database,omitempty"`
	TLSMode  string     `json:"tlsMode,omitempty"`
	TLS      *TLSConfig `json:"tls,omitempty"`
}

func (c RedisConfig) validate() error {
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if len(c.Username) > 1024 || len(c.Password) > 4096 || (c.Username != "" && c.Password == "") {
		return errors.New("invalid Redis credentials")
	}
	if c.Database < 0 || c.Database > 1<<20 {
		return errors.New("invalid Redis database")
	}
	if err := validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	h, _, _ := net.SplitHostPort(c.Address)
	_, err := tlsSettings(c.TLS, h)
	return err
}
func checkRedis(ctx context.Context, c RedisConfig) Result {
	conn, err := dialService(ctx, c.Address, c.TLSMode, c.TLS)
	if err != nil {
		return down("Redis connection or TLS failed")
	}
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 4096)
	command := func(expected string, args ...string) bool {
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(args))
		for _, v := range args {
			fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(v), v)
		}
		if _, err := io.WriteString(conn, b.String()); err != nil {
			return false
		}
		line, err := r.ReadSlice('\n')
		return err == nil && string(line) == "+"+expected+"\r\n"
	}
	if c.Password != "" {
		args := []string{"AUTH"}
		if c.Username != "" {
			args = append(args, c.Username)
		}
		args = append(args, c.Password)
		if !command("OK", args...) {
			return down("Redis authentication failed or malformed reply")
		}
	}
	if c.Database != 0 && !command("OK", "SELECT", strconv.Itoa(c.Database)) {
		return down("Redis database selection failed")
	}
	if !command("PONG", "PING") {
		return down("Redis PING failed or malformed reply")
	}
	return up("Redis PING succeeded")
}
