// Package secrets manages control-plane secrets that live outside the main
// config blob: currently the self-signed dashboard TLS certificate/key.
//
// Everything lives in a 0700 directory beside the database. The TLS key is
// 0600; the cert is 0644 and reused across restarts to avoid browser trust
// churn. Apps with more secret material (API keys, master keys, ...) should
// add it here rather than in the LMDB config.
package secrets

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
)

// Store owns the on-disk secret material. Safe for concurrent use.
type Store struct {
	dir string
}

// New opens (creating if needed) the secrets directory under storageDir and
// ensures a TLS certificate exists. commonName is baked into the generated
// cert (typically the app name).
func New(storageDir, commonName string) (*Store, error) {
	dir := filepath.Join(storageDir, "secrets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secrets dir: %w", err)
	}
	s := &Store{dir: dir}
	if err := s.ensureCert(commonName); err != nil {
		return nil, err
	}
	return s, nil
}

// CertPath and KeyPath return the dashboard TLS certificate/key file paths.
func (s *Store) CertPath() string { return filepath.Join(s.dir, certFile) }
func (s *Store) KeyPath() string  { return filepath.Join(s.dir, keyFile) }

func (s *Store) ensureCert(commonName string) error {
	certPath, keyPath := s.CertPath(), s.KeyPath()
	_, cerr := os.Stat(certPath)
	_, kerr := os.Stat(keyPath)
	if cerr == nil && kerr == nil {
		return nil
	}
	return generateSelfSigned(certPath, keyPath, commonName)
}

// generateSelfSigned writes a fresh self-signed ECDSA P-256 certificate/key
// pair with SANs for localhost, loopback, and the host's detected addresses.
func generateSelfSigned(certPath, keyPath, commonName string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate EC key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial: %w", err)
	}

	dnsNames, ips := detectSANs()
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeFileAtomic(certPath, certPEM, 0644); err != nil {
		return err
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("failed to marshal EC key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return writeFileAtomic(keyPath, keyPEM, 0600)
}

// detectSANs returns DNS names and IPs to bake into the dashboard cert: always
// localhost + loopback, plus the hostname and non-loopback interface addresses
// where practical.
func detectSANs() ([]string, []net.IP) {
	dnsNames := []string{"localhost"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}

	if host, err := os.Hostname(); err == nil && host != "" && host != "localhost" {
		dnsNames = append(dnsNames, host)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ipNet.IP)
		}
	}
	return dnsNames, ips
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to finalize %s: %w", path, err)
	}
	return nil
}
