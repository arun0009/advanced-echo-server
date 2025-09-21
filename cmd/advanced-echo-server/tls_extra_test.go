package main

import (
	"encoding/pem"
	"os"
	"testing"
)

func TestGenerateSelfSignedCert_Extra(t *testing.T) {
	setupTest()
	// Use temp files
	certFile := "test_server.crt"
	keyFile := "test_server.key"
	defer os.Remove(certFile)
	defer os.Remove(keyFile)

	configLock.Lock()
	config.CertFile = certFile
	config.KeyFile = keyFile
	configLock.Unlock()

	generateSelfSignedCert()
	// Validate files exist and are PEM
	cf, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("cert not created: %v", err)
	}
	if p, _ := pem.Decode(cf); p == nil || p.Type != "CERTIFICATE" {
		t.Errorf("invalid cert PEM")
	}
	kf, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("key not created: %v", err)
	}
	if p, _ := pem.Decode(kf); p == nil || p.Type != "RSA PRIVATE KEY" {
		t.Errorf("invalid key PEM")
	}
}
