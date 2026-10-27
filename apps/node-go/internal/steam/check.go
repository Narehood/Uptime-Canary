package steam

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/icmp"
)

const endpoint = "/IGameServersService/GetServerList/v1/"

var errMeasurementDeadline = errors.New("Steam measurement deadline")

// WithMeasurementTimeout marks the scheduled measurement's own deadline.
// Earlier caller deadlines and explicit cancellation retain their own cause.
func WithMeasurementTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, timeout, errMeasurementDeadline)
}

type dependencies struct {
	lookup func(context.Context, string) ([]net.IPAddr, error)
	ping   func(context.Context, icmp.Config) icmp.Result
}

func Check(ctx context.Context, c Config) Result {
	return check(ctx, c, dependencies{net.DefaultResolver.LookupIPAddr, icmp.Check})
}

func check(ctx context.Context, c Config, deps dependencies) Result {
	if c.Options.PacketSize != nil {
		size := *c.Options.PacketSize
		c.Options.PacketSize = &size
	}
	p, err := prepare(c)
	if err != nil {
		return Result{State: "down", Message: "Invalid Steam configuration"}
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	unavailable := func() Result {
		// The monitor's own API deadline is a provider failure governed by
		// the configured policy. Caller cancellation/expiry is no observation.
		if c.Options.APIFailureIsDown && (parent.Err() == nil || context.Cause(parent) == errMeasurementDeadline) {
			return Result{State: "down", Message: "Steam directory API unavailable"}
		}
		return Result{State: "pending", Message: "Steam directory API unavailable"}
	}
	if ctx.Err() != nil {
		return unavailable()
	}
	address, err := resolve(ctx, p.host, deps.lookup)
	if err != nil {
		if ctx.Err() != nil {
			return unavailable()
		}
		return Result{State: "down", Message: "Steam server hostname could not be resolved"}
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, certificate := range p.certs {
		pool.AddCert(certificate)
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 2,
		MaxResponseHeaderBytes: 32 << 10, ResponseHeaderTimeout: c.Timeout, TLSHandshakeTimeout: c.Timeout,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		DialContext:     (&net.Dialer{Timeout: c.Timeout}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u := *p.base
	u.Path = strings.TrimSuffix(u.Path, "/") + endpoint
	// Match the pinned Steam monitor's addr filter, including IPv4-first DNS.
	u.RawQuery = url.Values{"key": {c.Options.APIKey}, "filter": {"addr\\" + address.String() + ":" + p.port}, "limit": {"1"}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return unavailable()
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Uptime-Canary")
	response, err := client.Do(request)
	if err != nil {
		return unavailable() // Transport errors can contain the key-bearing URL.
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailable()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || ctx.Err() != nil {
		return unavailable()
	}
	listed, valid := directoryResult(raw, address, p.port)
	if !valid || ctx.Err() != nil {
		return unavailable()
	}
	if !listed {
		return Result{State: "down", Message: "Server is not listed in Steam directory"}
	}
	result := Result{State: "up", Message: "Server is listed in Steam directory"}
	if !c.Options.SkipLatency {
		deadline, _ := ctx.Deadline()
		budget := min(time.Until(deadline)/4, time.Second)
		if budget > 0 {
			measurementCtx, stop := context.WithTimeout(ctx, budget)
			measured := deps.ping(measurementCtx, icmp.Config{Target: address.String(), Timeout: budget, Count: 1, Size: c.Options.PacketSize})
			stop()
			if measured.State == icmp.Up && measured.LatencyAvailable {
				result.LatencyMs, result.LatencyAvailable = measured.LatencyMs, true
			}
		}
	}
	if ctx.Err() != nil {
		return unavailable()
	}
	return result
}

func resolve(ctx context.Context, host string, lookup func(context.Context, string) ([]net.IPAddr, error)) (net.IP, error) {
	if address := net.ParseIP(host); address != nil {
		return address, nil
	}
	addresses, err := lookup(ctx, host)
	if err != nil || len(addresses) == 0 || len(addresses) > 128 {
		return nil, errConfig
	}
	var first net.IP
	for _, address := range addresses {
		if address.Zone != "" || address.IP == nil {
			continue
		}
		if first == nil {
			first = address.IP
		}
		if ipv4 := address.IP.To4(); ipv4 != nil {
			return ipv4, nil
		}
	}
	if first == nil {
		return nil, errConfig
	}
	return first, nil
}
