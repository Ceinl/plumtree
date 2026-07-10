package gateway

import (
	"net"
	"sync"
	"time"
)

// connectionAdmission accounts for accepted TCP connections before a goroutine
// or SSH handshake is started.
type connectionAdmission struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	byIP     map[string]int
}

func newConnectionAdmission(maxTotal, maxPerIP int) *connectionAdmission {
	return &connectionAdmission{maxTotal: maxTotal, maxPerIP: maxPerIP, byIP: make(map[string]int)}
}

func (a *connectionAdmission) acquire(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maxTotal > 0 && a.total >= a.maxTotal {
		return false
	}
	if a.maxPerIP > 0 && a.byIP[ip] >= a.maxPerIP {
		return false
	}
	a.total++
	a.byIP[ip]++
	return true
}

func (a *connectionAdmission) release(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.byIP[ip] == 0 {
		return
	}
	a.total--
	a.byIP[ip]--
	if a.byIP[ip] == 0 {
		delete(a.byIP, ip)
	}
}

func connectionIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.AddrPort().Addr().Unmap().String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	return addr.String()
}

func effectiveLimit(configured, fallback int) int {
	if configured == 0 {
		return fallback
	}
	if configured < 0 {
		return 0
	}
	return configured
}

func effectiveDuration(configured, fallback time.Duration) time.Duration {
	if configured == 0 {
		return fallback
	}
	if configured < 0 {
		return 0
	}
	return configured
}

// activityConn turns a net.Conn's absolute deadline into an idle deadline by
// extending it after every successful read or write. It remains disabled during
// the handshake, when Server applies the shorter fixed handshake deadline.
type activityConn struct {
	net.Conn
	idle time.Duration
	mu   sync.Mutex
	on   bool
}

func newActivityConn(conn net.Conn, idle time.Duration) *activityConn {
	return &activityConn{Conn: conn, idle: idle}
}

func (c *activityConn) enableIdleDeadline() {
	c.mu.Lock()
	c.on = true
	c.mu.Unlock()
	c.refreshDeadline()
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

func (c *activityConn) refreshDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()
	on, idle := c.on, c.idle
	if !on {
		return
	}
	if idle <= 0 {
		_ = c.Conn.SetDeadline(time.Time{})
		return
	}
	_ = c.Conn.SetDeadline(time.Now().Add(idle))
}
