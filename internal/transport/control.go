package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/protocol/control"
)

type streamConn struct {
	rw io.ReadWriteCloser
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.rw.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.rw.Write(p) }
func (c *streamConn) Close() error                { return c.rw.Close() }
func (c *streamConn) LocalAddr() net.Addr         { return streamAddr("local") }
func (c *streamConn) RemoteAddr() net.Addr        { return streamAddr("plumtree-control") }
func (c *streamConn) SetDeadline(t time.Time) error {
	if conn, ok := c.rw.(net.Conn); ok {
		return conn.SetDeadline(t)
	}
	return nil
}
func (c *streamConn) SetReadDeadline(t time.Time) error {
	if conn, ok := c.rw.(net.Conn); ok {
		return conn.SetReadDeadline(t)
	}
	return nil
}
func (c *streamConn) SetWriteDeadline(t time.Time) error {
	if conn, ok := c.rw.(net.Conn); ok {
		return conn.SetWriteDeadline(t)
	}
	return nil
}

type streamAddr string

func (a streamAddr) Network() string { return "plumtree-ssh" }
func (a streamAddr) String() string  { return string(a) }

type oneListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newOneListener(conn net.Conn) *oneListener {
	return &oneListener{conn: conn, closed: make(chan struct{})}
}

func (l *oneListener) Accept() (net.Conn, error) {
	accepted := false
	l.once.Do(func() { accepted = true })
	if accepted {
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneListener) Close() error {
	l.signal()
	return l.conn.Close()
}

func (l *oneListener) signal() {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
}

func (l *oneListener) Addr() net.Addr { return streamAddr("plumtree-control") }

// ServeHTTPStream serves exactly one HTTP/1.1 connection. The caller owns
// the SSH subsystem and should close it after this function returns.
func ServeHTTPStream(channel io.ReadWriteCloser, handler http.Handler, productVersion string) error {
	if channel == nil || handler == nil || productVersion == "" {
		return errors.New("transport: invalid control stream")
	}
	conn := &streamConn{rw: channel}
	listener := newOneListener(conn)
	server := &http.Server{
		Handler:           versionedHandler{next: handler, productVersion: productVersion},
		MaxHeaderBytes:    control.MaxHeaderBytes,
		ReadHeaderTimeout: 10 * time.Second,
	}
	server.SetKeepAlivesEnabled(false)
	server.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			listener.signal()
		}
	}
	err := server.Serve(listener)
	if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type versionedHandler struct {
	next           http.Handler
	productVersion string
}

func (h versionedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := control.ValidateRequest(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set(control.VersionHeader, h.productVersion)
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, control.MaxBodyBytes)
	}
	h.next.ServeHTTP(w, r)
}

// NewHTTPClient creates a client over one already-authenticated control
// channel. No external HTTP or TLS listener is involved.
func NewHTTPClient(channel io.ReadWriteCloser, expectedVersion string) (*http.Client, io.Closer, error) {
	if channel == nil || expectedVersion == "" {
		return nil, nil, errors.New("transport: invalid control client")
	}
	base := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &streamConn{rw: channel}, nil
		},
		MaxResponseHeaderBytes: control.MaxHeaderBytes,
		DisableKeepAlives:      true,
	}
	client := &http.Client{Transport: checkedRoundTripper{base: base, expectedVersion: expectedVersion}}
	return client, closeTransport{transport: base, channel: channel}, nil
}

type closeTransport struct {
	transport *http.Transport
	channel   io.Closer
}

func (c closeTransport) Close() error {
	c.transport.CloseIdleConnections()
	return c.channel.Close()
}

type checkedRoundTripper struct {
	base            http.RoundTripper
	expectedVersion string
}

func (t checkedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := control.ValidateRequest(r); err != nil {
		return nil, err
	}
	if err := RejectIdentityHeaders(r.Header); err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if err := control.ValidateResponse(response, t.expectedVersion); err != nil {
		response.Body.Close()
		return nil, fmt.Errorf("transport: %w", err)
	}
	return response, nil
}
