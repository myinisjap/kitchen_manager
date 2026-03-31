package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"time"
)

// EnsureCert generates a self-signed TLS certificate at certFile/keyFile if
// either file is missing. The cert includes all local unicast IPs as SANs so
// Android can reach the server by IP without a hostname.
func EnsureCert(certFile, keyFile string) error {
	if fileExists(certFile) && fileExists(keyFile) {
		return nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	// Collect all local unicast IPs to include as SANs.
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.IsGlobalUnicast() {
				ips = append(ips, ip)
			}
		}
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kitchen-manager"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		IPAddresses:  ips,
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	cf, err := os.Create(certFile)
	if err != nil {
		return err
	}
	defer cf.Close()
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	kf, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	defer kf.Close()
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
