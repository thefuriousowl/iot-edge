package publisher

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxSecretNameLength       = 64
	maxOpaqueSecretBytes      = 16 * 1024
	maxCertificateSecretBytes = 128 * 1024
	maxPrivateKeySecretBytes  = 128 * 1024
)

var (
	ErrSecretRepositoryRequired = errors.New("Data Publisher secret repository is required")
	ErrSecretCipherRequired     = errors.New("Data Publisher secret cipher is required")
	ErrSecretRotationRequired   = errors.New("Data Publisher secret rotation publisher is required")
	ErrSecretNotFound           = errors.New("Data Publisher secret not found")
	ErrSecretKindMismatch       = errors.New("Data Publisher secret kind cannot be changed by rotation")
	ErrInvalidSecretReference   = errors.New("invalid Data Publisher secret reference")
	ErrInvalidSecretMaterial    = errors.New("invalid Data Publisher secret material")
	ErrSecretEncryption         = errors.New("Data Publisher secret could not be encrypted")
	ErrSecretDecryption         = errors.New("Data Publisher secret could not be decrypted")
)

var secretNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

type SecretKind string

const (
	SecretKindOpaque         SecretKind = "opaque"
	SecretKindCACertificate  SecretKind = "ca_certificate"
	SecretKindClientIdentity SecretKind = "client_identity"
)

type SecretReference struct {
	Name string `json:"name"`
}

func (reference SecretReference) Validate() error {
	if len(reference.Name) > maxSecretNameLength || reference.Name != strings.TrimSpace(reference.Name) || !secretNamePattern.MatchString(reference.Name) {
		return ErrInvalidSecretReference
	}
	return nil
}

func (reference SecretReference) MarshalJSON() ([]byte, error) {
	if err := reference.Validate(); err != nil {
		return nil, err
	}
	type referenceJSON SecretReference
	return json.Marshal(referenceJSON(reference))
}

func (reference *SecretReference) UnmarshalJSON(data []byte) error {
	if reference == nil {
		return ErrInvalidSecretReference
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded struct {
		Name *string `json:"name"`
	}
	if err := decoder.Decode(&decoded); err != nil {
		return ErrInvalidSecretReference
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || decoded.Name == nil {
		return ErrInvalidSecretReference
	}
	candidate := SecretReference{Name: *decoded.Name}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*reference = candidate
	return nil
}

type SecretMaterial struct {
	Opaque         []byte `json:"-"`
	CertificatePEM []byte `json:"-"`
	PrivateKeyPEM  []byte `json:"-"`
}

func (material SecretMaterial) Clone() SecretMaterial {
	return SecretMaterial{
		Opaque: append([]byte(nil), material.Opaque...), CertificatePEM: append([]byte(nil), material.CertificatePEM...),
		PrivateKeyPEM: append([]byte(nil), material.PrivateKeyPEM...),
	}
}

func (material *SecretMaterial) Destroy() {
	if material == nil {
		return
	}
	zeroBytes(material.Opaque)
	zeroBytes(material.CertificatePEM)
	zeroBytes(material.PrivateKeyPEM)
	material.Opaque = nil
	material.CertificatePEM = nil
	material.PrivateKeyPEM = nil
}

type SecretMetadata struct {
	PublisherID       uuid.UUID       `json:"publisher_id"`
	Reference         SecretReference `json:"reference"`
	Kind              SecretKind      `json:"kind"`
	Revision          uint64          `json:"revision"`
	PublisherRevision uint64          `json:"publisher_revision"`
	CreatedAt         time.Time       `json:"created_at"`
	RotatedAt         time.Time       `json:"rotated_at"`
}

type EncryptedSecret struct {
	ID         uuid.UUID      `json:"-"`
	Metadata   SecretMetadata `json:"-"`
	KeyID      string         `json:"-"`
	Ciphertext []byte         `json:"-"`
}

func cloneEncryptedSecret(secret EncryptedSecret) EncryptedSecret {
	secret.Ciphertext = append([]byte(nil), secret.Ciphertext...)
	return secret
}

type SecretRepository interface {
	Upsert(context.Context, *EncryptedSecret) (*SecretMetadata, error)
	FindEncrypted(context.Context, uuid.UUID, SecretReference) (*EncryptedSecret, error)
	ListMetadata(context.Context, uuid.UUID) ([]SecretMetadata, error)
	Delete(context.Context, uuid.UUID, SecretReference) (*SecretMetadata, error)
}

type SecretCipher interface {
	Seal(context.Context, []byte, []byte) (string, []byte, error)
	Open(context.Context, string, []byte, []byte) ([]byte, error)
}

type SecretResolver interface {
	Resolve(context.Context, uuid.UUID, SecretReference, SecretKind) (SecretMaterial, SecretMetadata, error)
}

type PutSecretInput struct {
	Reference SecretReference
	Kind      SecretKind
	Material  SecretMaterial
}

type SecretService struct {
	repository SecretRepository
	cipher     SecretCipher
	rotations  SecretRotationPublisher
}

func NewSecretService(repository SecretRepository, cipher SecretCipher, rotations SecretRotationPublisher) (*SecretService, error) {
	if isNilSourceDependency(repository) {
		return nil, ErrSecretRepositoryRequired
	}
	if isNilSourceDependency(cipher) {
		return nil, ErrSecretCipherRequired
	}
	if isNilSourceDependency(rotations) {
		return nil, ErrSecretRotationRequired
	}
	return &SecretService{repository: repository, cipher: cipher, rotations: rotations}, nil
}

func (service *SecretService) Put(ctx context.Context, publisherID uuid.UUID, input PutSecretInput) (*SecretMetadata, error) {
	if service == nil || ctx == nil || publisherID == uuid.Nil || input.Reference.Validate() != nil {
		return nil, ErrInvalidInput
	}
	material := input.Material.Clone()
	defer material.Destroy()
	if err := validateSecretMaterial(input.Kind, material); err != nil {
		return nil, err
	}
	payload, err := marshalSecretMaterial(input.Kind, material)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(payload)
	keyID, ciphertext, err := service.cipher.Seal(ctx, secretAdditionalData(publisherID, input.Reference, input.Kind), payload)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrSecretEncryption
	}
	if !validSecretKeyID(keyID) || len(ciphertext) == 0 {
		return nil, ErrSecretEncryption
	}
	stored := &EncryptedSecret{Metadata: SecretMetadata{PublisherID: publisherID, Reference: input.Reference, Kind: input.Kind}, KeyID: keyID, Ciphertext: ciphertext}
	metadata, err := service.repository.Upsert(ctx, stored)
	if err != nil {
		return nil, err
	}
	if !validSecretMetadata(metadata, publisherID, input.Reference, input.Kind) {
		return nil, ErrInvalidPublisher
	}
	publishSecretRotation(service.rotations, SecretRotationEvent{
		PublisherID: publisherID, Reference: input.Reference, Kind: input.Kind, Revision: metadata.Revision,
		PublisherRevision: metadata.PublisherRevision, Action: SecretRotationUpserted, RotatedAt: metadata.RotatedAt,
	})
	cloned := *metadata
	return &cloned, nil
}

func (service *SecretService) List(ctx context.Context, publisherID uuid.UUID) ([]SecretMetadata, error) {
	if service == nil || ctx == nil || publisherID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	metadata, err := service.repository.ListMetadata(ctx, publisherID)
	if err != nil {
		return nil, err
	}
	for index := range metadata {
		if !validSecretMetadata(&metadata[index], publisherID, metadata[index].Reference, metadata[index].Kind) {
			return nil, ErrInvalidPublisher
		}
	}
	return append([]SecretMetadata(nil), metadata...), nil
}

func (service *SecretService) Delete(ctx context.Context, publisherID uuid.UUID, reference SecretReference) error {
	if service == nil || ctx == nil || publisherID == uuid.Nil || reference.Validate() != nil {
		return ErrInvalidInput
	}
	metadata, err := service.repository.Delete(ctx, publisherID, reference)
	if err != nil {
		return err
	}
	if !validSecretMetadata(metadata, publisherID, reference, metadata.Kind) {
		return ErrInvalidPublisher
	}
	publishSecretRotation(service.rotations, SecretRotationEvent{
		PublisherID: publisherID, Reference: reference, Kind: metadata.Kind, Revision: metadata.Revision,
		PublisherRevision: metadata.PublisherRevision, Action: SecretRotationDeleted, RotatedAt: metadata.RotatedAt,
	})
	return nil
}

type SecretVault struct {
	repository SecretRepository
	cipher     SecretCipher
}

func NewSecretVault(repository SecretRepository, cipher SecretCipher) (*SecretVault, error) {
	if isNilSourceDependency(repository) {
		return nil, ErrSecretRepositoryRequired
	}
	if isNilSourceDependency(cipher) {
		return nil, ErrSecretCipherRequired
	}
	return &SecretVault{repository: repository, cipher: cipher}, nil
}

func (vault *SecretVault) Resolve(ctx context.Context, publisherID uuid.UUID, reference SecretReference, expectedKind SecretKind) (SecretMaterial, SecretMetadata, error) {
	if vault == nil || ctx == nil || publisherID == uuid.Nil || reference.Validate() != nil || !validSecretKind(expectedKind) {
		return SecretMaterial{}, SecretMetadata{}, ErrInvalidInput
	}
	stored, err := vault.repository.FindEncrypted(ctx, publisherID, reference)
	if err != nil {
		return SecretMaterial{}, SecretMetadata{}, err
	}
	if stored.Metadata.Kind != expectedKind {
		return SecretMaterial{}, SecretMetadata{}, ErrSecretKindMismatch
	}
	payload, err := vault.cipher.Open(ctx, stored.KeyID, secretAdditionalData(publisherID, reference, expectedKind), stored.Ciphertext)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return SecretMaterial{}, SecretMetadata{}, err
		}
		return SecretMaterial{}, SecretMetadata{}, ErrSecretDecryption
	}
	defer zeroBytes(payload)
	material, err := unmarshalSecretMaterial(expectedKind, payload)
	if err != nil || validateSecretMaterial(expectedKind, material) != nil {
		material.Destroy()
		return SecretMaterial{}, SecretMetadata{}, ErrSecretDecryption
	}
	return material, stored.Metadata, nil
}

func validateSecretMaterial(kind SecretKind, material SecretMaterial) error {
	switch kind {
	case SecretKindOpaque:
		if len(material.Opaque) == 0 || len(material.Opaque) > maxOpaqueSecretBytes || len(material.CertificatePEM) != 0 || len(material.PrivateKeyPEM) != 0 {
			return ErrInvalidSecretMaterial
		}
	case SecretKindCACertificate:
		if len(material.Opaque) != 0 || len(material.CertificatePEM) == 0 || len(material.CertificatePEM) > maxCertificateSecretBytes || len(material.PrivateKeyPEM) != 0 {
			return ErrInvalidSecretMaterial
		}
		if err := validateCAPEM(material.CertificatePEM); err != nil {
			return ErrInvalidSecretMaterial
		}
	case SecretKindClientIdentity:
		if len(material.Opaque) != 0 || len(material.CertificatePEM) == 0 || len(material.CertificatePEM) > maxCertificateSecretBytes || len(material.PrivateKeyPEM) == 0 || len(material.PrivateKeyPEM) > maxPrivateKeySecretBytes {
			return ErrInvalidSecretMaterial
		}
		if _, err := tls.X509KeyPair(material.CertificatePEM, material.PrivateKeyPEM); err != nil {
			return ErrInvalidSecretMaterial
		}
	default:
		return ErrInvalidSecretMaterial
	}
	return nil
}

func validSecretKind(kind SecretKind) bool {
	return kind == SecretKindOpaque || kind == SecretKindCACertificate || kind == SecretKindClientIdentity
}

func validSecretMetadata(metadata *SecretMetadata, publisherID uuid.UUID, reference SecretReference, kind SecretKind) bool {
	return metadata != nil && metadata.PublisherID == publisherID && metadata.Reference == reference && reference.Validate() == nil && metadata.Kind == kind &&
		validSecretKind(kind) && metadata.Revision > 0 && metadata.PublisherRevision > 0 && !metadata.CreatedAt.IsZero() && !metadata.RotatedAt.IsZero() && !metadata.RotatedAt.Before(metadata.CreatedAt)
}

func validateCAPEM(contents []byte) error {
	rest := contents
	count := 0
	for len(bytes.TrimSpace(rest)) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return ErrInvalidSecretMaterial
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.BasicConstraintsValid || !certificate.IsCA {
			return ErrInvalidSecretMaterial
		}
		count++
		rest = remaining
	}
	if count == 0 {
		return ErrInvalidSecretMaterial
	}
	return nil
}

func marshalSecretMaterial(kind SecretKind, material SecretMaterial) ([]byte, error) {
	switch kind {
	case SecretKindOpaque:
		return append([]byte(nil), material.Opaque...), nil
	case SecretKindCACertificate:
		return append([]byte(nil), material.CertificatePEM...), nil
	case SecretKindClientIdentity:
		payload := make([]byte, 4+len(material.CertificatePEM)+len(material.PrivateKeyPEM))
		binary.BigEndian.PutUint32(payload, uint32(len(material.CertificatePEM)))
		copy(payload[4:], material.CertificatePEM)
		copy(payload[4+len(material.CertificatePEM):], material.PrivateKeyPEM)
		return payload, nil
	default:
		return nil, ErrInvalidSecretMaterial
	}
}

func unmarshalSecretMaterial(kind SecretKind, payload []byte) (SecretMaterial, error) {
	switch kind {
	case SecretKindOpaque:
		return SecretMaterial{Opaque: append([]byte(nil), payload...)}, nil
	case SecretKindCACertificate:
		return SecretMaterial{CertificatePEM: append([]byte(nil), payload...)}, nil
	case SecretKindClientIdentity:
		if len(payload) < 4 {
			return SecretMaterial{}, ErrSecretDecryption
		}
		certificateLength := int(binary.BigEndian.Uint32(payload[:4]))
		if certificateLength < 1 || certificateLength > len(payload)-4 {
			return SecretMaterial{}, ErrSecretDecryption
		}
		return SecretMaterial{
			CertificatePEM: append([]byte(nil), payload[4:4+certificateLength]...),
			PrivateKeyPEM:  append([]byte(nil), payload[4+certificateLength:]...),
		}, nil
	default:
		return SecretMaterial{}, ErrSecretDecryption
	}
}

func secretAdditionalData(publisherID uuid.UUID, reference SecretReference, kind SecretKind) []byte {
	return []byte(fmt.Sprintf("iot-edge:publisher-secret:v1:%s:%s:%s", publisherID, reference.Name, kind))
}

func zeroBytes(contents []byte) {
	for index := range contents {
		contents[index] = 0
	}
}
