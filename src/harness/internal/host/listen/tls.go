package listen

import (
	"crypto/tls"
	"os"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// stamp identifies one version of a file by its modification time and size.
type stamp struct {
	mod  time.Time
	size int64
}

func stampOf(path string) (stamp, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}, err
	}
	return stamp{fi.ModTime(), fi.Size()}, nil
}

// certSource serves a certificate pair that is re-read when either file's stamp
// changes. A replaced pair that does not load keeps the last good one in service.
type certSource struct {
	certFile, keyFile string
	warn              func(error)

	mu      sync.Mutex
	stamps  [2]stamp
	current *tls.Certificate
	statErr string // the last stat failure reported, so it is warned once
}

// newCertSource loads the pair once; bad material fails here, before anything listens.
func newCertSource(certFile, keyFile string, warn func(error)) (*certSource, error) {
	c := &certSource{certFile: certFile, keyFile: keyFile, warn: warn}
	stamps, err := c.stat()
	if err != nil {
		return nil, herr.Wrap(ReasonTLS, err, "certificate pair")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, herr.Wrap(ReasonTLS, err, "certificate pair")
	}
	c.stamps, c.current = stamps, &cert
	return c, nil
}

func (c *certSource) stat() ([2]stamp, error) {
	cs, err := stampOf(c.certFile)
	if err != nil {
		return [2]stamp{}, err
	}
	ks, err := stampOf(c.keyFile)
	if err != nil {
		return [2]stamp{}, err
	}
	return [2]stamp{cs, ks}, nil
}

// getCertificate is tls.Config.GetCertificate: each handshake checks the stamps, so
// the next connection after a replacement presents the new pair without a restart.
func (c *certSource) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamps, err := c.stat()
	if err != nil {
		// A missing or unreadable file (a renewal mid-way, a removed pair) keeps the last
		// good pair in service. It is reported once per distinct failure, not per handshake.
		if msg := err.Error(); msg != c.statErr {
			c.statErr = msg
			c.warn(herr.Wrap(ReasonTLS, err, "certificate pair not readable; keeping the previous one"))
		}
		return c.current, nil
	}
	c.statErr = ""
	if stamps == c.stamps {
		return c.current, nil
	}
	// Record the stamps whether or not the pair loads: a half-written pair is retried
	// when the other file changes, not on every handshake.
	c.stamps = stamps
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		c.warn(herr.Wrap(ReasonTLS, err, "replaced certificate pair not loaded; keeping the previous one"))
		return c.current, nil
	}
	c.current = &cert
	return c.current, nil
}
