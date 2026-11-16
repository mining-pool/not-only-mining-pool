package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
)

type TLSClientOptions struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

func (to *TLSClientOptions) ToTLSConfig() *tls.Config {
	certs := make([]tls.Certificate, 0)
	if len(to.CertFile) > 0 && len(to.KeyFile) > 0 {
		cert, err := tls.LoadX509KeyPair(to.CertFile, to.KeyFile)
		if err != nil {
			log.Panic(err)
		}
		certs = append(certs, cert)
	}

	return &tls.Config{
		Certificates:       certs,
		InsecureSkipVerify: true,
	}
}

type TLSServerOptions struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

func (to *TLSServerOptions) ToTLSConfig() *tls.Config {
	certs := make([]tls.Certificate, 0)
	if len(to.CertFile) > 0 && len(to.KeyFile) > 0 {
		cert, err := tls.LoadX509KeyPair(to.CertFile, to.KeyFile)
		if err != nil {
			log.Fatal(err)
		}
		certs = append(certs, cert)
	}

	return &tls.Config{
		Certificates: certs,
	}
}

// LoadCertificate reads the configured certificate and key, reporting errors
// instead of exiting so a configuration reload can be rejected cleanly.
func (to *TLSServerOptions) LoadCertificate() (*tls.Certificate, error) {
	if to == nil || to.CertFile == "" || to.KeyFile == "" {
		return nil, errors.New("tls requires certFile and keyFile")
	}
	cert, err := tls.LoadX509KeyPair(to.CertFile, to.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tls certificate %s: %w", to.CertFile, err)
	}
	return &cert, nil
}

// CertificateHolder serves a certificate that can be replaced while a listener
// keeps running, so renewed certificates apply to new handshakes without
// dropping connected clients.
type CertificateHolder struct {
	cert atomic.Pointer[tls.Certificate]
}

// NewCertificateHolder loads the initial certificate.
func NewCertificateHolder(to *TLSServerOptions) (*CertificateHolder, error) {
	cert, err := to.LoadCertificate()
	if err != nil {
		return nil, err
	}
	h := &CertificateHolder{}
	h.cert.Store(cert)
	return h, nil
}

// Set replaces the served certificate.
func (h *CertificateHolder) Set(cert *tls.Certificate) { h.cert.Store(cert) }

// GetCertificate implements tls.Config.GetCertificate.
func (h *CertificateHolder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return h.cert.Load(), nil
}
