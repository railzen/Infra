package netx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"sync"
	"time"
)

// localCA signs the certificates the proxy presents to the embedded browser.
// It lives only in memory for the lifetime of the process and is never written
// to any certificate store. The browser is told to accept exactly one public
// key (leafKey) via --ignore-certificate-errors-spki-list, so real upstream
// verification happens solely in the proxy.
type localCA struct {
	caCert  *x509.Certificate
	caKey   *ecdsa.PrivateKey
	leafKey *ecdsa.PrivateKey

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newLocalCA() (*localCA, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: "Intra Local Session CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &localCA{caCert: caCert, caKey: caKey, leafKey: leafKey, cache: map[string]*tls.Certificate{}}, nil
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

// SPKIHash is the base64 SHA-256 of the leaf public key, the format Chromium expects.
func (ca *localCA) SPKIHash() string {
	der, _ := x509.MarshalPKIXPublicKey(&ca.leafKey.PublicKey)
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (ca *localCA) certFor(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.cache[host]; ok && time.Until(c.Leaf.NotAfter) > time.Hour {
		return c, nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.caCert, &ca.leafKey.PublicKey, ca.caKey)
	if err != nil {
		return nil, err
	}
	leaf, _ := x509.ParseCertificate(der)
	c := &tls.Certificate{
		Certificate: [][]byte{der, ca.caCert.Raw},
		PrivateKey:  ca.leafKey,
		Leaf:        leaf,
	}
	ca.cache[host] = c
	return c, nil
}
