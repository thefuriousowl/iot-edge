package credential

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

const (
	maxNameLength        = 100
	maxDescriptionLength = 500
	maxSearchLength      = 200
)

type SecretService interface {
	Put(context.Context, uuid.UUID, publisher.PutSecretInput) (*publisher.SecretMetadata, error)
	List(context.Context, uuid.UUID) ([]publisher.SecretMetadata, error)
	Delete(context.Context, uuid.UUID, publisher.SecretReference) error
}

type Service struct {
	repository Repository
	secrets    SecretService
}

func NewService(repository Repository, secrets SecretService) (*Service, error) {
	if repository == nil {
		return nil, ErrRepository
	}
	if secrets == nil {
		return nil, ErrSecretService
	}
	return &Service{repository: repository, secrets: secrets}, nil
}

func (service *Service) Create(ctx context.Context, input CreateInput) (*Profile, error) {
	if service == nil || ctx == nil || !validType(input.Type) {
		return nil, ErrInvalidInput
	}
	name, description, err := normalize(input.Name, input.Description)
	if err != nil {
		return nil, err
	}
	profile := &Profile{ID: uuid.New(), Type: input.Type, Name: name, Description: description}
	if err := service.repository.Create(ctx, profile); err != nil {
		return nil, err
	}
	return service.Get(ctx, profile.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Profile, error) {
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	profile, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	profile.Secrets, err = service.secrets.List(ctx, id)
	if err != nil {
		return nil, translateSecretError(err)
	}
	return profile, nil
}

func (service *Service) List(ctx context.Context, input ListInput) ([]Profile, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	input.Search = strings.TrimSpace(input.Search)
	if len(input.Search) > maxSearchLength || input.Type != nil && !validType(*input.Type) {
		return nil, ErrInvalidInput
	}
	profiles, err := service.repository.List(ctx, input)
	if err != nil {
		return nil, err
	}
	for index := range profiles {
		profiles[index].Secrets, err = service.secrets.List(ctx, profiles[index].ID)
		if err != nil {
			return nil, translateSecretError(err)
		}
	}
	return profiles, nil
}

func (service *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Profile, error) {
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	profile, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		profile.Name, _, err = normalize(*input.Name, profile.Description)
		if err != nil {
			return nil, err
		}
	}
	if input.Description.Set {
		_, profile.Description, err = normalize(profile.Name, input.Description.Value)
		if err != nil {
			return nil, err
		}
	}
	if err := service.repository.Update(ctx, profile); err != nil {
		return nil, err
	}
	return service.Get(ctx, id)
}

func (service *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if service == nil || ctx == nil || id == uuid.Nil {
		return ErrInvalidInput
	}
	return service.repository.Delete(ctx, id)
}

func (service *Service) ValidateCredential(ctx context.Context, id uuid.UUID, publisherType publisher.Type) error {
	if service == nil || ctx == nil || id == uuid.Nil {
		return publisher.ErrCredentialNotFound
	}
	profile, err := service.repository.Find(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return publisher.ErrCredentialNotFound
		}
		return err
	}
	compatible := profile.Type == TypeMQTT && publisherType == publisher.TypeMQTT ||
		profile.Type == TypeHTTP && (publisherType == publisher.TypeHTTPServer || publisherType == publisher.TypeHTTPClient)
	if !compatible {
		return publisher.ErrCredentialIncompatible
	}
	return nil
}

func validType(profileType Type) bool {
	return profileType == TypeMQTT || profileType == TypeHTTP
}

func (service *Service) PutSecret(ctx context.Context, id uuid.UUID, slot string, material publisher.SecretMaterial) (*publisher.SecretMetadata, error) {
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	profile, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	kind, err := ExpectedSecretKind(profile.Type, slot)
	if err != nil {
		return nil, err
	}
	metadata, err := service.secrets.Put(ctx, id, publisher.PutSecretInput{Reference: publisher.SecretReference{Name: slot}, Kind: kind, Material: material})
	return metadata, translateSecretError(err)
}

func (service *Service) DeleteSecret(ctx context.Context, id uuid.UUID, slot string) error {
	if service == nil || ctx == nil || id == uuid.Nil {
		return ErrInvalidInput
	}
	profile, err := service.repository.Find(ctx, id)
	if err != nil {
		return err
	}
	if _, err := ExpectedSecretKind(profile.Type, slot); err != nil {
		return err
	}
	return translateSecretError(service.secrets.Delete(ctx, id, publisher.SecretReference{Name: slot}))
}

func translateSecretError(err error) error {
	if errors.Is(err, publisher.ErrPublisherNotFound) {
		return ErrNotFound
	}
	return err
}

func normalize(name string, description *string) (string, *string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLength {
		return "", nil, ErrInvalidInput
	}
	if description == nil {
		return name, nil, nil
	}
	value := strings.TrimSpace(*description)
	if value == "" || len(value) > maxDescriptionLength {
		return "", nil, ErrInvalidInput
	}
	return name, &value, nil
}

type PublisherSecretResolver struct {
	publishers PublisherStore
	vault      publisher.SecretResolver
}

func NewPublisherSecretResolver(publishers PublisherStore, vault publisher.SecretResolver) (*PublisherSecretResolver, error) {
	if publishers == nil {
		return nil, ErrPublisherStore
	}
	if vault == nil {
		return nil, ErrVault
	}
	return &PublisherSecretResolver{publishers: publishers, vault: vault}, nil
}

func (resolver *PublisherSecretResolver) Resolve(ctx context.Context, publisherID uuid.UUID, reference publisher.SecretReference, kind publisher.SecretKind) (publisher.SecretMaterial, publisher.SecretMetadata, error) {
	if resolver == nil || ctx == nil || publisherID == uuid.Nil {
		return publisher.SecretMaterial{}, publisher.SecretMetadata{}, publisher.ErrInvalidInput
	}
	entity, err := resolver.publishers.Find(ctx, publisherID)
	if err != nil {
		return publisher.SecretMaterial{}, publisher.SecretMetadata{}, err
	}
	if entity.CredentialID == nil || *entity.CredentialID == uuid.Nil {
		return publisher.SecretMaterial{}, publisher.SecretMetadata{}, publisher.ErrSecretNotFound
	}
	return resolver.vault.Resolve(ctx, *entity.CredentialID, reference, kind)
}
