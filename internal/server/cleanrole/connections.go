package cleanrole

import (
	"net"
	"sync"
	"time"
)

type connectionAdmission struct {
	mu       sync.Mutex
	maxTotal int
	maxPerIP int
	total    int
	byIP     map[string]int
}

type activityConn struct {
	net.Conn
	idle        time.Duration
	mu          sync.Mutex
	on          bool
	nextRefresh time.Time
}

func newActivityConn(connection net.Conn, idle time.Duration) *activityConn {
	return &activityConn{Conn: connection, idle: idle}
}

func (c *activityConn) enableIdleDeadline() {
	c.mu.Lock()
	c.on = true
	c.mu.Unlock()
	c.refreshDeadline()
}

func (c *activityConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

func (c *activityConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if n > 0 {
		c.refreshDeadline()
	}
	return n, err
}

func (c *activityConn) refreshDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on {
		return
	}
	if c.idle <= 0 {
		_ = c.Conn.SetDeadline(time.Time{})
		return
	}
	now := time.Now()
	if now.Before(c.nextRefresh) {
		return
	}
	c.nextRefresh = now.Add(c.idle / 2)
	_ = c.Conn.SetDeadline(now.Add(c.idle))
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

func connectionIP(address net.Addr) string {
	host, _, err := net.SplitHostPort(address.String())
	if err == nil {
		return host
	}
	return address.String()
}
