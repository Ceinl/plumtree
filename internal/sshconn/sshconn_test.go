package sshconn

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestAdmissionEnforcesTotalAndPerIPLimits(t *testing.T) {
	a := NewAdmission(3, 2)
	if !a.Acquire("192.0.2.1") || !a.Acquire("192.0.2.1") {
		t.Fatal("connections up to the per-IP limit should be admitted")
	}
	if a.Acquire("192.0.2.1") {
		t.Fatal("connection above the per-IP limit was admitted")
	}
	if !a.Acquire("192.0.2.2") {
		t.Fatal("connection up to the global limit should be admitted")
	}
	if a.Acquire("192.0.2.3") {
		t.Fatal("connection above the global limit was admitted")
	}
	a.Release("192.0.2.1")
	if !a.Acquire("192.0.2.3") {
		t.Fatal("released capacity was not reusable")
	}
}

func TestAdmissionDisabledLimits(t *testing.T) {
	for _, limits := range [][2]int{{0, 0}, {-1, -1}} {
		a := NewAdmission(limits[0], limits[1])
		for i := 0; i < 10; i++ {
			if !a.Acquire("192.0.2.1") {
				t.Fatalf("disabled limits %v rejected connection %d", limits, i)
			}
		}
		a.Release("unknown")
		if !a.Acquire("192.0.2.1") {
			t.Fatalf("disabled limits %v rejected after releasing untracked ip", limits)
		}
	}
}

type stubAddr struct{ s string }

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return a.s }

func TestClientIPNormalizesIPv4MappedIPv6(t *testing.T) {
	cases := []struct {
		name string
		addr net.Addr
		want string
	}{
		{
			name: "tcp addr mapped",
			addr: &net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.1"), Port: 2222},
			want: "192.0.2.1",
		},
		{
			name: "tcp addr plain",
			addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 2222},
			want: "192.0.2.1",
		},
		{
			name: "hostport mapped",
			addr: stubAddr{s: "[::ffff:192.0.2.1]:2222"},
			want: "192.0.2.1",
		},
		{
			name: "hostport v6",
			addr: stubAddr{s: "[2001:db8::1]:2222"},
			want: "2001:db8::1",
		},
		{
			name: "hostport hostname",
			addr: stubAddr{s: "example.com:2222"},
			want: "example.com",
		},
		{
			name: "unparseable falls back",
			addr: stubAddr{s: "unparsable"},
			want: "unparsable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientIP(tc.addr); got != tc.want {
				t.Fatalf("ClientIP(%v) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

// stubConn answers reads and writes without blocking so deadline-throttle
// behavior can be tested without pipe plumbing or sleeping.
type stubConn struct {
	mu        sync.Mutex
	deadlines []time.Time
}

func (s *stubConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func (s *stubConn) Write(p []byte) (int, error) { return len(p), nil }
func (s *stubConn) Close() error                { return nil }
func (s *stubConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (s *stubConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (s *stubConn) SetDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadlines = append(s.deadlines, t)
	return nil
}
func (s *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (s *stubConn) SetWriteDeadline(time.Time) error { return nil }

func (s *stubConn) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deadlines)
}

func (s *stubConn) last() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadlines[len(s.deadlines)-1]
}

func TestActivityConnThrottlesDeadlineRefresh(t *testing.T) {
	stub := &stubConn{}
	conn := NewActivityConn(stub, 200*time.Millisecond)

	conn.EnableIdleDeadline()
	if got := stub.count(); got != 1 {
		t.Fatalf("deadline updates after enable = %d, want 1", got)
	}

	for i := 0; i < 10; i++ {
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		buf := make([]byte, 1)
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if got := stub.count(); got != 1 {
		t.Fatalf("deadline updates after rapid activity = %d, want 1 (throttled)", got)
	}

	time.Sleep(120 * time.Millisecond)
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write after refresh window: %v", err)
	}
	if got := stub.count(); got != 2 {
		t.Fatalf("deadline updates after refresh window = %d, want 2", got)
	}
}

func TestActivityConnDisabledIdleClearsDeadline(t *testing.T) {
	for _, idle := range []time.Duration{0, -time.Second} {
		stub := &stubConn{}
		conn := NewActivityConn(stub, idle)

		conn.EnableIdleDeadline()
		if got := stub.count(); got != 1 {
			t.Fatalf("idle %v: deadline updates after enable = %d, want 1", idle, got)
		}
		if last := stub.last(); !last.IsZero() {
			t.Fatalf("idle %v: deadline after enable = %v, want zero (cleared)", idle, last)
		}

		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatalf("idle %v: write: %v", idle, err)
		}
		if last := stub.last(); !last.IsZero() {
			t.Fatalf("idle %v: deadline after write = %v, want zero (cleared)", idle, last)
		}
	}
}

func TestActivityConnRefreshesIdleDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	conn := NewActivityConn(server, 300*time.Millisecond)
	conn.EnableIdleDeadline()
	buf := make([]byte, 1)
	for i := 0; i < 3; i++ {
		time.Sleep(100 * time.Millisecond)
		writeErr := make(chan error, 1)
		go func() {
			_, err := client.Write([]byte{'x'})
			writeErr <- err
		}()
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("read %d before refreshed deadline: %v", i, err)
		}
		if err := <-writeErr; err != nil {
			t.Fatalf("write %d before refreshed deadline: %v", i, err)
		}
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("read after idle timeout unexpectedly succeeded")
	}
}
