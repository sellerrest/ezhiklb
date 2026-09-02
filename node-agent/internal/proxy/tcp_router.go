package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// sniffTimeout bounds how long a TCP router waits for a ClientHello to
// arrive before giving up on SNI and routing as if the field were empty
// (matches any binding whose rules don't require SNI, i.e. a catch-all).
const sniffTimeout = 3 * time.Second

const maxSniffBytes = 16 * 1024

// tcpKeepAlivePeriod bounds how long a silently-vanished peer (network
// drop, crashed client — anything that never sends a proper FIN) can keep a
// spliced connection, and the "online" IP count it holds up, stuck. Without
// keepalive, an idle net.Conn's Read blocks forever: TCP itself only
// notices a dead peer when it actually tries to send something.
const tcpKeepAlivePeriod = 15 * time.Second

func enableKeepAlive(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(tcpKeepAlivePeriod)
	}
}

// tcpRouter owns one raw-TCP listener for one Inbound. It never terminates
// TLS — it only peeks the plaintext ClientHello that precedes the
// handshake, extracts SNI, picks a target, and then splices bytes in both
// directions unmodified. Backends keep and present their own certificates.
type tcpRouter struct {
	logger *slog.Logger
	health HealthSource
	conns  *connCounters

	listener net.Listener
	wg       sync.WaitGroup

	mu       sync.RWMutex
	bindings []compiledBinding
}

func newTCPRouter(logger *slog.Logger, health HealthSource) *tcpRouter {
	if health == nil {
		health = noopHealthSource{}
	}
	return &tcpRouter{logger: logger, health: health, conns: newConnCounters()}
}

func (t *tcpRouter) updateBindings(bindings []compiledBinding) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bindings = bindings
}

func (t *tcpRouter) start(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", addr, err)
	}
	t.listener = listener
	t.wg.Add(1)
	go t.serve(ctx)
	return nil
}

// stats reports this router's live per-outbound connection/IP counts —
// satisfies the runningRouter stats accessor Manager.Stats() uses.
func (t *tcpRouter) stats() map[string]OutboundStat { return t.conns.snapshot() }

// clientIPs reports the distinct client IPs currently connected through
// this router — satisfies runningRouter for Manager.ActiveClientIPs().
func (t *tcpRouter) clientIPs() map[string]struct{} { return t.conns.allIPs() }

func (t *tcpRouter) close() error {
	if t.listener == nil {
		return nil
	}
	err := t.listener.Close()
	t.wg.Wait()
	return err
}

func (t *tcpRouter) serve(ctx context.Context) {
	defer t.wg.Done()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			t.logger.Warn("tcp proxy accept failed", "error", err)
			continue
		}
		go t.handle(ctx, conn)
	}
}

func (t *tcpRouter) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	enableKeepAlive(conn)

	sni, peeked := t.sniff(conn)
	binding, ok := t.selectBinding(sni)
	if !ok {
		t.logger.Debug("tcp proxy: no matching binding", "sni", sni, "remote", conn.RemoteAddr().String())
		return
	}
	target, ok := pickTarget(binding.strategy, binding.targets, t.health, t.conns)
	if !ok {
		t.logger.Warn("tcp proxy: binding has no usable target", "sni", sni)
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	upstream, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", fmt.Sprintf("%s:%d", target.Address, target.Port))
	cancel()
	if err != nil {
		t.logger.Warn("tcp proxy: dial target failed", "target", target.Address, "error", err)
		return
	}
	defer upstream.Close()
	enableKeepAlive(upstream)

	clientIP := remoteIP(conn.RemoteAddr())
	t.conns.inc(target.OutboundID)
	t.conns.incIP(target.OutboundID, clientIP)
	defer t.conns.dec(target.OutboundID)
	defer t.conns.decIP(target.OutboundID, clientIP)

	splice(conn, upstream, peeked, func(n int64) { t.conns.addRx(target.OutboundID, n) }, func(n int64) { t.conns.addTx(target.OutboundID, n) })
}

// sniff waits briefly for a ClientHello, growing the read buffer until
// ExtractSNI can decide. Whatever was read is returned in peeked so it can
// be replayed to the chosen upstream — sniffing must never consume bytes
// the backend needs to see.
func (t *tcpRouter) sniff(conn net.Conn) (sni string, peeked []byte) {
	_ = conn.SetReadDeadline(time.Now().Add(sniffTimeout))
	defer conn.SetReadDeadline(time.Time{})

	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, err := conn.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			sni, sniErr := ExtractSNI(buf)
			if sniErr == nil {
				return sni, buf
			}
			if !errors.Is(sniErr, ErrIncomplete) {
				return "", buf
			}
		}
		if err != nil || len(buf) >= maxSniffBytes {
			return "", buf
		}
	}
}

func (t *tcpRouter) selectBinding(sni string) (compiledBinding, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, binding := range t.bindings {
		if !binding.enabled {
			continue
		}
		if GroupsMatch(binding.groups, sni, "") {
			return binding, true
		}
	}
	return compiledBinding{}, false
}

// countingWriter reports each Write's length to onWrite as it happens, so a
// long-lived connection's traffic shows up incrementally rather than only
// once it finally closes.
type countingWriter struct {
	io.Writer
	onWrite func(int64)
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.Writer.Write(p)
	if n > 0 {
		c.onWrite(int64(n))
	}
	return n, err
}

// splice pipes bytes bidirectionally between two already-connected sockets,
// replaying whatever was peeked from downstream first since sniff() reads
// it out of the socket buffer before this function ever sees it. onRx/onTx
// report bytes copied in each direction as they're written (client->
// upstream = rx, upstream->client = tx), for live traffic stats.
func splice(downstream, upstream net.Conn, peeked []byte, onRx, onTx func(int64)) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countingWriter{upstream, onRx}, io.MultiReader(bytes.NewReader(peeked), downstream))
		if closer, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(countingWriter{downstream, onTx}, upstream)
		if closer, ok := downstream.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}()
	wg.Wait()
}

// remoteIP strips the port from a net.Addr's string form so several
// connections from the same client (or several ports on the same client)
// count as one IP in the "Исходящие" status page's online count.
func remoteIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
