package publisher

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultPublishersPerPage = 20
	maxPublishersPerPage     = 100
	maxPublisherNameLength   = 100
	maxPublisherDescription  = 500
	maxPublisherConfigBytes  = 64 * 1024
	maxPublisherSearchLength = 200
)

type OptionalPublisherDescription struct {
	Set   bool
	Value *string
}

type CreateInput struct {
	Type        Type
	Name        string
	Description *string
	Enabled     *bool
	Config      Config
	Sources     []SourceSelection
}

type UpdateInput struct {
	Name        *string
	Description OptionalPublisherDescription
	Enabled     *bool
	Config      *Config
	Sources     *[]SourceSelection
}

type Service struct {
	repository  Repository
	sources     SourceResolver
	definitions *DefinitionRegistry
}

func NewService(repository Repository, sources SourceResolver, definitions *DefinitionRegistry) (*Service, error) {
	if isNilSourceDependency(repository) {
		return nil, ErrRepositoryRequired
	}
	if isNilSourceDependency(sources) {
		return nil, ErrSourceResolverRequired
	}
	if definitions == nil {
		return nil, ErrDefinitionRegistryRequired
	}
	return &Service{repository: repository, sources: sources, definitions: definitions}, nil
}

func (service *Service) Types() []DefinitionDescriptor {
	if service == nil || service.definitions == nil {
		return []DefinitionDescriptor{}
	}
	return service.definitions.List()
}

func (service *Service) Create(ctx context.Context, input CreateInput) (*Publisher, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	definition, err := service.definitions.Find(input.Type)
	if err != nil {
		return nil, err
	}
	name, err := normalizePublisherName(input.Name)
	if err != nil {
		return nil, err
	}
	description, err := normalizePublisherDescription(input.Description)
	if err != nil {
		return nil, err
	}
	config, err := normalizeDefinitionConfig(ctx, definition, input.Config)
	if err != nil {
		return nil, err
	}
	sources, resolved, err := service.normalizeSources(ctx, definition, input.Sources)
	if err != nil {
		return nil, err
	}
	if err := validateTriggerSources(config, sources); err != nil {
		return nil, err
	}
	if err := validateDefinitionSourceConfig(ctx, definition, config, resolved); err != nil {
		return nil, err
	}
	enabled := false
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	descriptor := definition.Descriptor()
	entity := &Publisher{
		Type: input.Type, Name: name, Description: description, Enabled: enabled,
		Config: config, ConfigVersion: descriptor.ConfigVersion, Sources: sources, SourceCount: len(sources),
	}
	if err := service.repository.Create(ctx, entity); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, entity.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Publisher, error) {
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	if input.Type != nil {
		if _, err := service.definitions.Find(*input.Type); err != nil {
			return nil, err
		}
	}
	input.Search = strings.TrimSpace(input.Search)
	if len(input.Search) > maxPublisherSearchLength || input.Page < 0 || input.PerPage < 0 {
		return nil, ErrInvalidInput
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultPublishersPerPage
	}
	if input.PerPage > maxPublishersPerPage {
		return nil, ErrInvalidInput
	}
	return service.repository.List(ctx, input)
}

func (service *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Publisher, error) {
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	current, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	entity := clonePublisher(*current)
	definition, err := service.definitions.Find(entity.Type)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		entity.Name, err = normalizePublisherName(*input.Name)
		if err != nil {
			return nil, err
		}
	}
	if input.Description.Set {
		entity.Description, err = normalizePublisherDescription(input.Description.Value)
		if err != nil {
			return nil, err
		}
	}
	if input.Enabled != nil {
		entity.Enabled = *input.Enabled
	}
	if input.Config != nil {
		entity.Config, err = normalizeDefinitionConfig(ctx, definition, *input.Config)
		if err != nil {
			return nil, err
		}
		entity.ConfigVersion = definition.Descriptor().ConfigVersion
	} else if entity.Enabled {
		if entity.ConfigVersion != definition.Descriptor().ConfigVersion {
			return nil, ErrConfigVersionMismatch
		}
		entity.Config, err = normalizeDefinitionConfig(ctx, definition, entity.Config)
		if err != nil {
			return nil, err
		}
	}
	if input.Sources != nil {
		entity.Sources = append([]SourceSelection(nil), (*input.Sources)...)
	}
	var resolved []ResolvedSource
	entity.Sources, resolved, err = service.normalizeSources(ctx, definition, entity.Sources)
	if err != nil {
		return nil, err
	}
	if err := validateTriggerSources(entity.Config, entity.Sources); err != nil {
		return nil, err
	}
	if err := validateDefinitionSourceConfig(ctx, definition, entity.Config, resolved); err != nil {
		return nil, err
	}
	entity.SourceCount = len(entity.Sources)
	if err := service.repository.Update(ctx, &entity); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*Publisher, error) {
	if enabled {
		return service.Update(ctx, id, UpdateInput{Enabled: &enabled})
	}
	if service == nil || ctx == nil || id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	entity, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	entity.Enabled = false
	if err := service.repository.Update(ctx, entity); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if service == nil || ctx == nil || id == uuid.Nil {
		return ErrInvalidInput
	}
	return service.repository.Delete(ctx, id)
}

func (service *Service) normalizeSources(ctx context.Context, definition Definition, selections []SourceSelection) ([]SourceSelection, []ResolvedSource, error) {
	normalized, err := NormalizeSourceSelections(selections)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := service.sources.Resolve(ctx, normalized)
	if err != nil {
		return nil, nil, err
	}
	if len(resolved) != len(normalized) {
		return nil, nil, ErrSourceNotFound
	}
	for index, source := range resolved {
		selection := normalized[index]
		if source.Alias != selection.Alias || source.Descriptor.Reference != selection.Reference {
			return nil, nil, ErrSourceNotFound
		}
		if !definition.Supports(source.Descriptor) {
			return nil, nil, fmt.Errorf("%w: %s", ErrIncompatibleSource, selection.Reference)
		}
	}
	return normalized, cloneResolvedSources(resolved), nil
}

func validateDefinitionSourceConfig(ctx context.Context, definition Definition, config Config, sources []ResolvedSource) error {
	validator, ok := definition.(SourceConfigDefinition)
	if !ok {
		return nil
	}
	return validator.ValidateSourceConfig(ctx, cloneConfig(config), cloneResolvedSources(sources))
}

func normalizeDefinitionConfig(ctx context.Context, definition Definition, config Config) (Config, error) {
	if len(config) > maxPublisherConfigBytes {
		return nil, ErrInvalidPublisherConfig
	}
	normalized, err := definition.NormalizeConfig(ctx, cloneConfig(config))
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 || len(normalized) > maxPublisherConfigBytes {
		return nil, ErrInvalidPublisherConfig
	}
	return cloneConfig(normalized), nil
}

func validateTriggerSources(config Config, sources []SourceSelection) error {
	trigger, err := ParseTriggerConfig(config)
	if err != nil {
		return err
	}
	if trigger.Mode != TriggerModeOnChange {
		return nil
	}
	for _, source := range sources {
		if source.Alias == trigger.SourceAlias {
			return nil
		}
	}
	return ErrInvalidPublisherConfig
}

func normalizePublisherName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || len(name) > maxPublisherNameLength {
		return "", ErrInvalidInput
	}
	return name, nil
}

func normalizePublisherDescription(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	description := strings.TrimSpace(*value)
	if description == "" {
		return nil, nil
	}
	if len(description) > maxPublisherDescription {
		return nil, ErrInvalidInput
	}
	return &description, nil
}
