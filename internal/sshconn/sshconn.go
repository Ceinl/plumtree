// Package sshconn shares SSH listener connection behavior between the
// gateway and clean-role listeners: admission accounting, client IP
// normalization, and activity-based idle deadlines. It exists so a limit, IP,
// or deadline fix cannot update one listener and miss the other.
package sshconn

import (
	"net"
	"sync"
	"time"
)

// Admission accounts for accepted TCP connections before a goroutine or SSH
// handshake is started. A zero or negative limit disables that bound.
type Admission struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	byIP     map[string]int
}

// NewAdmission returns admission accounting with the given total and per-IP
// bounds.
func NewAdmission(maxTotal, maxPerIP int) *Admission {
	return &Admission{maxTotal: maxTotal, maxPerIP: maxPerIP, byIP: make(map[string]int)}
}

// Acquire admits one connection from ip, reporting whether capacity allowed it.
func (a *Admission) Acquire(ip string) bool {
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

// Release returns one connection from ip to the admission budget. Releasing an
// untracked ip is a no-op.
func (a *Admission) Release(ip string) {
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

// ClientIP normalizes the remote address of an accepted connection for
// admission accounting. IPv4-mapped IPv6 addresses collapse to their IPv4
// form so one client cannot occupy two per-IP buckets.
func ClientIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.AddrPort().Addr().Unmap().String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
			return ip.String()
		}
		return host
	}
	return addr.String()
}

// ActivityConn turns a net.Conn's absolute deadline into an idle deadline by
// extending it on read/write activity. It remains disabled during the
// handshake, when the caller applies the shorter fixed handshake deadline.
// Deadline refreshes are throttled to at most one syscall per half idle
// interval so chatty sessions do not pay a timerfd rearm per frame.
type ActivityConn struct {
	net.Conn
	idle                time.Duration
	mu                  sync.Mutex
	idleDeadlineEnabled bool
	nextRefresh         time.Time
	now                 func() time.Time
}

// NewActivityConn wraps conn with an idle deadline applied after
// EnableIdleDeadline. A non-positive idle clears the deadline instead.
func NewActivityConn(conn net.Conn, idle time.Duration) *ActivityConn {
	return &ActivityConn{Conn: conn, idle: idle, now: time.Now}
}

// EnableIdleDeadline starts activity-based refreshes with an immediate
// deadline application.
func (c *ActivityConn) EnableIdleDeadline() {
	c.mu.Lock()
	c.idleDeadlineEnabled = true
	c.mu.Unlock()
	c.refreshDeadline()
}

// Read reads from the wrapped connection and requests a throttled idle-deadline
// refresh after a successful read.
func (c *ActivityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

// Write writes to the wrapped connection and requests a throttled idle-deadline
// refresh after a successful write.
func (c *ActivityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

func (c *ActivityConn) refreshDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.idleDeadlineEnabled {
		return
	}
	if c.idle <= 0 {
		_ = c.Conn.SetDeadline(time.Time{})
		return
	}
	now := c.now()
	if now.Before(c.nextRefresh) {
		return
	}
	refreshInterval := c.idle / 2
	c.nextRefresh = now.Add(refreshInterval)
	// The extra refresh interval is the bounded cost of throttling. It prevents
	// activity just before nextRefresh from expiring before one full idle period.
	_ = c.Conn.SetDeadline(now.Add(c.idle + refreshInterval))
}
