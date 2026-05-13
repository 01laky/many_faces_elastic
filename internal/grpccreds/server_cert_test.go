package grpccreds

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestTLSChain writes a minimal CA + server key pair and returns paths (ca.pem for mTLS client trust).
func writeTestTLSChain(t *testing.T, dir string) (serverCertPath, serverKeyPath, clientCAPath string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caSerial := big.NewInt(1)
	caTmpl := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  nil,
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srvCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	keyBytes, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	srvKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	sc := filepath.Join(dir, "server.crt")
	sk := filepath.Join(dir, "server.key")
	if err := os.WriteFile(sc, srvCertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sk, srvKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return sc, sk, caPath
}

func TestLoadServerCredentials_WithGeneratedServerPair_TLSOnly(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, _ := writeTestTLSChain(t, dir)

	creds, err := LoadServerCredentials(certPath, keyPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil transport credentials")
	}
}

func TestLoadServerCredentials_WithGeneratedServerPair_mTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, caPath := writeTestTLSChain(t, dir)

	creds, err := LoadServerCredentials(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil transport credentials")
	}
}

func TestLoadServerCredentials_mTLSClientCaInvalidPem(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, _ := writeTestTLSChain(t, dir)
	badCA := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadServerCredentials(certPath, keyPath, badCA)
	if err == nil {
		t.Fatal("expected error for invalid client CA PEM")
	}
}
