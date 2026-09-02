package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"
)

// countingReadCloser/countingResponseWriter report request-body and
// response-body bytes as they're read/written — request body = client-
// >outbound ("rx"), response body = outbound->client ("tx") — the same
// incremental accounting tcp_router.go's splice() does for raw TCP.
type countingReadCloser struct {
	io.ReadCloser
	onRead func(int64)
}

func (c countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		c.onRead(int64(n))
	}
	return n, err
}

type countingResponseWriter struct {
	http.ResponseWriter
	onWrite func(int64)
}

func (c countingResponseWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	if n > 0 {
		c.onWrite(int64(n))
	}
	return n, err
}

func (c countingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// httpRouter owns one plain-HTTP listener for one HTTP-mode Inbound. It
// matches the Host header (the unencrypted equivalent of TLS SNI for plain
// HTTP) and the request path against Binding rules, then reverse-proxies to
// the chosen outbound. This deliberately does not terminate TLS — an
// HTTP-mode inbound is plaintext HTTP on its listen port; routing HTTPS by
// Host+path would require this proxy to hold a certificate for every
// matched domain, which is out of scope here.
type httpRouter struct {
	logger *slog.Logger
	health HealthSource
	conns  *connCounters

	server *http.Server

	mu       sync.RWMutex
	bindings []compiledBinding
}

func newHTTPRouter(logger *slog.Logger, health HealthSource) *httpRouter {
	if health == nil {
		health = noopHealthSource{}
	}
	return &httpRouter{logger: logger, health: health, conns: newConnCounters()}
}

func (h *httpRouter) updateBindings(bindings []compiledBinding) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bindings = bindings
}

func (h *httpRouter) start(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", addr, err)
	}
	h.server = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := h.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			h.logger.Warn("http proxy server stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.server.Shutdown(shutdownCtx)
	}()
	return nil
}

// stats reports this router's live per-outbound connection/IP counts —
// satisfies the runningRouter stats accessor Manager.Stats() uses.
func (h *httpRouter) stats() map[string]OutboundStat { return h.conns.snapshot() }

// clientIPs reports the distinct client IPs currently connected through
// this router — satisfies runningRouter for Manager.ActiveClientIPs().
func (h *httpRouter) clientIPs() map[string]struct{} { return h.conns.allIPs() }

func (h *httpRouter) close() error {
	if h.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.server.Shutdown(shutdownCtx)
}

func (h *httpRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	binding, ok := h.selectBinding(host, r.URL.Path)
	if !ok {
		http.Error(w, "no route matches this request", http.StatusBadGateway)
		return
	}
	target, ok := pickTarget(binding.strategy, binding.targets, h.health, h.conns)
	if !ok {
		http.Error(w, "no reachable upstream", http.StatusBadGateway)
		return
	}

	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = host
	}
	h.conns.inc(target.OutboundID)
	h.conns.incIP(target.OutboundID, clientIP)
	defer h.conns.dec(target.OutboundID)
	defer h.conns.decIP(target.OutboundID, clientIP)

	if r.Body != nil {
		r.Body = countingReadCloser{r.Body, func(n int64) { h.conns.addRx(target.OutboundID, n) }}
	}
	countingW := countingResponseWriter{w, func(n int64) { h.conns.addTx(target.OutboundID, n) }}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = fmt.Sprintf("%s:%d", target.Address, target.Port)
		},
		ErrorLog: nil,
	}
	proxy.ServeHTTP(countingW, r)
}

func (h *httpRouter) selectBinding(host, path string) (compiledBinding, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, binding := range h.bindings {
		if !binding.enabled {
			continue
		}
		if GroupsMatch(binding.groups, host, path) {
			return binding, true
		}
	}
	return compiledBinding{}, false
}
