package checks

import (
	"errors"
	"net"
	"runtime"
	"syscall"
	"testing"
)

const dnsFixtureBindAttempts = 32

func dnsFixtureSockets(listen func(string, string) (net.Listener, error), packet func(string, string) (net.PacketConn, error)) (net.PacketConn, net.Listener, error) {
	var last error
	for range dnsFixtureBindAttempts {
		// TCP's allocator accounts for its listeners and TIME_WAIT sockets;
		// choosing UDP first says nothing about availability in TCP.
		tcp, err := listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, nil, err
		}
		udp, err := packet("udp", tcp.Addr().String())
		if err == nil {
			return udp, tcp, nil
		}
		tcp.Close()
		// Two protocols cannot be reserved atomically. Retry only a UDP bind
		// collision, keeping both successful sockets until fixture cleanup.
		// Winsock's WSAEADDRINUSE is 10048, unlike syscall.EADDRINUSE there.
		if !errors.Is(err, syscall.EADDRINUSE) && !(runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(10048))) {
			return nil, nil, err
		}
		last = err
	}
	return nil, nil, last
}

func TestDNSFixtureReservesTCPBeforeUDP(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	// UDP's ephemeral allocator can choose a port already occupied in TCP.
	// Force exactly that choice while leaving actual socket binding intact.
	packet := func(network, address string) (net.PacketConn, error) {
		if address == "127.0.0.1:0" {
			address = occupied.Addr().String()
		}
		return net.ListenPacket(network, address)
	}
	udp, tcp, err := dnsFixtureSockets(net.Listen, packet)
	if err != nil {
		t.Fatal("DNS fixture could not reserve a shared TCP/UDP port:", err)
	}
	defer udp.Close()
	defer tcp.Close()
	if udp.LocalAddr().String() != tcp.Addr().String() || tcp.Addr().String() == occupied.Addr().String() {
		t.Fatal("DNS fixture did not retain matching private TCP/UDP sockets")
	}
}

type dnsFixtureListener struct {
	net.Listener
	closes int
}

func (l *dnsFixtureListener) Close() error {
	l.closes++
	return l.Listener.Close()
}

func TestDNSFixtureRetriesUDPBindCollision(t *testing.T) {
	var listeners []*dnsFixtureListener
	listen := func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err != nil {
			return nil, err
		}
		tracked := &dnsFixtureListener{Listener: listener}
		listeners = append(listeners, tracked)
		t.Cleanup(func() { listener.Close() })
		return tracked, nil
	}
	calls := 0
	packet := func(network, address string) (net.PacketConn, error) {
		calls++
		if calls == 1 {
			// Hold a real UDP socket at the first selected TCP port. The next
			// bind must return the platform's actual address-in-use error.
			blocker, err := net.ListenPacket(network, address)
			if err != nil {
				return nil, err
			}
			t.Cleanup(func() { blocker.Close() })
		}
		return net.ListenPacket(network, address)
	}
	udp, tcp, err := dnsFixtureSockets(listen, packet)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	defer tcp.Close()
	if calls < 2 || len(listeners) != calls || udp.LocalAddr().String() != tcp.Addr().String() {
		t.Fatal("DNS fixture did not recover from the forced UDP collision")
	}
	for index, listener := range listeners {
		want := 1
		if index == len(listeners)-1 {
			want = 0
		}
		if listener.closes != want {
			t.Fatal("DNS fixture leaked a rejected reservation or closed the successful one", index, listener.closes)
		}
	}
}

func TestDNSFixtureBindFailuresStayBoundedAndCloseReservations(t *testing.T) {
	for _, collision := range []bool{true, false} {
		name := "other-error"
		if collision {
			name = "repeated-collision"
		}
		t.Run(name, func(t *testing.T) {
			cause := errors.New("controlled DNS bind failure")
			var failure error = cause
			want := 1
			if collision {
				failure = &net.OpError{Op: "listen", Net: "udp", Err: syscall.EADDRINUSE}
				want = dnsFixtureBindAttempts
			}
			var listeners []*dnsFixtureListener
			listen := func(network, address string) (net.Listener, error) {
				listener, err := net.Listen(network, address)
				if err != nil {
					return nil, err
				}
				tracked := &dnsFixtureListener{Listener: listener}
				listeners = append(listeners, tracked)
				t.Cleanup(func() { listener.Close() })
				return tracked, nil
			}
			calls := 0
			packet := func(string, string) (net.PacketConn, error) { calls++; return nil, failure }
			udp, tcp, err := dnsFixtureSockets(listen, packet)
			if !errors.Is(err, failure) || udp != nil || tcp != nil || calls != want || len(listeners) != want {
				t.Fatal("Unexpected DNS allocation result", udp, tcp, err, calls, len(listeners))
			}
			for _, listener := range listeners {
				if listener.closes != 1 {
					t.Fatal("DNS fixture left a failed TCP reservation open")
				}
			}
		})
	}
	t.Run("TCP-error", func(t *testing.T) {
		failure := errors.New("controlled TCP listen failure")
		listen := func(string, string) (net.Listener, error) { return nil, failure }
		packet := func(string, string) (net.PacketConn, error) {
			t.Fatal("UDP attempted without a TCP reservation")
			return nil, nil
		}
		udp, tcp, err := dnsFixtureSockets(listen, packet)
		if !errors.Is(err, failure) || udp != nil || tcp != nil {
			t.Fatal("TCP failure was not preserved", udp, tcp, err)
		}
	})
}
