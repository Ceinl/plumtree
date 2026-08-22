package cleanrole

import (
	"net"
	"testing"
	"time"
)

type deadlineCountingConn struct {
	net.Conn
	deadlines int
}

func (c *deadlineCountingConn) SetDeadline(deadline time.Time) error {
	c.deadlines++
	return c.Conn.SetDeadline(deadline)
}

func TestActivityDeadlineRefreshIsThrottled(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	counting := &deadlineCountingConn{Conn: left}
	connection := newActivityConn(counting, time.Minute)

	connection.enableIdleDeadline()
	connection.refreshDeadline()
	if counting.deadlines != 1 {
		t.Fatalf("deadline updates = %d, want 1", counting.deadlines)
	}

	connection.nextRefresh = time.Now().Add(-time.Second)
	connection.refreshDeadline()
	if counting.deadlines != 2 {
		t.Fatalf("deadline updates after refresh window = %d, want 2", counting.deadlines)
	}
}
