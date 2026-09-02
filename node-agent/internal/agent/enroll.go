package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Enrollment holds the node's long-lived local identity: a self-signed TLS
// keypair and a random API key. The panel pins the exact certificate PEM
// printed at first boot (trust-on-first-use, the same idea as the reference
// panel's "Сертификат" field) and authenticates every call with the API key
// as a Bearer token, so both must stay stable across restarts — never
// regenerated once a panel has paired with this node.
type Enrollment struct {
	APIKey  string
	CertPEM string
	TLSCert tls.Certificate
}

// LoadOrCreateEnrollment ensures dir/{cert,key}.pem and dir/api_key exist,
// generating them on first boot, and returns the loaded identity plus
// whether this call created them (so the caller knows to print the
// connection block).
func LoadOrCreateEnrollment(dir string) (*Enrollment, bool, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, false, err
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	apiKeyPath := filepath.Join(dir, "api_key")

	created := false
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		if err := generateSelfSigned(certPath, keyPath); err != nil {
			return nil, false, fmt.Errorf("generate TLS keypair: %w", err)
		}
		created = true
	} else if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(apiKeyPath); os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(apiKeyPath, []byte(hex.EncodeToString(key)), 0600); err != nil {
			return nil, false, err
		}
		created = true
	} else if err != nil {
		return nil, false, err
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, false, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, false, err
	}
	apiKeyRaw, err := os.ReadFile(apiKeyPath)
	if err != nil {
		return nil, false, err
	}
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, false, fmt.Errorf("load TLS keypair: %w", err)
	}

	return &Enrollment{
		APIKey:  strings.TrimSpace(string(apiKeyRaw)),
		CertPEM: string(certPEM),
		TLSCert: tlsCert,
	}, created, nil
}

func generateSelfSigned(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ezhiklb-node"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(15, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"ezhiklb-node"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return err
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}

// ConnectionBlock renders the paste-able enrollment block for the operator
// to copy into the panel's "Добавить узел" dialog (screenshot-3 shape:
// address, port, API key, certificate).
func (e *Enrollment) ConnectionBlock(address string, port int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Адрес узла:  %s\n", address)
	fmt.Fprintf(&b, "Порт узла:   %d\n", port)
	fmt.Fprintf(&b, "API ключ:    %s\n", e.APIKey)
	fmt.Fprintf(&b, "Сертификат:\n%s\n", e.CertPEM)
	return b.String()
}

// DetectPublicIPv4 mirrors install.sh's detect_server_ipv4: opening a UDP
// "connection" sends no packets, it only asks the kernel to pick the route
// (and therefore the source address) it would use to reach that target.
func DetectPublicIPv4() string {
	conn, err := net.Dial("udp4", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}
