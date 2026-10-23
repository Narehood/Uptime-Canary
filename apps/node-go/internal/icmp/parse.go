package icmp

import (
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
)

var (
	unixReply = regexp.MustCompile(`^\s*\d+ bytes from (\S+): (?:icmp_seq|icmp6_seq|seq)[= ]\d+\b`)
	unixTime  = regexp.MustCompile(`\btime([=<])\s*([0-9]+(?:\.[0-9]+)?)\s*ms\b`)
	unixRTT   = regexp.MustCompile(`^(?:rtt|round-trip) min/avg/max(?:/(?:mdev|stddev))? = ([0-9]+(?:\.[0-9]+)?)/([0-9]+(?:\.[0-9]+)?)/([0-9]+(?:\.[0-9]+)?)(?:/[0-9]+(?:\.[0-9]+)?)? ms\s*$`)
	unixLoss  = regexp.MustCompile(`^([0-9]+) packets transmitted, ([0-9]+)(?: packets)? received,`)
	windowsIP = regexp.MustCompile(`(?:^|\s)((?:[0-9]{1,3}\.){3}[0-9]{1,3}|[0-9a-fA-F]*:[0-9a-fA-F:]+)(?:\s+[^:=<>\r\n]*)?[:：]\s`)
	windowsMS = regexp.MustCompile(`([=<])\s*([0-9]+(?:[.,][0-9]+)?)\s*ms\b`)
)

type parsed struct {
	replies bool
	noReply bool
	latency bool
	avg     float64
}

func number(s string) (float64, bool) {
	n, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 120000
}

func parseOutput(output, platform string) parsed {
	var p parsed
	var sum float64
	samples := 0
	summary := false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if platform == "windows" {
			times := windowsMS.FindAllStringSubmatch(line, 4)
			if ip := windowsIP.FindStringSubmatch(line); ip != nil && net.ParseIP(ip[1]) != nil && len(times) == 1 {
				p.replies = true
				if n, ok := number(times[0][2]); ok && times[0][1] == "=" {
					sum += n
					samples++
				}
			} else if len(times) == 3 {
				// Localized Windows summary: minimum, maximum, average in that
				// order. Do not depend on translated labels or OEM encoding.
				lo, a := number(times[0][2])
				hi, b := number(times[1][2])
				avg, c := number(times[2][2])
				if a && b && c && lo <= avg && avg <= hi && times[0][1] == "=" && times[1][1] == "=" && times[2][1] == "=" {
					p.avg, summary = avg, true
				}
			}
			lower := strings.ToLower(line)
			if lower == "request timed out." || strings.Contains(lower, "destination host unreachable") || strings.Contains(lower, "destination net unreachable") {
				p.noReply = true
			}
			continue
		}
		if match := unixReply.FindStringSubmatch(line); match != nil && net.ParseIP(match[1]) != nil {
			p.replies = true
			if m := unixTime.FindStringSubmatch(line); m != nil && m[1] == "=" {
				if n, ok := number(m[2]); ok {
					sum += n
					samples++
				}
			}
		}
		if m := unixLoss.FindStringSubmatch(line); m != nil {
			sent, e1 := strconv.Atoi(m[1])
			received, e2 := strconv.Atoi(m[2])
			if e1 == nil && e2 == nil && sent > 0 && received <= sent {
				p.replies = p.replies || received > 0
				p.noReply = received == 0
			}
		}
		if m := unixRTT.FindStringSubmatch(line); m != nil {
			lo, a := number(m[1])
			avg, b := number(m[2])
			hi, c := number(m[3])
			if a && b && c && lo <= avg && avg <= hi {
				p.avg, summary = avg, true
			}
		}
	}
	if p.replies {
		p.latency = summary || samples > 0
		if !summary && samples > 0 {
			p.avg = sum / float64(samples)
		}
	} else {
		p.avg = 0
	}
	return p
}

func localFailure(stdout, stderr, platform string) bool {
	// Inspect diagnostics, never return them. Permission, send and resource
	// failures may use the same exit status as packet loss (notably BusyBox).
	lower := strings.ToLower(stderr)
	for _, marker := range []string{
		"permission denied", "operation not permitted", "not permitted", "must be root",
		"not allowed", "can't create", "cannot create", "can't open", "cannot open",
		"no buffer space", "cannot allocate", "out of memory", "resource temporarily unavailable",
		"too many open files", "bad address", "name or service not known", "unknown host",
		"temporary failure", "cannot resolve", "invalid", "usage:", "unrecognized option",
		"illegal option", "socket:", "socket ", "sendto:", "sendmsg:", "network is unreachable",
		"address family", "message too long", "cap_net_raw", "write error", "minimal interval",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if platform == "windows" {
		lower = strings.ToLower(stdout)
		return strings.Contains(lower, "general failure") || strings.Contains(lower, "transmit failed") || strings.Contains(lower, "could not find host")
	}
	return false
}
