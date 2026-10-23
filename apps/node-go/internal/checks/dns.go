package checks

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
)

type resolverConn struct {
	net.Conn
	stop func() bool
}

func (c *resolverConn) Close() error { c.stop(); return c.Conn.Close() }

type resolverPacketConn struct {
	*resolverConn
	packet net.PacketConn
}

func (c *resolverPacketConn) ReadFrom(p []byte) (int, net.Addr, error) { return c.packet.ReadFrom(p) }
func (c *resolverPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	return c.packet.WriteTo(p, a)
}

func canonicalRecord(recordType, s string) (string, bool) {
	switch recordType {
	case "A", "AAAA":
		ip := net.ParseIP(s)
		if ip == nil {
			return "", false
		}
		if recordType == "A" && ip.To4() == nil || recordType == "AAAA" && ip.To4() != nil {
			return "", false
		}
		return ip.String(), true
	case "MX":
		parts := strings.Fields(s)
		if len(parts) != 2 || !validDNSName(parts[1]) {
			return "", false
		}
		priority, err := strconv.ParseUint(parts[0], 10, 16)
		if err != nil {
			return "", false
		}
		return strconv.FormatUint(priority, 10) + " " + strings.ToLower(strings.TrimSuffix(parts[1], ".")), true
	case "TXT":
		return s, true
	default:
		if !validDNSName(s) {
			return "", false
		}
		return strings.ToLower(strings.TrimSuffix(s, ".")), true
	}
}

func checkDNS(ctx context.Context, c Config) Result {
	o := dnsOptions(c)
	rt := dnsType(o)
	resolver := &net.Resolver{PreferGo: true, StrictErrors: true}
	var queried atomic.Bool
	resolver.Dial = func(dialCtx context.Context, network, server string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if o.Server != "" {
			server = o.Server
		}
		conn, err := (&net.Dialer{}).DialContext(dialCtx, network, server)
		if err != nil {
			return nil, err
		}
		queried.Store(true)
		wrapped := &resolverConn{Conn: conn, stop: context.AfterFunc(ctx, func() { _ = conn.Close() })}
		// Preserve PacketConn: net.Resolver uses that interface to distinguish
		// UDP datagrams from length-prefixed DNS over TCP.
		if packet, ok := conn.(net.PacketConn); ok {
			return &resolverPacketConn{resolverConn: wrapped, packet: packet}, nil
		}
		return wrapped, nil
	}
	var records []string
	var err error
	switch rt {
	case "A", "AAAA":
		network := "ip4"
		if rt == "AAAA" {
			network = "ip6"
		}
		var addresses []net.IP
		addresses, err = resolver.LookupIP(ctx, network, c.Target)
		for _, ip := range addresses {
			records = append(records, ip.String())
		}
	case "CNAME":
		var name string
		name, err = resolver.LookupCNAME(ctx, c.Target)
		if err == nil && name != "" {
			records = []string{name}
		}
	case "MX":
		var mx []*net.MX
		mx, err = resolver.LookupMX(ctx, c.Target)
		for _, m := range mx {
			records = append(records, fmt.Sprintf("%d %s", m.Pref, m.Host))
		}
	case "TXT":
		records, err = resolver.LookupTXT(ctx, c.Target)
	case "NS":
		var ns []*net.NS
		ns, err = resolver.LookupNS(ctx, c.Target)
		for _, n := range ns {
			records = append(records, n.Host)
		}
	case "PTR":
		records, err = resolver.LookupAddr(ctx, c.Target)
	}
	if err != nil {
		return down("DNS lookup failed")
	}
	// net.Resolver can answer IP literals and hosts-file entries without
	// contacting DNS, even when Dial is customized. Such a result does not
	// demonstrate that a monitored DNS server is available.
	if !queried.Load() {
		return down("DNS lookup used local data without querying a server")
	}
	if len(records) == 0 {
		return down("DNS lookup returned no records")
	}
	if len(records) > 1024 {
		return down("DNS record count exceeds limit")
	}
	total := 0
	actual := map[string]bool{}
	for _, value := range records {
		total += len(value)
		if total > maxDNSBytes {
			return down("DNS answer data exceeds size limit")
		}
		normalized, ok := canonicalRecord(rt, value)
		if !ok {
			return down("DNS response record is invalid")
		}
		actual[normalized] = true
	}
	for _, expected := range o.Expected {
		normalized, _ := canonicalRecord(rt, expected)
		if !actual[normalized] {
			return down("DNS expected record not found")
		}
	}
	return up(fmt.Sprintf("DNS %s lookup passed (%d records)", rt, len(records)))
}
