package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"
)

// generateSelfSignedCert creates a self-signed certificate and key at the paths
// specified by config.CertFile and config.KeyFile. It is used for quick local TLS.
func generateSelfSignedCert() {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate private key: %v", err)
	}

	hostname, _ := os.Hostname()

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Advanced Echo Server"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", hostname},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	derCert, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		log.Fatalf("Failed to create certificate: %v", err)
	}

	certOut, err := os.OpenFile(config.CertFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatalf("Failed to open cert file: %v", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derCert}); err != nil {
		log.Fatalf("Failed to write cert PEM: %v", err)
	}

	keyOut, err := os.OpenFile(config.KeyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Failed to open key file: %v", err)
	}
	defer keyOut.Close()
	keyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes}); err != nil {
		log.Fatalf("Failed to write key PEM: %v", err)
	}

	log.Println("Successfully generated self-signed certificate and key.")
}

// startServer starts the provided HTTP server with TLS if enabled in config.
// It returns any error from ListenAndServe or ListenAndServeTLS.
func startServer(server *http.Server) error {
	if config.EnableTLS {
		if _, err := os.Stat(config.CertFile); os.IsNotExist(err) {
			log.Println("Certificate file not found. Generating a self-signed certificate...")
			generateSelfSignedCert()
		}
		log.Printf("Starting HTTPS server with cert: %s", config.CertFile)
		return server.ListenAndServeTLS(config.CertFile, config.KeyFile)
	}
	log.Printf("Starting HTTP server (with H2C support for non-WebSocket routes)")
	return server.ListenAndServe()
}
