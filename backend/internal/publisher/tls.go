package publisher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrSecretResolverRequired = errors.New("Data Publisher secret resolver is required")
	ErrInvalidTLSConfig       = errors.New("invalid Data Publisher TLS config")
	ErrInvalidTLSMaterial     = errors.New("invalid Data Publisher TLS certificate material")
)

type ClientTLSConfig struct {
	ServerName     string           `json:"server_name,omitempty"`
	CustomCA       *SecretReference `json:"custom_ca,omitempty"`
	ClientIdentity *SecretReference `json:"client_identity,omitempty"`
}

func BuildClientTLSConfig(ctx context.Context, publisherID uuid.UUID, resolver SecretResolver, config ClientTLSConfig, endpointHost string) (*tls.Config, error) {
	if ctx == nil || publisherID == uuid.Nil {
		return nil, ErrInvalidTLSConfig
	}
	if isNilSourceDependency(resolver) {
		return nil, ErrSecretResolverRequired
	}
	serverName, err := normalizeTLSServerName(config.ServerName, endpointHost)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if config.CustomCA != nil {
		if err := config.CustomCA.Validate(); err != nil {
			return nil, ErrInvalidTLSConfig
		}
		material, _, err := resolver.Resolve(ctx, publisherID, *config.CustomCA, SecretKindCACertificate)
		if err != nil {
			return nil, err
		}
		defer material.Destroy()
		if validateCAPEM(material.CertificatePEM) != nil {
			return nil, ErrInvalidTLSMaterial
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(material.CertificatePEM) {
			return nil, ErrInvalidTLSMaterial
		}
		tlsConfig.RootCAs = roots
	}
	if config.ClientIdentity != nil {
		if err := config.ClientIdentity.Validate(); err != nil {
			return nil, ErrInvalidTLSConfig
		}
		material, _, err := resolver.Resolve(ctx, publisherID, *config.ClientIdentity, SecretKindClientIdentity)
		if err != nil {
			return nil, err
		}
		defer material.Destroy()
		identity, err := tls.X509KeyPair(material.CertificatePEM, material.PrivateKeyPEM)
		if err != nil || len(identity.Certificate) == 0 {
			return nil, ErrInvalidTLSMaterial
		}
		identity.Leaf, err = x509.ParseCertificate(identity.Certificate[0])
		if err != nil {
			return nil, ErrInvalidTLSMaterial
		}
		tlsConfig.Certificates = []tls.Certificate{identity}
	}
	return tlsConfig, nil
}

func normalizeTLSServerName(explicit, endpointHost string) (string, error) {
	serverName := strings.TrimSpace(explicit)
	if serverName == "" {
		serverName = strings.TrimSpace(endpointHost)
		if strings.Contains(serverName, "://") {
			return "", ErrInvalidTLSConfig
		}
		if host, _, err := net.SplitHostPort(serverName); err == nil {
			serverName = host
		}
	}
	serverName = strings.Trim(serverName, "[]")
	if serverName == "" || len(serverName) > 253 || strings.ContainsAny(serverName, "/\\@ \t\r\n") || strings.Contains(serverName, "://") {
		return "", ErrInvalidTLSConfig
	}
	return serverName, nil
}
