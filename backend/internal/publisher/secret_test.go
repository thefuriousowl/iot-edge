package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSecretReferenceUsesStrictWriteOnlyJSON(t *testing.T) {
	t.Parallel()

	reference := SecretReference{Name: "mqtt.password"}
	encoded, err := json.Marshal(reference)
	if err != nil || string(encoded) != `{"name":"mqtt.password"}` {
		t.Fatalf("Marshal() = %s, %v", encoded, err)
	}
	var decoded SecretReference
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != reference {
		t.Fatalf("Unmarshal() = %#v, %v", decoded, err)
	}
	for _, encoded := range []string{`{}`, `null`, `{"name":" MQTT "}`, `{"name":"Upper"}`, `{"name":"valid","future":true}`, `{"name":"valid"} {}`} {
		if err := json.Unmarshal([]byte(encoded), &decoded); err == nil {
			t.Errorf("Unmarshal(%s) error = %v", encoded, err)
		}
	}
	materialJSON, err := json.Marshal(SecretMaterial{Opaque: []byte{1, 2, 3}, PrivateKeyPEM: []byte{4, 5, 6}})
	if err != nil || string(materialJSON) != `{}` {
		t.Fatalf("SecretMaterial JSON = %s, %v", materialJSON, err)
	}
	encryptedJSON, err := json.Marshal(EncryptedSecret{KeyID: "key", Ciphertext: []byte{1, 2, 3}})
	if err != nil || string(encryptedJSON) != `{}` {
		t.Fatalf("EncryptedSecret JSON = %s, %v", encryptedJSON, err)
	}
}

func TestAESGCMSecretCipherBindsCiphertextToOwnerReferenceAndKind(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x2a}, 32)
	secretCipher, err := NewAESGCMSecretCipher("publisher-master-v1", key)
	if err != nil {
		t.Fatalf("NewAESGCMSecretCipher() error = %v", err)
	}
	plaintext := bytes.Repeat([]byte{0x5a}, 48)
	additionalData := secretAdditionalData(uuid.New(), SecretReference{Name: "mqtt.password"}, SecretKindOpaque)
	keyID, first, err := secretCipher.Seal(context.Background(), additionalData, plaintext)
	if err != nil || keyID != "publisher-master-v1" || bytes.Contains(first, plaintext) {
		t.Fatalf("Seal() = %q, %x, %v", keyID, first, err)
	}
	_, second, _ := secretCipher.Seal(context.Background(), additionalData, plaintext)
	if bytes.Equal(first, second) {
		t.Fatal("Seal() reused a nonce")
	}
	opened, err := secretCipher.Open(context.Background(), keyID, additionalData, first)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("Open() = %x, %v", opened, err)
	}
	first[len(first)-1] ^= 0xff
	if _, err := secretCipher.Open(context.Background(), keyID, additionalData, first); !errors.Is(err, ErrSecretDecryption) {
		t.Errorf("tampered Open() error = %v", err)
	}
	if _, err := secretCipher.Open(context.Background(), keyID, []byte("different owner"), second); !errors.Is(err, ErrSecretDecryption) {
		t.Errorf("wrong AAD Open() error = %v", err)
	}
	if _, err := secretCipher.Open(context.Background(), "future-key", additionalData, second); !errors.Is(err, ErrSecretCipherKeyMismatch) {
		t.Errorf("wrong key Open() error = %v", err)
	}
	if _, err := NewAESGCMSecretCipher("", key); !errors.Is(err, ErrInvalidSecretCipherKey) {
		t.Errorf("blank key ID error = %v", err)
	}
	if _, err := NewAESGCMSecretCipher("key", key[:16]); !errors.Is(err, ErrInvalidSecretCipherKey) {
		t.Errorf("short key error = %v", err)
	}
}

func TestSecretServicePersistsOnlyCiphertextAndPublishesCommittedRotations(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	repository := newSecretMemoryRepository(publisherID)
	secretCipher, _ := NewAESGCMSecretCipher("publisher-master-v1", bytes.Repeat([]byte{0x33}, 32))
	rotations, _ := NewSecretRotationBroker(8)
	subscription, _ := rotations.SubscribeSecretRotations()
	service, err := NewSecretService(repository, secretCipher, rotations)
	if err != nil {
		t.Fatalf("NewSecretService() error = %v", err)
	}
	vault, err := NewSecretVault(repository, secretCipher)
	if err != nil {
		t.Fatalf("NewSecretVault() error = %v", err)
	}
	reference := SecretReference{Name: "mqtt.password"}
	firstValue := bytes.Repeat([]byte{0x41}, 40)
	metadata, err := service.Put(context.Background(), publisherID, PutSecretInput{Reference: reference, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: firstValue}})
	if err != nil || metadata.Revision != 1 || metadata.PublisherRevision != 1 {
		t.Fatalf("Put(first) = %#v, %v", metadata, err)
	}
	stored := repository.encrypted(publisherID, reference)
	if stored == nil || bytes.Contains(stored.Ciphertext, firstValue) || stored.KeyID != "publisher-master-v1" {
		t.Fatalf("stored secret = %#v", stored)
	}
	assertSecretRotation(t, subscription, SecretRotationUpserted, 1, 1)

	resolved, resolvedMetadata, err := vault.Resolve(context.Background(), publisherID, reference, SecretKindOpaque)
	if err != nil || !bytes.Equal(resolved.Opaque, firstValue) || resolvedMetadata.Revision != 1 {
		t.Fatalf("Resolve(first) = %#v, %#v, %v", resolved, resolvedMetadata, err)
	}
	resolved.Opaque[0] = 0
	resolved.Destroy()
	reloaded, _, _ := vault.Resolve(context.Background(), publisherID, reference, SecretKindOpaque)
	if !bytes.Equal(reloaded.Opaque, firstValue) {
		t.Fatal("Resolve() returned aliased plaintext")
	}
	reloaded.Destroy()

	secondValue := bytes.Repeat([]byte{0x42}, 40)
	metadata, err = service.Put(context.Background(), publisherID, PutSecretInput{Reference: reference, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: secondValue}})
	if err != nil || metadata.Revision != 2 || metadata.PublisherRevision != 2 {
		t.Fatalf("Put(rotation) = %#v, %v", metadata, err)
	}
	assertSecretRotation(t, subscription, SecretRotationUpserted, 2, 2)
	if _, err := service.Put(context.Background(), publisherID, PutSecretInput{Reference: reference, Kind: SecretKindCACertificate, Material: SecretMaterial{CertificatePEM: []byte("not a certificate")}}); !errors.Is(err, ErrInvalidSecretMaterial) {
		t.Errorf("invalid kind material error = %v", err)
	}
	metadataList, err := service.List(context.Background(), publisherID)
	if err != nil || len(metadataList) != 1 || metadataList[0].Reference != reference || metadataList[0].Revision != 2 {
		t.Fatalf("List() = %#v, %v", metadataList, err)
	}

	if err := service.Delete(context.Background(), publisherID, reference); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertSecretRotation(t, subscription, SecretRotationDeleted, 3, 3)
	if _, _, err := vault.Resolve(context.Background(), publisherID, reference, SecretKindOpaque); !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("Resolve(deleted) error = %v", err)
	}
}

func TestSecretServiceValidatesDependenciesInputsAndImmutableKind(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	repository := newSecretMemoryRepository(publisherID)
	secretCipher, _ := NewAESGCMSecretCipher("key-v1", bytes.Repeat([]byte{0x11}, 32))
	rotations, _ := NewSecretRotationBroker(1)
	var nilRepository *secretMemoryRepository
	var nilCipher *AESGCMSecretCipher
	var nilRotations *SecretRotationBroker
	if _, err := NewSecretService(nilRepository, secretCipher, rotations); !errors.Is(err, ErrSecretRepositoryRequired) {
		t.Errorf("nil repository error = %v", err)
	}
	if _, err := NewSecretService(repository, nilCipher, rotations); !errors.Is(err, ErrSecretCipherRequired) {
		t.Errorf("nil cipher error = %v", err)
	}
	if _, err := NewSecretService(repository, secretCipher, nilRotations); !errors.Is(err, ErrSecretRotationRequired) {
		t.Errorf("nil rotations error = %v", err)
	}
	service, _ := NewSecretService(repository, secretCipher, rotations)
	reference := SecretReference{Name: "http.api_key"}
	valid := PutSecretInput{Reference: reference, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: []byte{1}}}
	if _, err := service.Put(nil, publisherID, valid); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Put(nil context) error = %v", err)
	}
	if _, err := service.Put(context.Background(), uuid.Nil, valid); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Put(nil publisher) error = %v", err)
	}
	if _, err := service.Put(context.Background(), publisherID, PutSecretInput{Reference: reference, Kind: "future", Material: SecretMaterial{Opaque: []byte{1}}}); !errors.Is(err, ErrInvalidSecretMaterial) {
		t.Errorf("Put(future kind) error = %v", err)
	}
	if _, err := service.Put(context.Background(), publisherID, valid); err != nil {
		t.Fatalf("Put(valid) error = %v", err)
	}
	repository.forceKind(publisherID, reference, SecretKindCACertificate)
	if _, err := service.Put(context.Background(), publisherID, valid); !errors.Is(err, ErrSecretKindMismatch) {
		t.Errorf("Put(kind change) error = %v", err)
	}
	if _, err := NewSecretVault(nilRepository, secretCipher); !errors.Is(err, ErrSecretRepositoryRequired) {
		t.Errorf("NewSecretVault(nil repository) error = %v", err)
	}
	if _, err := NewSecretVault(repository, nilCipher); !errors.Is(err, ErrSecretCipherRequired) {
		t.Errorf("NewSecretVault(nil cipher) error = %v", err)
	}
}

func TestSecretServiceValidatesCertificateMaterialKinds(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	repository := newSecretMemoryRepository(publisherID)
	secretCipher, _ := NewAESGCMSecretCipher("key-v1", bytes.Repeat([]byte{0x19}, 32))
	rotations, _ := NewSecretRotationBroker(8)
	service, _ := NewSecretService(repository, secretCipher, rotations)
	vault, _ := NewSecretVault(repository, secretCipher)
	caPEM, clientCertificatePEM, clientKeyPEM := generateSecretTestCertificates(t)
	for _, input := range []PutSecretInput{
		{Reference: SecretReference{Name: "tls.ca"}, Kind: SecretKindCACertificate, Material: SecretMaterial{CertificatePEM: caPEM}},
		{Reference: SecretReference{Name: "tls.client"}, Kind: SecretKindClientIdentity, Material: SecretMaterial{CertificatePEM: clientCertificatePEM, PrivateKeyPEM: clientKeyPEM}},
	} {
		if _, err := service.Put(context.Background(), publisherID, input); err != nil {
			t.Fatalf("Put(%s) error = %v", input.Kind, err)
		}
		resolved, _, err := vault.Resolve(context.Background(), publisherID, input.Reference, input.Kind)
		if err != nil {
			t.Fatalf("Resolve(%s) error = %v", input.Kind, err)
		}
		resolved.Destroy()
	}
	for _, input := range []PutSecretInput{
		{Reference: SecretReference{Name: "bad.ca"}, Kind: SecretKindCACertificate, Material: SecretMaterial{CertificatePEM: clientCertificatePEM}},
		{Reference: SecretReference{Name: "bad.client"}, Kind: SecretKindClientIdentity, Material: SecretMaterial{CertificatePEM: clientCertificatePEM, PrivateKeyPEM: caPEM}},
		{Reference: SecretReference{Name: "mixed"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: []byte{1}, CertificatePEM: caPEM}},
	} {
		if _, err := service.Put(context.Background(), publisherID, input); !errors.Is(err, ErrInvalidSecretMaterial) {
			t.Errorf("Put(invalid %s) error = %v", input.Reference.Name, err)
		}
	}
}

func TestSecretRotationBrokerDisconnectsLaggingSubscriber(t *testing.T) {
	t.Parallel()

	broker, _ := NewSecretRotationBroker(1)
	subscription, _ := broker.SubscribeSecretRotations()
	event := SecretRotationEvent{
		PublisherID: uuid.New(), Reference: SecretReference{Name: "mqtt.password"}, Kind: SecretKindOpaque,
		Revision: 1, PublisherRevision: 1, Action: SecretRotationUpserted, RotatedAt: time.Now().UTC(),
	}
	broker.PublishSecretRotation(event)
	event.Revision++
	event.PublisherRevision++
	broker.PublishSecretRotation(event)
	for range subscription.Events() {
	}
	if !errors.Is(subscription.Err(), ErrSecretRotationSubscriberLag) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
}

func TestSecretServiceContainsCipherAndRotationPublisherFailures(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	repository := newSecretMemoryRepository(publisherID)
	leakingCipher := &leakingSecretCipher{}
	service, err := NewSecretService(repository, leakingCipher, panicSecretRotationPublisher{})
	if err != nil {
		t.Fatalf("NewSecretService() error = %v", err)
	}
	material := bytes.Repeat([]byte{0x6b}, 32)
	_, err = service.Put(context.Background(), publisherID, PutSecretInput{
		Reference: SecretReference{Name: "http.api_key"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: material},
	})
	if !errors.Is(err, ErrSecretEncryption) || strings.Contains(err.Error(), fmt.Sprintf("%x", material)) {
		t.Fatalf("Put(cipher failure) error = %v", err)
	}

	secretCipher, _ := NewAESGCMSecretCipher("key-v1", bytes.Repeat([]byte{0x21}, 32))
	service, _ = NewSecretService(repository, secretCipher, panicSecretRotationPublisher{})
	metadata, err := service.Put(context.Background(), publisherID, PutSecretInput{
		Reference: SecretReference{Name: "http.api_key"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: material},
	})
	if err != nil || metadata.Revision != 1 {
		t.Fatalf("Put(panicking rotations) = %#v, %v", metadata, err)
	}
}

func TestSecretServiceRejectsCorruptRepositoryMetadataBeforeNotification(t *testing.T) {
	t.Parallel()

	publisherID := uuid.New()
	repository := &corruptSecretRepository{secretMemoryRepository: newSecretMemoryRepository(publisherID)}
	secretCipher, _ := NewAESGCMSecretCipher("key-v1", bytes.Repeat([]byte{0x27}, 32))
	rotations, _ := NewSecretRotationBroker(1)
	subscription, _ := rotations.SubscribeSecretRotations()
	service, _ := NewSecretService(repository, secretCipher, rotations)
	_, err := service.Put(context.Background(), publisherID, PutSecretInput{
		Reference: SecretReference{Name: "mqtt.password"}, Kind: SecretKindOpaque, Material: SecretMaterial{Opaque: []byte{1}},
	})
	if !errors.Is(err, ErrInvalidPublisher) {
		t.Fatalf("Put(corrupt metadata) error = %v", err)
	}
	select {
	case event := <-subscription.Events():
		t.Fatalf("corrupt metadata emitted event %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

type secretMemoryRepository struct {
	mu         sync.Mutex
	publishers map[uuid.UUID]uint64
	secrets    map[string]EncryptedSecret
	now        time.Time
}

type leakingSecretCipher struct{}

func (*leakingSecretCipher) Seal(_ context.Context, _, plaintext []byte) (string, []byte, error) {
	return "", nil, fmt.Errorf("cipher rejected %x", plaintext)
}

func (*leakingSecretCipher) Open(_ context.Context, _ string, _, ciphertext []byte) ([]byte, error) {
	return nil, fmt.Errorf("cipher rejected %x", ciphertext)
}

type panicSecretRotationPublisher struct{}

func (panicSecretRotationPublisher) PublishSecretRotation(SecretRotationEvent) {
	panic("rotation notifier panic")
}

type corruptSecretRepository struct{ *secretMemoryRepository }

func (repository *corruptSecretRepository) Upsert(ctx context.Context, secret *EncryptedSecret) (*SecretMetadata, error) {
	metadata, err := repository.secretMemoryRepository.Upsert(ctx, secret)
	if err == nil {
		metadata.PublisherID = uuid.New()
	}
	return metadata, err
}

func newSecretMemoryRepository(publisherIDs ...uuid.UUID) *secretMemoryRepository {
	repository := &secretMemoryRepository{
		publishers: make(map[uuid.UUID]uint64), secrets: make(map[string]EncryptedSecret),
		now: time.Date(2026, time.August, 23, 15, 0, 0, 0, time.UTC),
	}
	for _, publisherID := range publisherIDs {
		repository.publishers[publisherID] = 0
	}
	return repository
}

func (repository *secretMemoryRepository) Upsert(ctx context.Context, secret *EncryptedSecret) (*SecretMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.publishers[secret.Metadata.PublisherID]; !exists {
		return nil, ErrPublisherNotFound
	}
	key := secretMemoryKey(secret.Metadata.PublisherID, secret.Metadata.Reference)
	current, exists := repository.secrets[key]
	if exists && current.Metadata.Kind != secret.Metadata.Kind {
		return nil, ErrSecretKindMismatch
	}
	if !exists {
		current.ID = uuid.New()
		current.Metadata.CreatedAt = repository.now
		current.Metadata.Revision = 0
	}
	current.Metadata.PublisherID = secret.Metadata.PublisherID
	current.Metadata.Reference = secret.Metadata.Reference
	current.Metadata.Kind = secret.Metadata.Kind
	current.Metadata.Revision++
	current.Metadata.RotatedAt = repository.now.Add(time.Duration(current.Metadata.Revision) * time.Second)
	repository.publishers[secret.Metadata.PublisherID]++
	current.Metadata.PublisherRevision = repository.publishers[secret.Metadata.PublisherID]
	current.KeyID = secret.KeyID
	current.Ciphertext = append([]byte(nil), secret.Ciphertext...)
	repository.secrets[key] = cloneEncryptedSecret(current)
	metadata := current.Metadata
	return &metadata, nil
}

func (repository *secretMemoryRepository) FindEncrypted(ctx context.Context, publisherID uuid.UUID, reference SecretReference) (*EncryptedSecret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	secret, exists := repository.secrets[secretMemoryKey(publisherID, reference)]
	if !exists {
		return nil, ErrSecretNotFound
	}
	cloned := cloneEncryptedSecret(secret)
	return &cloned, nil
}

func (repository *secretMemoryRepository) ListMetadata(ctx context.Context, publisherID uuid.UUID) ([]SecretMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.publishers[publisherID]; !exists {
		return nil, ErrPublisherNotFound
	}
	metadata := make([]SecretMetadata, 0)
	for _, secret := range repository.secrets {
		if secret.Metadata.PublisherID == publisherID {
			metadata = append(metadata, secret.Metadata)
		}
	}
	return metadata, nil
}

func (repository *secretMemoryRepository) Delete(ctx context.Context, publisherID uuid.UUID, reference SecretReference) (*SecretMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := secretMemoryKey(publisherID, reference)
	secret, exists := repository.secrets[key]
	if !exists {
		return nil, ErrSecretNotFound
	}
	delete(repository.secrets, key)
	repository.publishers[publisherID]++
	secret.Metadata.Revision++
	secret.Metadata.PublisherRevision = repository.publishers[publisherID]
	secret.Metadata.RotatedAt = repository.now.Add(time.Duration(secret.Metadata.Revision) * time.Second)
	metadata := secret.Metadata
	return &metadata, nil
}

func (repository *secretMemoryRepository) encrypted(publisherID uuid.UUID, reference SecretReference) *EncryptedSecret {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	secret, exists := repository.secrets[secretMemoryKey(publisherID, reference)]
	if !exists {
		return nil
	}
	cloned := cloneEncryptedSecret(secret)
	return &cloned
}

func (repository *secretMemoryRepository) forceKind(publisherID uuid.UUID, reference SecretReference, kind SecretKind) {
	repository.mu.Lock()
	secret := repository.secrets[secretMemoryKey(publisherID, reference)]
	secret.Metadata.Kind = kind
	repository.secrets[secretMemoryKey(publisherID, reference)] = secret
	repository.mu.Unlock()
}

func secretMemoryKey(publisherID uuid.UUID, reference SecretReference) string {
	return publisherID.String() + ":" + reference.Name
}

func assertSecretRotation(t *testing.T, subscription SecretRotationSubscription, action SecretRotationAction, revision, publisherRevision uint64) {
	t.Helper()
	select {
	case event := <-subscription.Events():
		if event.Action != action || event.Revision != revision || event.PublisherRevision != publisherRevision {
			t.Fatalf("rotation event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for secret rotation")
	}
}
