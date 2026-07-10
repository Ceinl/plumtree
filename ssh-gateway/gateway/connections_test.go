package gateway

import (
	"net"
	"testing"
	"time"
)

func TestConnectionAdmissionEnforcesGlobalAndPerIPLimits(t *testing.T) {
	a := newConnectionAdmission(3, 2)
	if !a.acquire("192.0.2.1") || !a.acquire("192.0.2.1") {
		t.Fatal("connections up to the per-IP limit should be admitted")
	}
	if a.acquire("192.0.2.1") {
		t.Fatal("connection above the per-IP limit was admitted")
	}
	if !a.acquire("192.0.2.2") {
		t.Fatal("connection up to the global limit should be admitted")
	}
	if a.acquire("192.0.2.3") {
		t.Fatal("connection above the global limit was admitted")
	}
	a.release("192.0.2.1")
	if !a.acquire("192.0.2.3") {
		t.Fatal("released capacity was not reusable")
	}
}

func TestActivityConnRefreshesIdleDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	conn := newActivityConn(server, 80*time.Millisecond)
	conn.enableIdleDeadline()
	buf := make([]byte, 1)
	for i := 0; i < 3; i++ {
		time.Sleep(40 * time.Millisecond)
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
	time.Sleep(100 * time.Millisecond)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("read after idle timeout unexpectedly succeeded")
	}
}
