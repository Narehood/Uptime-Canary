# Tailscale peer checks

The `tailscale-ping` monitor asks the Tailscale client on each selected node to
ping one peer. The target is a literal IP address or an ASCII DNS/MagicDNS name,
without a URL, port, flags or shell syntax. It is a Tailscale discovery ping;
a reply through DERP or a peer relay counts as a reply. It does not require a
direct connection and does not fall back to ordinary ICMP.

The node must already have an installed client and an authorized, running
daemon with access to the intended tailnet. Canary never installs, starts,
signs in to or configures Tailscale. Linux uses `/usr/bin/tailscale` or
`/usr/local/bin/tailscale`. Windows uses `Tailscale/tailscale.exe` below the
system Program Files known folder. Other operating systems report Pending.
The base Alpine image contains neither the client nor the daemon; container
access to a host daemon needs deliberate deployment configuration and is not
provided by publishing a port or joining the container network.

A validated peer reply is UP and supplies the client-reported latency. A
confirmed ping timeout is DOWN. Missing clients, unavailable or unauthorized
daemons, unmatched peers, a local-self target, unexpected output, resource
exhaustion, cancellation and overall deadlines are Pending. Pending is not
inverted and does not prove recovery from an open outage. Raw client output,
peer names, routes and daemon diagnostics do not enter observations or alerts.

The disposable worker runs only the fixed `ping` operation, with one attempt,
`--until-direct=false` and a bounded deadline. It captures at most 4 KiB from
each output stream, kills and reaps interrupted clients, and uses the existing
two-slot worker admission and platform controls. The monitor cannot select an
executable, socket, environment, daemon command or shell.

The output profile was checked against Tailscale's
[pinned CLI source](https://github.com/tailscale/tailscale/blob/566f89e0fb2aa4b2f1ebf98761e45e62aa5ef225/cmd/tailscale/cli/ping.go).
An unrecognized future profile stays Pending. The comparison implementation is
Uptime Kuma 2.5.6's
[Tailscale monitor](https://github.com/louislam/uptime-kuma/blob/7ca4a3debd76443d8452da95d4040200a1479af4/server/monitor-types/tailscale-ping.js).

## Development evidence

This completion branch is in development. The first private-client test exposed
an output-limit bypass caused by an embedded Go buffer; composition repaired
it with the original flood regression retained. A Windows component run passed
on source `e7f2876425419fcceee8157e4d0d44ad6fcc0ff8306c2f3118337b2b3e7802c9`.
Later worker, node, peer and dashboard integration changes are outside that
run. Actual Tailscale interoperability, native Linux/Windows execution and full
release acceptance require their own evidence. No user's daemon or tailnet was
queried by those private fixtures.
