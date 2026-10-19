package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Easy SSL: Omni Web Server keeps a local certificate authority (created once) and
// issues a certificate per site covering "site.tld" and "*.site.tld". Once the
// CA is trusted (one click on Windows), every https://*.local works without
// browser warnings.

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 825 * 24 * time.Hour
	renewBefore  = 30 * 24 * time.Hour
)

func (a *App) caCertPath() string   { return a.path("etc", "ssl", "omni-ca.crt") }
func (a *App) caKeyPath() string    { return a.path("etc", "ssl", "omni-ca.key") }
func (a *App) caBundlePath() string { return a.path("etc", "ssl", "bundle.pem") }

func (a *App) siteCertPaths(host string) (string, string) {
	dir := a.path("etc", "ssl", "sites")
	return filepath.Join(dir, host+".crt"), filepath.Join(dir, host+".key")
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: typ, Bytes: der}); err != nil {
		return err
	}
	if err := writeFileAtomic(path, buf.Bytes()); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func readPEM(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM data", path)
	}
	return blk.Bytes, nil
}

// loadCA returns the local CA, creating it on first use.
func (a *App) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certDER, err1 := readPEM(a.caCertPath())
	keyDER, err2 := readPEM(a.caKeyPath())
	if err1 == nil && err2 == nil {
		cert, err := x509.ParseCertificate(certDER)
		if err != nil {
			return nil, nil, err
		}
		key, err := x509.ParseECPrivateKey(keyDER)
		if err != nil {
			return nil, nil, err
		}
		if time.Now().Before(cert.NotAfter.Add(-renewBefore)) {
			return cert, key, nil
		}
		a.log.Printf("local CA expires soon, creating a new one")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Omni Web Server Local CA (" + host + ")", Organization: []string{"Omni Web Server local development"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(a.caKeyPath(), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return nil, nil, err
	}
	if err := writePEM(a.caCertPath(), "CERTIFICATE", der, 0o644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// ensureSiteCert issues (or renews) the certificate for host and *.host.
func (a *App) ensureSiteCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string, ips []net.IP) (string, string, error) {
	crtPath, keyPath := a.siteCertPaths(host)
	names := []string{host, "*." + host}
	if der, err := readPEM(crtPath); err == nil && exists(keyPath) {
		if cert, err := x509.ParseCertificate(der); err == nil &&
			cert.CheckSignatureFrom(ca) == nil &&
			time.Now().Before(cert.NotAfter.Add(-renewBefore)) &&
			sameNames(cert.DNSNames, names) && len(cert.IPAddresses) == len(ips) {
			return crtPath, keyPath, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Omni Web Server local development"}},
		DNSNames:     names,
		IPAddresses:  ips,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return "", "", err
	}
	if err := writePEM(crtPath, "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	return crtPath, keyPath, nil
}

// prepareSSL makes sure the CA and every enabled site's certificate exist and
// returns host -> [cert, key]. It also refreshes the CA bundle PHP uses, so
// curl/file_get_contents to https://*.local verify too.
func (a *App) prepareSSL(cfg *Config, sites []Site) (map[string][2]string, error) {
	ca, caKey, err := a.loadCA()
	if err != nil {
		return nil, fmt.Errorf("creating the local CA: %v", err)
	}
	a.writeCABundle()
	certs := map[string][2]string{}
	crt, key, err := a.ensureSiteCert(ca, caKey, "localhost", []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback})
	if err != nil {
		return nil, err
	}
	certs["localhost"] = [2]string{crt, key}
	for _, s := range sites {
		if !s.Enabled || s.Missing {
			continue
		}
		crt, key, err := a.ensureSiteCert(ca, caKey, s.Host, nil)
		if err != nil {
			return nil, fmt.Errorf("certificate for %s: %v", s.Host, err)
		}
		certs[s.Host] = [2]string{crt, key}
	}
	return certs, nil
}

// writeCABundle combines Mozilla's bundle (if downloaded) with the local CA.
func (a *App) writeCABundle() {
	var buf bytes.Buffer
	if b, err := os.ReadFile(a.path("etc", "ssl", "cacert.pem")); err == nil {
		buf.Write(b)
		buf.WriteString("\n")
	}
	if b, err := os.ReadFile(a.caCertPath()); err == nil {
		buf.WriteString("Omni Web Server Local CA\n=================\n")
		buf.Write(b)
	}
	if buf.Len() > 0 {
		_, _ = writeIfChanged(a.caBundlePath(), buf.Bytes())
	}
}

// caThumbprint is the SHA-1 fingerprint Windows uses to identify certificates.
func (a *App) caThumbprint() string {
	der, err := readPEM(a.caCertPath())
	if err != nil {
		return ""
	}
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

type sslStatus struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"` // Apache has mod_ssl
	CA        bool   `json:"ca"`
	Trusted   bool   `json:"trusted"`
	CAPath    string `json:"ca_path,omitempty"`
	Expires   string `json:"expires,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (a *App) sslStatus(cfg *Config) sslStatus {
	st := sslStatus{Enabled: cfg.SSL, Available: a.apacheHasSSL(cfg), CAPath: a.caCertPath()}
	if der, err := readPEM(a.caCertPath()); err == nil {
		if cert, err := x509.ParseCertificate(der); err == nil {
			st.CA = true
			st.Expires = cert.NotAfter.Format("2006-01-02")
			st.Trusted = a.cachedVersion("ca-trust:"+a.caThumbprint(), func() string {
				if caTrusted(a.caThumbprint(), a.caCertPath()) {
					return "yes"
				}
				return ""
			}) == "yes"
		}
	}
	a.mu.Lock()
	st.Error = a.sslErr
	a.mu.Unlock()
	return st
}

// TrustCA adds the local CA to the OS trust store.
func (a *App) TrustCA() error {
	if _, _, err := a.loadCA(); err != nil {
		return err
	}
	if err := trustCA(a.caCertPath()); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.versions, "ca-trust:"+a.caThumbprint())
	a.mu.Unlock()
	if !caTrusted(a.caThumbprint(), a.caCertPath()) {
		return errors.New("the certificate was not added (the confirmation dialog was declined?)")
	}
	return nil
}

func (a *App) apacheHasSSL(cfg *Config) bool {
	bin := a.apacheBin(cfg)
	if bin == "" {
		return false
	}
	return a.apacheStaticModules(bin)["ssl"] || exists(filepath.Join(a.apacheModulesDir(cfg), "mod_ssl.so"))
}

// caTrusted reports whether the OS trust store accepts the local CA.
func caTrusted(thumb, path string) bool {
	der, err := readPEM(path)
	if err != nil {
		return false
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return false
	}
	if _, err := cert.Verify(x509.VerifyOptions{}); err == nil {
		return true
	}
	return caInstalled(thumb, path)
}
