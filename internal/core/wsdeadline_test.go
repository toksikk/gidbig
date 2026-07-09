package gidbig

import (
	"errors"
	"net"
	"testing"
	"time"
)

func newTestDeadlineConn(c net.Conn, timeout time.Duration) *deadlineConn {
	return &deadlineConn{Conn: c, readTimeout: timeout, writeTimeout: timeout}
}

func assertTimeoutErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() {
		t.Fatalf("expected net timeout error, got %v", err)
	}
}

func TestDeadlineConnReadTimesOut(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	c := newTestDeadlineConn(client, 50*time.Millisecond)

	buf := make([]byte, 1)
	_, err := c.Read(buf)
	assertTimeoutErr(t, err)
}

func TestDeadlineConnWriteTimesOut(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	c := newTestDeadlineConn(client, 50*time.Millisecond)

	// Nobody reads from server, so the pipe write blocks until the
	// armed deadline fires.
	_, err := c.Write([]byte("x"))
	assertTimeoutErr(t, err)
}

func TestDeadlineConnReadWithinDeadlineSucceeds(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	c := newTestDeadlineConn(client, time.Second)

	go func() {
		_, _ = server.Write([]byte("ok"))
	}()

	buf := make([]byte, 2)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if string(buf[:n]) != "ok" {
		t.Fatalf("expected %q, got %q", "ok", string(buf[:n]))
	}
}

func TestDeadlineConnDeadlineRearmsPerCall(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	timeout := 200 * time.Millisecond
	c := newTestDeadlineConn(client, timeout)

	// Two reads, each delayed by more than half the timeout: combined
	// they exceed a single deadline window, so both only succeed if the
	// deadline is re-armed on every Read call.
	go func() {
		for range 2 {
			time.Sleep(120 * time.Millisecond)
			_, _ = server.Write([]byte("x"))
		}
	}()

	buf := make([]byte, 1)
	for i := range 2 {
		if _, err := c.Read(buf); err != nil {
			t.Fatalf("read %d failed: %v", i, err)
		}
	}
}

func TestNewDeadlineDialerWrapsConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
		}
	}()

	dialer := newDeadlineDialer()
	if dialer.NetDialContext == nil {
		t.Fatal("expected NetDialContext to be set")
	}

	conn, err := dialer.NetDialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	dc, ok := conn.(*deadlineConn)
	if !ok {
		t.Fatalf("expected *deadlineConn, got %T", conn)
	}
	if dc.readTimeout != wsReadTimeout {
		t.Errorf("readTimeout = %v, want %v", dc.readTimeout, wsReadTimeout)
	}
	if dc.writeTimeout != wsWriteTimeout {
		t.Errorf("writeTimeout = %v, want %v", dc.writeTimeout, wsWriteTimeout)
	}
}
