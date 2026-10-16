package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// Listen describes where the daemon accepts connections.
type Listen struct {
	// Unix, if set, is the path of a unix socket (mode 0600). Otherwise Addr is
	// a host:port.
	Unix string
	Addr string
	// Unsafe permits a non-loopback address. The API has no TLS; a non-loopback
	// bind belongs behind a tailnet or another authenticated transport.
	Unsafe bool
}

// Bind opens the listener, refusing a non-loopback TCP address unless Unsafe.
func (l Listen) Bind() (net.Listener, error) {
	if l.Unix != "" {
		if err := os.Remove(l.Unix); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		ln, err := net.Listen("unix", l.Unix)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(l.Unix, 0o600); err != nil {
			_ = ln.Close()
			return nil, err
		}
		return ln, nil
	}
	host, _, err := net.SplitHostPort(l.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen address %q: %w", l.Addr, err)
	}
	if !l.Unsafe && !isLoopback(host) {
		return nil, fmt.Errorf("refusing to listen on %q: not a loopback address (pass -unsafe-bind to allow, and put it behind an authenticated network)", l.Addr)
	}
	return net.Listen("tcp", l.Addr)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve serves h on ln until ctx is done, then shuts down gracefully.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(sctx)
		if errors.Is(err, context.DeadlineExceeded) {
			err = srv.Close()
		}
		return err
	}
}
