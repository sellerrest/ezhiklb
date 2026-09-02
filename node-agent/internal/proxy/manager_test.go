package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

// freeAddr asks the OS for an ephemeral TCP port so tests never depend on a
// fixed port being available on the machine running them.
func freeAddr(t *testing.T) (host string, port uint16) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()
	return addr.IP.String(), uint16(addr.Port)
}

// echoBackend is a plain (non-TLS) TCP server that records everything it
// receives on the first connection and echoes back "ack" once it has read
// wantBytes bytes, so the test client has something deterministic to wait
// on before asserting.
func echoBackend(t *testing.T, wantBytes int) (addr string, port uint16, received func() []byte) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr := l.Addr().(*net.TCPAddr)
	var buf bytes.Buffer
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.CopyN(&buf, conn, int64(wantBytes))
		conn.Write([]byte("ack"))
	}()
	t.Cleanup(func() { l.Close() })
	return tcpAddr.IP.String(), uint16(tcpAddr.Port), func() []byte { return buf.Bytes() }
}

func TestManagerRoutesTCPBySNI(t *testing.T) {
	helloA := captureClientHello(t, "a.example")
	helloB := captureClientHello(t, "b.example")
	extra := []byte("-payload-after-hello")

	backendAAddr, backendAPort, backendAReceived := echoBackend(t, len(helloA)+len(extra))
	backendBAddr, backendBPort, backendBReceived := echoBackend(t, len(helloB)+len(extra))

	listenHost, listenPort := freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(ctx, nil)

	cfg := domain.ProfileConfig{
		SchemaVersion: domain.SchemaVersion,
		Inbounds:      []domain.Inbound{{ID: "in1", Name: "TLS", Enabled: true, ListenAddress: listenHost, ListenPort: listenPort, Mode: domain.InboundModeTCP, TCP: true}},
		Outbounds: []domain.Outbound{
			{ID: "outA", Name: "A", Address: backendAAddr, Port: backendAPort, Enabled: true},
			{ID: "outB", Name: "B", Address: backendBAddr, Port: backendBPort, Enabled: true},
		},
		Bindings: []domain.Binding{
			{ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast,
				Groups:  []domain.MatchGroup{{Conditions: []domain.MatchCondition{{Field: domain.MatchFieldSNI, Operator: domain.MatchOpEquals, Value: "a.example"}}}},
				Targets: []domain.BindingTarget{{OutboundID: "outA", WeightPercent: 100}}},
			{ID: "b2", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast,
				Groups:  []domain.MatchGroup{{Conditions: []domain.MatchCondition{{Field: domain.MatchFieldSNI, Operator: domain.MatchOpEquals, Value: "b.example"}}}},
				Targets: []domain.BindingTarget{{OutboundID: "outB", WeightPercent: 100}}},
		},
	}
	if err := manager.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(manager.Stop)

	dialAndSend := func(hello []byte) []byte {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(listenHost, strconv.Itoa(int(listenPort))), 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write(append(append([]byte(nil), hello...), extra...)); err != nil {
			t.Fatal(err)
		}
		ack := make([]byte, 3)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, ack); err != nil {
			t.Fatalf("no ack from backend (routed to the wrong place, or not routed at all): %v", err)
		}
		return ack
	}

	dialAndSend(helloA)
	dialAndSend(helloB)

	gotA := backendAReceived()
	wantA := append(append([]byte(nil), helloA...), extra...)
	if !bytes.Equal(gotA, wantA) {
		t.Fatalf("backend A received %d bytes, want %d bytes matching helloA+extra", len(gotA), len(wantA))
	}
	gotB := backendBReceived()
	wantB := append(append([]byte(nil), helloB...), extra...)
	if !bytes.Equal(gotB, wantB) {
		t.Fatalf("backend B received %d bytes, want %d bytes matching helloB+extra", len(gotB), len(wantB))
	}
}

// TestManagerStatsTracksLiveConnectionsAndIPs proves Manager.Stats() — the
// data source behind the panel's "Исходящие" online/IP-count reading —
// actually reflects a real in-flight connection, and drops back to zero
// once it closes.
func TestManagerStatsTracksLiveConnectionsAndIPs(t *testing.T) {
	hello := captureClientHello(t, "stats.example")

	backendL, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backendL.Close()
	release := make(chan struct{})
	go func() {
		conn, err := backendL.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		go io.Copy(io.Discard, conn)                               // drain whatever the client sends, so writes never block
		_, _ = conn.Write([]byte("reply-from-backend-0123456789")) // fixed payload for the BytesTx assertion
		<-release
	}()
	backendAddr := backendL.Addr().(*net.TCPAddr)

	listenHost, listenPort := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(ctx, nil)

	cfg := domain.ProfileConfig{
		SchemaVersion: domain.SchemaVersion,
		Inbounds:      []domain.Inbound{{ID: "in1", Name: "TLS", Enabled: true, ListenAddress: listenHost, ListenPort: listenPort, Mode: domain.InboundModeTCP, TCP: true}},
		Outbounds:     []domain.Outbound{{ID: "outA", Name: "A", Address: backendAddr.IP.String(), Port: uint16(backendAddr.Port), Enabled: true}},
		Bindings: []domain.Binding{
			{ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast, Targets: []domain.BindingTarget{{OutboundID: "outA", WeightPercent: 100}}},
		},
	}
	if err := manager.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(manager.Stop)

	if got := manager.Stats()["outA"]; got.ActiveConnections != 0 {
		t.Fatalf("before connecting: stats = %+v, want zero", got)
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(listenHost, strconv.Itoa(int(listenPort))), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(hello); err != nil {
		t.Fatal(err)
	}

	waitFor := func(want int64) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if manager.Stats()["outA"].ActiveConnections == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("stats never reached %d active connections: %+v", want, manager.Stats()["outA"])
	}
	waitFor(1)
	if got := manager.Stats()["outA"]; got.ActiveIPs != 1 {
		t.Fatalf("mid-connection stats = %+v, want 1 active IP", got)
	}

	extraPayload := []byte("extra-client-traffic-payload-1234567890")
	if _, err := conn.Write(extraPayload); err != nil {
		t.Fatal(err)
	}
	wantRx := int64(len(hello) + len(extraPayload))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stat := manager.Stats()["outA"]
		if stat.BytesRx >= wantRx && stat.BytesTx > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.Stats()["outA"]; got.BytesRx < wantRx || got.BytesTx == 0 {
		t.Fatalf("byte counters = %+v, want BytesRx >= %d and BytesTx > 0", got, wantRx)
	}

	close(release)
	conn.Close()
	waitFor(0)
	if got := manager.Stats()["outA"]; got.ActiveIPs != 0 {
		t.Fatalf("after closing: stats = %+v, want zero IPs", got)
	}
}

func TestManagerRoutesHTTPByHostAndPath(t *testing.T) {
	usersBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "users:%s", r.URL.Path)
	}))
	defer usersBackend.Close()
	ordersBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "orders:%s", r.URL.Path)
	}))
	defer ordersBackend.Close()

	usersAddr, usersPort := splitHostPort(t, usersBackend.Listener.Addr().String())
	ordersAddr, ordersPort := splitHostPort(t, ordersBackend.Listener.Addr().String())

	listenHost, listenPort := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(ctx, nil)

	cfg := domain.ProfileConfig{
		SchemaVersion: domain.SchemaVersion,
		Inbounds:      []domain.Inbound{{ID: "in1", Name: "HTTP", Enabled: true, ListenAddress: listenHost, ListenPort: listenPort, Mode: domain.InboundModeHTTP, TCP: true}},
		Outbounds: []domain.Outbound{
			{ID: "outUsers", Name: "Users", Address: usersAddr, Port: usersPort, Enabled: true},
			{ID: "outOrders", Name: "Orders", Address: ordersAddr, Port: ordersPort, Enabled: true},
		},
		Bindings: []domain.Binding{
			{ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast,
				Groups:  []domain.MatchGroup{{Conditions: []domain.MatchCondition{{Field: domain.MatchFieldPath, Operator: domain.MatchOpStartsWith, Value: "/users"}}}},
				Targets: []domain.BindingTarget{{OutboundID: "outUsers", WeightPercent: 100}}},
			{ID: "b2", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionLeast,
				Groups:  []domain.MatchGroup{{Conditions: []domain.MatchCondition{{Field: domain.MatchFieldPath, Operator: domain.MatchOpStartsWith, Value: "/orders"}}}},
				Targets: []domain.BindingTarget{{OutboundID: "outOrders", WeightPercent: 100}}},
		},
	}
	if err := manager.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(manager.Stop)

	base := fmt.Sprintf("http://%s", net.JoinHostPort(listenHost, strconv.Itoa(int(listenPort))))
	get := func(path string) string {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	if got := get("/users/42"); got != "users:/users/42" {
		t.Fatalf("path /users/42 routed to %q", got)
	}
	if got := get("/orders/7"); got != "orders:/orders/7" {
		t.Fatalf("path /orders/7 routed to %q", got)
	}
}

func splitHostPort(t *testing.T, addr string) (string, uint16) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, uint16(port)
}
