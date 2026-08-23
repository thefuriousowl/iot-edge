package publisher

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildClientTLSConfigSupportsSystemCAOverrideAndMTLS(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	caPEM, clientCertificatePEM, clientKeyPEM := generateSecretTestCertificates(t)
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{
		"tls.ca":     {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: caPEM}},
		"tls.client": {kind: SecretKindClientIdentity, material: SecretMaterial{CertificatePEM: clientCertificatePEM, PrivateKeyPEM: clientKeyPEM}},
	}}
	tlsConfig, err := BuildClientTLSConfig(context.Background(), publisherID, resolver, ClientTLSConfig{
		ServerName: "broker.example.test", CustomCA: &SecretReference{Name: "tls.ca"}, ClientIdentity: &SecretReference{Name: "tls.client"},
	}, "ignored.example.test:8883")
	if err != nil {
		t.Fatalf("BuildClientTLSConfig() error = %v", err)
	}
	if tlsConfig.ServerName != "broker.example.test" || tlsConfig.MinVersion != tls.VersionTLS12 || tlsConfig.InsecureSkipVerify || tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) != 1 || tlsConfig.Certificates[0].Leaf == nil {
		t.Fatalf("TLS config = %#v", tlsConfig)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d", resolver.calls)
	}

	systemConfig, err := BuildClientTLSConfig(context.Background(), publisherID, resolver, ClientTLSConfig{}, "broker.example.test:8883")
	if err != nil || systemConfig.ServerName != "broker.example.test" || systemConfig.RootCAs != nil || len(systemConfig.Certificates) != 0 || systemConfig.InsecureSkipVerify {
		t.Fatalf("system TLS config = %#v, %v", systemConfig, err)
	}
}

func TestBuildClientTLSConfigFailsClosed(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	resolver := &secretTestResolver{publisherID: publisherID, materials: map[string]secretTestResolved{
		"bad.ca": {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: []byte("invalid")}},
	}}
	for _, test := range []struct {
		name     string
		resolver SecretResolver
		config   ClientTLSConfig
		host     string
		want     error
	}{
		{name: "resolver", host: "broker.test", want: ErrSecretResolverRequired},
		{name: "host", resolver: resolver, want: ErrInvalidTLSConfig},
		{name: "URL host", resolver: resolver, host: "mqtts://broker.test", want: ErrInvalidTLSConfig},
		{name: "invalid reference", resolver: resolver, host: "broker.test", config: ClientTLSConfig{CustomCA: &SecretReference{Name: "Upper"}}, want: ErrInvalidTLSConfig},
		{name: "invalid CA", resolver: resolver, host: "broker.test", config: ClientTLSConfig{CustomCA: &SecretReference{Name: "bad.ca"}}, want: ErrInvalidTLSMaterial},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildClientTLSConfig(context.Background(), publisherID, test.resolver, test.config, test.host); !errors.Is(err, test.want) {
				t.Fatalf("BuildClientTLSConfig() error = %v, want %v", err, test.want)
			}
		})
	}
}

type secretTestResolved struct {
	kind     SecretKind
	material SecretMaterial
}

type secretTestResolver struct {
	publisherID uuid.UUID
	materials   map[string]secretTestResolved
	calls       int
}

func (resolver *secretTestResolver) Resolve(_ context.Context, publisherID uuid.UUID, reference SecretReference, expectedKind SecretKind) (SecretMaterial, SecretMetadata, error) {
	resolver.calls++
	if publisherID != resolver.publisherID {
		return SecretMaterial{}, SecretMetadata{}, ErrSecretNotFound
	}
	resolved, exists := resolver.materials[reference.Name]
	if !exists {
		return SecretMaterial{}, SecretMetadata{}, ErrSecretNotFound
	}
	if resolved.kind != expectedKind {
		return SecretMaterial{}, SecretMetadata{}, ErrSecretKindMismatch
	}
	return resolved.material.Clone(), SecretMetadata{PublisherID: publisherID, Reference: reference, Kind: resolved.kind, Revision: 1}, nil
}

func generateSecretTestCertificates(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	now := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Publisher Test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating client key: %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Publisher Test Client"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(0, 6, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating client certificate: %v", err)
	}
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshalling client key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER})
}
