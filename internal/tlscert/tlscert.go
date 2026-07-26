// Package tlscert generates and loads the appliance's self-signed TLS
// certificate. Browsers will warn once (self-signed) — that is expected for
// LAN appliances; a device-CA install flow is a later milestone.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Ensure returns cert/key paths under dataDir/tls, generating a self-signed
// certificate on first use. hosts lists extra SANs (DNS names or IPs).
func Ensure(dataDir string, hosts []string) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, "tls")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if _, err1 := os.Stat(certPath); err1 == nil {
		if _, err2 := os.Stat(keyPath); err2 == nil {
			return certPath, keyPath, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "NodeOS", Organization: []string{"NodeOS"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	seen := map[string]bool{}
	for _, h := range defaultSANs(hosts) {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// defaultSANs augments the caller's hosts with hostname, hostname.local,
// loopback and all private interface IPs.
func defaultSANs(hosts []string) []string {
	out := append([]string{}, hosts...)
	if hn, err := os.Hostname(); err == nil && hn != "" {
		out = append(out, hn, hn+".local")
	}
	out = append(out, "localhost", "127.0.0.1", "::1")
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok {
					if ip4 := ipnet.IP.To4(); ip4 != nil && ip4.IsPrivate() {
						out = append(out, ip4.String())
					}
				}
			}
		}
	}
	return out
}

// Config builds a *tls.Config from the given (or generated) material.
func Config(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
