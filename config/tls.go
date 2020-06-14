package config

import (
	"crypto/tls"
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
