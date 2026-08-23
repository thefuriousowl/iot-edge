package credential

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

func TestServiceManagesProfilesAndTypedSecretSlots(t *testing.T) {
	repository := &credentialRepositoryStub{}
	secrets := &credentialSecretsStub{}
	service, err := NewService(repository, secrets)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	description := "  Production broker  "
	profile, err := service.Create(context.Background(), CreateInput{Type: TypeMQTT, Name: "  Plant MQTT  ", Description: &description})
	if err != nil || profile.Name != "Plant MQTT" || profile.Description == nil || *profile.Description != "Production broker" {
		t.Fatalf("Create() = %#v, %v", profile, err)
	}

	material := publisher.SecretMaterial{Opaque: []byte("not-returned")}
	metadata, err := service.PutSecret(context.Background(), profile.ID, MQTTPasswordSlot, material)
	if err != nil || metadata.Kind != publisher.SecretKindOpaque || secrets.lastInput.Reference.Name != MQTTPasswordSlot {
		t.Fatalf("PutSecret() = %#v, %v", metadata, err)
	}
	if string(secrets.lastInput.Material.Opaque) != "not-returned" {
		t.Fatalf("secret material = %q", secrets.lastInput.Material.Opaque)
	}
	if _, err := service.PutSecret(context.Background(), profile.ID, "mqtt.unknown", material); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("PutSecret(unknown) error = %v", err)
	}
	if err := service.DeleteSecret(context.Background(), profile.ID, MQTTPasswordSlot); err != nil || secrets.deleted.Name != MQTTPasswordSlot {
		t.Fatalf("DeleteSecret() = %v, deleted %#v", err, secrets.deleted)
	}
}

func TestServiceManagesHTTPProfilesAndAllTypedSecretSlots(t *testing.T) {
	repository := &credentialRepositoryStub{}
	secrets := &credentialSecretsStub{}
	service, err := NewService(repository, secrets)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	profile, err := service.Create(context.Background(), CreateInput{Type: TypeHTTP, Name: "Plant HTTP"})
	if err != nil || profile.Type != TypeHTTP {
		t.Fatalf("Create(HTTP) = %#v, %v", profile, err)
	}
	for slot, wantKind := range map[string]publisher.SecretKind{
		HTTPUsernameSlot: publisher.SecretKindOpaque, HTTPPasswordSlot: publisher.SecretKindOpaque,
		HTTPAPIKeySlot: publisher.SecretKindOpaque, HTTPBearerTokenSlot: publisher.SecretKindOpaque,
		HTTPOAuthSecretSlot: publisher.SecretKindOpaque, HTTPCustomCASlot: publisher.SecretKindCACertificate,
		HTTPClientIdentitySlot: publisher.SecretKindClientIdentity,
	} {
		kind, err := ExpectedSecretKind(TypeHTTP, slot)
		if err != nil || kind != wantKind {
			t.Errorf("ExpectedSecretKind(%s) = %s, %v; want %s", slot, kind, err, wantKind)
		}
	}
	if _, err := service.PutSecret(context.Background(), profile.ID, HTTPAPIKeySlot, publisher.SecretMaterial{Opaque: []byte("write-only")}); err != nil {
		t.Fatalf("PutSecret(http.api_key) error = %v", err)
	}
	if secrets.lastInput.Reference.Name != HTTPAPIKeySlot || secrets.lastInput.Kind != publisher.SecretKindOpaque {
		t.Fatalf("HTTP secret input = %#v", secrets.lastInput)
	}
}

func TestServiceValidatesPublisherCompatibilityAndResolverOwnership(t *testing.T) {
	credentialID := uuid.New()
	publisherID := uuid.New()
	repository := &credentialRepositoryStub{profile: &Profile{ID: credentialID, Type: TypeMQTT, Name: "MQTT"}}
	service, _ := NewService(repository, &credentialSecretsStub{})
	if err := service.ValidateCredential(context.Background(), credentialID, publisher.TypeMQTT); err != nil {
		t.Fatalf("ValidateCredential(MQTT) error = %v", err)
	}
	if err := service.ValidateCredential(context.Background(), credentialID, publisher.TypeHTTPServer); !errors.Is(err, publisher.ErrCredentialIncompatible) {
		t.Errorf("ValidateCredential(HTTP) error = %v", err)
	}

	vault := &credentialVaultStub{material: publisher.SecretMaterial{Opaque: []byte("resolved")}}
	resolver, err := NewPublisherSecretResolver(&publisherStoreStub{entity: &publisher.Publisher{ID: publisherID, CredentialID: &credentialID}}, vault)
	if err != nil {
		t.Fatalf("NewPublisherSecretResolver() error = %v", err)
	}
	material, _, err := resolver.Resolve(context.Background(), publisherID, publisher.SecretReference{Name: MQTTUsernameSlot}, publisher.SecretKindOpaque)
	if err != nil || string(material.Opaque) != "resolved" || vault.ownerID != credentialID {
		t.Fatalf("Resolve() material=%q owner=%s error=%v", material.Opaque, vault.ownerID, err)
	}

	resolver, _ = NewPublisherSecretResolver(&publisherStoreStub{entity: &publisher.Publisher{ID: publisherID}}, vault)
	if _, _, err := resolver.Resolve(context.Background(), publisherID, publisher.SecretReference{Name: MQTTUsernameSlot}, publisher.SecretKindOpaque); !errors.Is(err, publisher.ErrSecretNotFound) {
		t.Errorf("Resolve(no credential) error = %v", err)
	}
}

func TestServiceValidatesHTTPProfileCompatibility(t *testing.T) {
	credentialID := uuid.New()
	repository := &credentialRepositoryStub{profile: &Profile{ID: credentialID, Type: TypeHTTP, Name: "HTTP"}}
	service, _ := NewService(repository, &credentialSecretsStub{})
	for _, publisherType := range []publisher.Type{publisher.TypeHTTPServer, publisher.TypeHTTPClient} {
		if err := service.ValidateCredential(context.Background(), credentialID, publisherType); err != nil {
			t.Errorf("ValidateCredential(%s) error = %v", publisherType, err)
		}
	}
	if err := service.ValidateCredential(context.Background(), credentialID, publisher.TypeMQTT); !errors.Is(err, publisher.ErrCredentialIncompatible) {
		t.Errorf("ValidateCredential(MQTT) error = %v", err)
	}
}

type credentialRepositoryStub struct{ profile *Profile }

func (repository *credentialRepositoryStub) Create(_ context.Context, profile *Profile) error {
	now := time.Now().UTC()
	profile.CreatedAt, profile.UpdatedAt = now, now
	repository.profile = profile
	return nil
}
func (repository *credentialRepositoryStub) Find(_ context.Context, id uuid.UUID) (*Profile, error) {
	if repository.profile == nil || repository.profile.ID != id {
		return nil, ErrNotFound
	}
	copy := *repository.profile
	return &copy, nil
}
func (repository *credentialRepositoryStub) List(context.Context, ListInput) ([]Profile, error) {
	if repository.profile == nil {
		return []Profile{}, nil
	}
	return []Profile{*repository.profile}, nil
}
func (repository *credentialRepositoryStub) Update(_ context.Context, profile *Profile) error {
	repository.profile = profile
	return nil
}
func (repository *credentialRepositoryStub) Delete(_ context.Context, id uuid.UUID) error {
	if repository.profile == nil || repository.profile.ID != id {
		return ErrNotFound
	}
	repository.profile = nil
	return nil
}

type credentialSecretsStub struct {
	metadata  []publisher.SecretMetadata
	lastInput publisher.PutSecretInput
	deleted   publisher.SecretReference
}

func (secrets *credentialSecretsStub) Put(_ context.Context, ownerID uuid.UUID, input publisher.PutSecretInput) (*publisher.SecretMetadata, error) {
	secrets.lastInput = input
	secrets.lastInput.Material = input.Material.Clone()
	metadata := publisher.SecretMetadata{PublisherID: ownerID, Reference: input.Reference, Kind: input.Kind, Revision: 1, PublisherRevision: 1, CreatedAt: time.Now().UTC(), RotatedAt: time.Now().UTC()}
	secrets.metadata = []publisher.SecretMetadata{metadata}
	return &metadata, nil
}
func (secrets *credentialSecretsStub) List(context.Context, uuid.UUID) ([]publisher.SecretMetadata, error) {
	return append([]publisher.SecretMetadata(nil), secrets.metadata...), nil
}
func (secrets *credentialSecretsStub) Delete(_ context.Context, _ uuid.UUID, reference publisher.SecretReference) error {
	secrets.deleted = reference
	secrets.metadata = nil
	return nil
}

type publisherStoreStub struct{ entity *publisher.Publisher }

func (store *publisherStoreStub) Find(context.Context, uuid.UUID) (*publisher.Publisher, error) {
	return store.entity, nil
}

type credentialVaultStub struct {
	ownerID  uuid.UUID
	material publisher.SecretMaterial
}

func (vault *credentialVaultStub) Resolve(_ context.Context, ownerID uuid.UUID, reference publisher.SecretReference, kind publisher.SecretKind) (publisher.SecretMaterial, publisher.SecretMetadata, error) {
	vault.ownerID = ownerID
	return vault.material.Clone(), publisher.SecretMetadata{PublisherID: ownerID, Reference: reference, Kind: kind}, nil
}
