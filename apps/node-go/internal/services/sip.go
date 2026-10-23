package services

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SIPConfig struct {
	Address        string     `json:"address"`
	Transport      string     `json:"transport"`
	RequestURI     string     `json:"requestUri,omitempty"`
	ExpectedStatus []int      `json:"expectedStatus,omitempty"`
	TLS            *TLSConfig `json:"tls,omitempty"`
}

var sipURI = regexp.MustCompile(`^sips?:(?:[A-Za-z0-9_.!~*'()%+\-]+@)?(?:[A-Za-z0-9_.\-]+|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?$`)

func (c SIPConfig) uri() string {
	if c.RequestURI != "" {
		return c.RequestURI
	}
	scheme := "sip:"
	if c.Transport == "tls" {
		scheme = "sips:"
	}
	return scheme + c.Address
}
func (c SIPConfig) validate() error {
	if e := validAddress(c.Address); e != nil {
		return e
	}
	if c.Transport != "udp" && c.Transport != "tcp" && c.Transport != "tls" {
		return errors.New("SIP transport must be udp, tcp or tls")
	}
	if len(c.uri()) > 1024 || !sipURI.MatchString(c.uri()) {
		return errors.New("invalid SIP request URI")
	}
	if strings.HasPrefix(c.uri(), "sips:") && c.Transport != "tls" {
		return errors.New("SIPS URI requires TLS transport")
	}
	if len(c.ExpectedStatus) > 16 {
		return errors.New("SIP expected status count exceeds limit")
	}
	seen := map[int]bool{}
	for _, s := range c.ExpectedStatus {
		if s < 200 || s > 699 || seen[s] {
			return errors.New("invalid SIP expected status")
		}
		seen[s] = true
	}
	if c.Transport != "tls" && c.TLS != nil {
		return errors.New("SIP TLS options require TLS transport")
	}
	h, _, _ := net.SplitHostPort(c.Address)
	_, e := tlsSettings(c.TLS, h)
	return e
}

type sipTransaction struct {
	uri, via, branch, callID, fromTag, from string
	cseq                                    uint32
}

func newSIPTransaction(c SIPConfig, local string) (sipTransaction, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return sipTransaction{}, e
	}
	seq, e := snmpID()
	if e != nil {
		return sipTransaction{}, e
	}
	t := sipTransaction{uri: c.uri(), branch: "z9hG4bK" + hex.EncodeToString(b[:12]), callID: hex.EncodeToString(b[12:24]) + "@canary", fromTag: hex.EncodeToString(b[24:]), cseq: seq}
	t.via = "SIP/2.0/" + strings.ToUpper(c.Transport) + " " + local
	t.from = "<sip:canary@localhost>;tag=" + t.fromTag
	return t, nil
}
func (t sipTransaction) request() []byte {
	return []byte(fmt.Sprintf("OPTIONS %s SIP/2.0\r\nVia: %s;branch=%s;rport\r\nMax-Forwards: 0\r\nFrom: %s\r\nTo: <%s>\r\nCall-ID: %s\r\nCSeq: %d OPTIONS\r\nAccept: application/sdp\r\nUser-Agent: Uptime-Canary\r\nContent-Length: 0\r\n\r\n", t.uri, t.via, t.branch, t.from, t.uri, t.callID, t.cseq))
}

type sipResponse struct {
	status  int
	headers map[string][]string
}

func sipHeaderName(s string) string {
	s = strings.ToLower(s)
	switch s {
	case "v":
		return "via"
	case "f":
		return "from"
	case "t":
		return "to"
	case "i":
		return "call-id"
	case "l":
		return "content-length"
	}
	return s
}
func sipPrintable(s string) bool {
	for _, b := range []byte(s) {
		if b != '\t' && (b < 32 || b > 126) {
			return false
		}
	}
	return true
}

// ReadSlice bounds each line before allocation. Header folding is deliberately
// unsupported rather than accepting ambiguous framing or duplicate identities.
func readSIPResponse(r *bufio.Reader, datagram bool) (sipResponse, error) {
	bad := errors.New("invalid SIP response")
	var response sipResponse
	response.headers = make(map[string][]string)
	budget := 16 << 10
	line := func() (string, error) {
		b, e := r.ReadSlice('\n')
		budget -= len(b)
		if e != nil || budget < 0 || len(b) < 2 || !bytes.HasSuffix(b, []byte("\r\n")) {
			return "", bad
		}
		s := string(b[:len(b)-2])
		if !sipPrintable(s) {
			return "", bad
		}
		return s, nil
	}
	status, e := line()
	if e != nil || len(status) < 12 || !strings.HasPrefix(status, "SIP/2.0 ") || status[11] != ' ' {
		return response, bad
	}
	for _, b := range []byte(status[8:11]) {
		if b < '0' || b > '9' {
			return response, bad
		}
	}
	response.status, e = strconv.Atoi(status[8:11])
	if e != nil || response.status < 100 || response.status > 699 {
		return response, bad
	}
	for i := 0; ; i++ {
		s, e := line()
		if e != nil {
			return response, bad
		}
		if s == "" {
			break
		}
		if i >= 64 || s[0] == ' ' || s[0] == '\t' {
			return response, bad
		}
		name, value, ok := strings.Cut(s, ":")
		if !ok || name == "" {
			return response, bad
		}
		for _, b := range []byte(name) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-') {
				return response, bad
			}
		}
		name = sipHeaderName(name)
		value = strings.TrimSpace(value)
		if len(response.headers[name]) > 0 && name != "via" {
			return response, bad
		}
		if len(response.headers[name]) >= 8 {
			return response, bad
		}
		response.headers[name] = append(response.headers[name], value)
	}
	length := 0
	if values := response.headers["content-length"]; len(values) == 1 {
		s := values[0]
		if s == "" || len(s) > 5 {
			return response, bad
		}
		for _, b := range []byte(s) {
			if b < '0' || b > '9' {
				return response, bad
			}
		}
		length, e = strconv.Atoi(s)
		if e != nil || length > 8192 {
			return response, bad
		}
	} else if !datagram {
		return response, bad
	} else {
		_, e = boundedRead(r, 8192)
		return response, e
	}
	if _, e = io.CopyN(io.Discard, r, int64(length)); e != nil {
		return response, bad
	}
	if datagram {
		if _, e = r.ReadByte(); e != io.EOF {
			return response, bad
		}
	}
	return response, nil
}

func sipParameter(s, name string) (string, bool) {
	parts := strings.Split(s, ";")
	found := ""
	ok := false
	for _, p := range parts[1:] {
		k, v, has := strings.Cut(strings.TrimSpace(p), "=")
		if strings.EqualFold(k, name) {
			if ok || !has || v == "" || strings.ContainsAny(v, " \t\r\n\"") {
				return "", false
			}
			found = v
			ok = true
		}
	}
	return found, ok
}
func sipIdentity(s, uri string) bool {
	start := strings.IndexByte(s, '<')
	end := strings.IndexByte(s, '>')
	return start >= 0 && end > start && s[start+1:end] == uri
}
func (t sipTransaction) matches(r sipResponse) bool {
	one := func(name string) string {
		v := r.headers[name]
		if len(v) != 1 {
			return ""
		}
		return v[0]
	}
	if one("call-id") != t.callID || one("cseq") != strconv.FormatUint(uint64(t.cseq), 10)+" OPTIONS" {
		return false
	}
	from := one("from")
	tag, ok := sipParameter(from, "tag")
	if !ok || tag != t.fromTag || !sipIdentity(from, "sip:canary@localhost") || !sipIdentity(one("to"), t.uri) {
		return false
	}
	if r.status > 100 {
		if _, ok := sipParameter(one("to"), "tag"); !ok {
			return false
		}
	}
	via := r.headers["via"]
	if len(via) == 0 {
		return false
	}
	top, _, _ := strings.Cut(via[0], ",")
	branch, ok := sipParameter(top, "branch")
	sentBy, _, _ := strings.Cut(top, ";")
	return ok && branch == t.branch && strings.EqualFold(strings.TrimSpace(sentBy), t.via)
}

func checkSIP(ctx context.Context, c SIPConfig) Result {
	var conn net.Conn
	var e error
	if c.Transport == "udp" {
		conn, e = dial(ctx, "udp", c.Address)
	} else {
		mode := "plain"
		if c.Transport == "tls" {
			mode = "tls"
		}
		conn, e = dialService(ctx, c.Address, mode, c.TLS)
	}
	if e != nil {
		return down("SIP connection failed")
	}
	defer conn.Close()
	t, e := newSIPTransaction(c, conn.LocalAddr().String())
	if e != nil {
		return down("SIP transaction creation failed")
	}
	request := t.request()
	write := func() bool { n, e := conn.Write(request); return e == nil && n == len(request) }
	if !write() {
		return down("SIP OPTIONS request failed")
	}
	stream := bufio.NewReaderSize(conn, 4096)
	interval := 500 * time.Millisecond
	deadline, _ := ctx.Deadline()
	responses, sends := 0, 1
	for responses < 16 && sends <= 80 {
		var response sipResponse
		if c.Transport == "udp" {
			next := time.Now().Add(interval)
			if deadline.Before(next) {
				next = deadline
			}
			_ = conn.SetReadDeadline(next)
			var p [24<<10 + 1]byte
			n, err := conn.Read(p[:])
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() && ctx.Err() == nil && time.Now().Before(deadline) {
					if sends >= 80 {
						return down("SIP retransmission limit exceeded")
					}
					if !write() {
						return down("SIP OPTIONS retransmission failed")
					}
					sends++
					interval *= 2
					if interval > 4*time.Second {
						interval = 4 * time.Second
					}
					continue
				}
				return down("SIP response failed")
			}
			if n > 24<<10 {
				return down("SIP response exceeds limit")
			}
			response, e = readSIPResponse(bufio.NewReaderSize(bytes.NewReader(p[:n]), 4096), true)
		} else {
			response, e = readSIPResponse(stream, false)
		}
		responses++
		if e != nil || !t.matches(response) {
			return down("SIP response does not match OPTIONS transaction")
		}
		if response.status < 200 {
			interval = 4 * time.Second
			continue
		}
		accepted := response.status >= 200 && response.status <= 299
		if len(c.ExpectedStatus) > 0 {
			accepted = false
			for _, status := range c.ExpectedStatus {
				if response.status == status {
					accepted = true
				}
			}
		}
		if accepted {
			return up("SIP OPTIONS received expected final response")
		}
		return down("SIP OPTIONS received unexpected final status")
	}
	return down("SIP transaction response limit exceeded")
}
