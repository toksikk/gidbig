package gidbig

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Timeouts for the Discord gateway and voice websocket connections.
//
// The pinned discordgo fork never sets read or write deadlines on its
// websockets, so on a degraded network ReadMessage/WriteJSON can block
// forever: Open() hangs waiting for the Hello packet while holding the
// session lock, and the heartbeat goroutine hangs in WriteJSON while
// holding wsMutex — in both cases the reconnect loop stalls silently
// without logging anything. Wrapping the underlying net.Conn bounds
// every read and write, so a stalled connection surfaces as an error
// and discordgo's own close-and-reconnect path takes over.
const (
	// wsReadTimeout must comfortably exceed the gateway heartbeat
	// interval (~41s): heartbeat ACKs guarantee at least one read per
	// interval on a healthy connection, so two minutes of read silence
	// means the connection is dead.
	wsReadTimeout = 2 * time.Minute
	// wsWriteTimeout bounds a single websocket frame write; on a
	// healthy connection writes complete in milliseconds.
	wsWriteTimeout = 30 * time.Second
	// wsDialTimeout bounds the TCP connect during (re)connects.
	wsDialTimeout = 30 * time.Second
)

// deadlineConn arms a fresh deadline before every Read/Write so a dead
// TCP connection can never block a websocket operation indefinitely.
//
// Deadlines set externally via the promoted SetReadDeadline /
// SetWriteDeadline still reach the underlying conn, but are
// deliberately overwritten on the next Read/Write: the per-call
// deadline is the whole point of the wrapper. The pinned discordgo
// fork never sets its own deadlines (verified against 930441e7); if a
// future fork bump introduces them, revisit this wrapper.
type deadlineConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

// newDeadlineDialer returns a websocket dialer that wraps every
// connection in a deadlineConn. Assigned to discordgo's Session.Dialer,
// which is used for both the gateway and voice websockets.
func newDeadlineDialer() *websocket.Dialer {
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: wsDialTimeout}
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &deadlineConn{
				Conn:         conn,
				readTimeout:  wsReadTimeout,
				writeTimeout: wsWriteTimeout,
			}, nil
		},
	}
}
