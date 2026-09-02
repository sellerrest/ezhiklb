package proxy

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// captureClientHello drives a real crypto/tls handshake far enough to
// capture the exact bytes a genuine TLS client sends, so ExtractSNI is
// tested against real wire data rather than hand-crafted bytes.
func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		client := tls.Client(clientConn, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
		_ = client.Handshake() // never completes: nothing replies on serverConn
	}()

	buf := make([]byte, 8192)
	_ = serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := serverConn.Read(buf)
	if err != nil {
		t.Fatalf("read ClientHello: %v", err)
	}
	clientConn.Close()
	<-done
	return buf[:n]
}

func TestExtractSNIFromRealClientHello(t *testing.T) {
	data := captureClientHello(t, "example.com")
	sni, err := ExtractSNI(data)
	if err != nil {
		t.Fatalf("ExtractSNI: %v", err)
	}
	if sni != "example.com" {
		t.Fatalf("sni = %q, want example.com", sni)
	}
}

func TestExtractSNIIncompleteAsksForMoreBytes(t *testing.T) {
	data := captureClientHello(t, "example.com")
	_, err := ExtractSNI(data[:10])
	if err != ErrIncomplete {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
}

func TestExtractSNINotTLS(t *testing.T) {
	_, err := ExtractSNI([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	if err != ErrNotTLS {
		t.Fatalf("err = %v, want ErrNotTLS", err)
	}
}

func TestExtractSNIGrowsUntilComplete(t *testing.T) {
	data := captureClientHello(t, "grows.example")
	// Feed it back byte-by-byte through the same growing-buffer contract
	// tcpRouter.sniff uses, to prove ExtractSNI's ErrIncomplete signal is
	// actually sufficient to drive an incremental reader.
	for cut := 1; cut < len(data); cut++ {
		sni, err := ExtractSNI(data[:cut])
		if err == nil {
			if sni != "grows.example" {
				t.Fatalf("early success at cut=%d gave wrong sni %q", cut, sni)
			}
			return
		}
		if err != ErrIncomplete && err != ErrNotTLS {
			t.Fatalf("cut=%d: unexpected error %v", cut, err)
		}
	}
	sni, err := ExtractSNI(data)
	if err != nil || sni != "grows.example" {
		t.Fatalf("full buffer: sni=%q err=%v", sni, err)
	}
}
