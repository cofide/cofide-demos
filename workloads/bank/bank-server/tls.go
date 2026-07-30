package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"sync"
	"time"
)

// certReloader periodically re-reads a certificate/key pair from disk so
// cert-manager's certificate rotation is picked up without a pod restart.
type certReloader struct {
	certFile, keyFile string

	mu   sync.RWMutex
	cert *tls.Certificate
}

// newCertReloader loads the certificate/key pair once up front, so a
// misconfigured mount fails startup immediately rather than at first
// handshake.
func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}

	// tls.LoadX509KeyPair doesn't always populate cert.Leaf — parse it
	// explicitly so the log line below can report an expiry.
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}

	r.mu.Lock()
	r.cert = &cert
	r.mu.Unlock()

	if leaf != nil {
		slog.Info("Loaded TLS certificate", "cert_file", r.certFile, "not_after", leaf.NotAfter)
	} else {
		slog.Info("Loaded TLS certificate", "cert_file", r.certFile)
	}
	return nil
}

// GetCertificate implements tls.Config.GetCertificate.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// watch reloads the certificate from disk every interval until ctx is
// done. Polling, not fsnotify: kubelet's Secret-volume rotation is an
// atomic symlink swap, which silently invalidates a watch on the leaf file
// the moment cert-manager rotates it — watching the parent directory for
// CREATE events is more moving parts than a periodic reload buys back here.
func (r *certReloader) watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.reload(); err != nil {
				slog.Warn("Failed to reload TLS certificate, keeping previous one", "error", err)
			}
		}
	}
}
