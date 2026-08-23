package publisherpostgres

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	"gorm.io/gorm"
)

func TestSecretRepositoryRotatesCredentialAndUpdatesLinkedPublisherRevision_Integration(t *testing.T) {
	database, tagID, _ := newPublisherSecretRepositoryDatabase(t)
	publishers := NewRepository(database)
	repository := NewSecretRepository(database)
	credentialID := createCredentialProfile(t, database, "Secret Repository")
	entity := publisher.Publisher{
		Type: publisher.TypeMQTT, Name: "Secret Repository", Enabled: true,
		Config: publisher.Config(`{"trigger":{"mode":"interval","interval_ms":60000}}`), ConfigVersion: 2,
		Sources:      []publisher.SourceSelection{{Alias: "value", Reference: publisher.TagSource(tagID)}},
		CredentialID: &credentialID,
	}
	if err := publishers.Create(context.Background(), &entity); err != nil {
		t.Fatalf("creating Publisher: %v", err)
	}
	reference := publisher.SecretReference{Name: "mqtt.password"}
	first := &publisher.EncryptedSecret{
		Metadata: publisher.SecretMetadata{PublisherID: credentialID, Reference: reference, Kind: publisher.SecretKindOpaque},
		KeyID:    "master-v1", Ciphertext: bytes.Repeat([]byte{0x31}, 32),
	}
	metadata, err := repository.Upsert(context.Background(), first)
	if err != nil || metadata.Revision != 1 || metadata.PublisherRevision != 1 || metadata.CreatedAt.IsZero() || metadata.RotatedAt.IsZero() {
		t.Fatalf("Upsert(first) = %#v, %v", metadata, err)
	}
	first.Ciphertext[0] = 0
	stored, err := repository.FindEncrypted(context.Background(), credentialID, reference)
	if err != nil || stored.Ciphertext[0] != 0x31 || stored.Metadata.PublisherRevision != 1 {
		t.Fatalf("FindEncrypted(first) = %#v, %v", stored, err)
	}
	stored.Ciphertext[0] = 0
	reloaded, _ := repository.FindEncrypted(context.Background(), credentialID, reference)
	if reloaded.Ciphertext[0] != 0x31 {
		t.Fatal("FindEncrypted() returned aliased ciphertext")
	}

	second := &publisher.EncryptedSecret{
		Metadata: publisher.SecretMetadata{PublisherID: credentialID, Reference: reference, Kind: publisher.SecretKindOpaque},
		KeyID:    "master-v2", Ciphertext: bytes.Repeat([]byte{0x32}, 40),
	}
	metadata, err = repository.Upsert(context.Background(), second)
	if err != nil || metadata.Revision != 2 || metadata.PublisherRevision != 2 || metadata.CreatedAt != first.Metadata.CreatedAt {
		t.Fatalf("Upsert(rotation) = %#v, %v", metadata, err)
	}
	kindChange := *second
	kindChange.Metadata.Kind = publisher.SecretKindCACertificate
	if _, err := repository.Upsert(context.Background(), &kindChange); !errors.Is(err, publisher.ErrInvalidSecretMaterial) {
		t.Errorf("Upsert(kind change) error = %v", err)
	}
	listed, err := repository.ListMetadata(context.Background(), credentialID)
	if err != nil || len(listed) != 1 || listed[0].Reference != reference || listed[0].Revision != 2 {
		t.Fatalf("ListMetadata() = %#v, %v", listed, err)
	}

	deleted, err := repository.Delete(context.Background(), credentialID, reference)
	if err != nil || deleted.Revision != 3 || deleted.PublisherRevision != 3 {
		t.Fatalf("Delete() = %#v, %v", deleted, err)
	}
	if _, err := repository.FindEncrypted(context.Background(), credentialID, reference); !errors.Is(err, publisher.ErrSecretNotFound) {
		t.Errorf("FindEncrypted(deleted) error = %v", err)
	}
	foundPublisher, err := publishers.Find(context.Background(), entity.ID)
	if err != nil || foundPublisher.SecretRevision != 3 {
		t.Fatalf("Publisher secret revision = %#v, %v", foundPublisher, err)
	}
}

func TestSecretRepositorySerializesConcurrentRotationsAndCascades_Integration(t *testing.T) {
	database, tagID, _ := newPublisherSecretRepositoryDatabase(t)
	publishers := NewRepository(database)
	repository := NewSecretRepository(database)
	credentialID := createCredentialProfile(t, database, "Concurrent Secrets")
	entity := publisher.Publisher{
		Type: publisher.TypeMQTT, Name: "Concurrent Secrets", Config: publisher.Config(`{"trigger":{"mode":"interval","interval_ms":60000}}`), ConfigVersion: 2,
		Sources:      []publisher.SourceSelection{{Alias: "value", Reference: publisher.TagSource(tagID)}},
		CredentialID: &credentialID,
	}
	if err := publishers.Create(context.Background(), &entity); err != nil {
		t.Fatalf("creating Publisher: %v", err)
	}
	const writerCount = 16
	results := make(chan *publisher.SecretMetadata, writerCount)
	errorsChannel := make(chan error, writerCount)
	var waitGroup sync.WaitGroup
	for index := range writerCount {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			metadata, err := repository.Upsert(context.Background(), &publisher.EncryptedSecret{
				Metadata: publisher.SecretMetadata{PublisherID: credentialID, Reference: publisher.SecretReference{Name: "mqtt.username"}, Kind: publisher.SecretKindOpaque},
				KeyID:    "master-v1", Ciphertext: bytes.Repeat([]byte{byte(index + 1)}, 32),
			})
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- metadata
		}(index)
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("concurrent Upsert() error = %v", err)
	}
	revisions := make(map[uint64]struct{}, writerCount)
	for metadata := range results {
		revisions[metadata.Revision] = struct{}{}
	}
	if len(revisions) != writerCount {
		t.Fatalf("concurrent revisions = %#v", revisions)
	}
	stored, err := repository.FindEncrypted(context.Background(), credentialID, publisher.SecretReference{Name: "mqtt.username"})
	if err != nil || stored.Metadata.Revision != writerCount || stored.Metadata.PublisherRevision != writerCount {
		t.Fatalf("FindEncrypted(concurrent) = %#v, %v", stored, err)
	}
	if err := publishers.Delete(context.Background(), entity.ID); err != nil {
		t.Fatalf("deleting Publisher: %v", err)
	}
	if _, err := repository.FindEncrypted(context.Background(), credentialID, publisher.SecretReference{Name: "mqtt.username"}); err != nil {
		t.Errorf("FindEncrypted(after Publisher delete) error = %v", err)
	}
	if err := database.Table("credential_profiles").Where("id = ?", credentialID).Delete(nil).Error; err != nil {
		t.Fatalf("deleting Credential Profile: %v", err)
	}
	if _, err := repository.FindEncrypted(context.Background(), credentialID, publisher.SecretReference{Name: "mqtt.username"}); !errors.Is(err, publisher.ErrSecretNotFound) {
		t.Errorf("FindEncrypted(after Credential cascade) error = %v", err)
	}
}

func createCredentialProfile(t *testing.T, database *gorm.DB, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := database.Exec(`INSERT INTO credential_profiles (id,type,name) VALUES (?,?,?)`, id, "mqtt", name).Error; err != nil {
		t.Fatalf("creating Credential Profile: %v", err)
	}
	return id
}

func TestSecretRepositoryRejectsInvalidAndCancelledInputs_Integration(t *testing.T) {
	database, _, _ := newPublisherSecretRepositoryDatabase(t)
	repository := NewSecretRepository(database)
	if _, err := repository.Upsert(context.Background(), nil); !errors.Is(err, publisher.ErrInvalidInput) {
		t.Errorf("Upsert(nil) error = %v", err)
	}
	if _, err := repository.FindEncrypted(context.Background(), uuid.Nil, publisher.SecretReference{Name: "valid"}); !errors.Is(err, publisher.ErrInvalidInput) {
		t.Errorf("FindEncrypted(nil ID) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListMetadata(cancelled, uuid.New()); !errors.Is(err, context.Canceled) {
		t.Errorf("ListMetadata(cancelled) error = %v", err)
	}
	if _, err := repository.Delete(context.Background(), uuid.New(), publisher.SecretReference{Name: "missing"}); !errors.Is(err, publisher.ErrSecretNotFound) {
		t.Errorf("Delete(missing) error = %v", err)
	}
}

func newPublisherSecretRepositoryDatabase(t *testing.T) (*gorm.DB, uuid.UUID, uuid.UUID) {
	t.Helper()
	return newPublisherRepositoryDatabase(t)
}
