package listen_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// pair makes a self-signed certificate for 127.0.0.1 with the given serial and
// returns its PEM certificate and key.
func pair(t *testing.T, serial int64) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "dsh test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}

// replace writes both files and moves their modification time on, as a renewal
// tool replacing the pair would.
func replace(t *testing.T, certFile, keyFile string, certPEM, keyPEM []byte, at time.Time) {
	t.Helper()
	for file, b := range map[string][]byte{certFile: certPEM, keyFile: keyPEM} {
		if err := os.WriteFile(file, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// served opens a new connection and returns the serial of the certificate it sees.
func served(t *testing.T, l listen.Listener) int64 {
	t.Helper()
	conn, err := tls.Dial("tcp", strings.TrimPrefix(l.Origin(), "https://"), &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test reads the presented certificate, not its trust
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64()
}

// TC-10b: a replaced valid pair is served to the next handshake without restart; a
// replaced bad pair keeps the previous one.
//
//dsh:test DH-T-HOST-2
func TestTLSReloadsReplacedPair(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	c1, k1 := pair(t, 1)
	now := time.Now()
	replace(t, certFile, keyFile, c1, k1, now)
	l, err := start(t, cordisRoot(t), listen.Config{Address: "127.0.0.1:0", CertFile: certFile, KeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	if !l.Secure() || !strings.HasPrefix(l.Origin(), "https://") {
		t.Fatalf("origin %s secure %v, want https", l.Origin(), l.Secure())
	}
	if got := served(t, l); got != 1 {
		t.Fatalf("first handshake serial %d, want 1", got)
	}
	c2, k2 := pair(t, 2)
	replace(t, certFile, keyFile, c2, k2, now.Add(time.Minute))
	if got := served(t, l); got != 2 {
		t.Errorf("after replacement serial %d, want 2", got)
	}
	replace(t, certFile, keyFile, []byte("not pem"), k2, now.Add(2*time.Minute))
	if got := served(t, l); got != 2 {
		t.Errorf("after a bad replacement serial %d, want the previous pair (2)", got)
	}
}

// An unreadable, empty, non-PEM or mismatched pair fails the component; nothing listens.
//
//dsh:test DH-T-HOST-2
func TestTLSBadMaterialFailsStart(t *testing.T) {
	dir := t.TempDir()
	c1, k1 := pair(t, 1)
	_, k2 := pair(t, 2)
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cert, key := write("c1.pem", c1), write("k1.pem", k1)
	cases := map[string][2]string{
		"unreadable": {filepath.Join(dir, "missing.pem"), key},
		"empty":      {write("empty.pem", nil), key},
		"non-PEM":    {write("junk.pem", []byte("junk")), key},
		"mismatched": {cert, write("k2.pem", k2)},
		"half":       {cert, ""},
	}
	for name, files := range cases {
		_, err := start(t, cordisRoot(t), listen.Config{Address: "127.0.0.1:0", CertFile: files[0], KeyFile: files[1]})
		if !herr.Has(err, listen.ReasonTLS) {
			t.Errorf("%s: err = %v, want %s", name, err, listen.ReasonTLS)
		}
	}
}
