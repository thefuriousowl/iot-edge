package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	defaultInstancePerPage = 20
	maxInstancePerPage     = 100
)

var (
	ErrRepositoryRequired          = errors.New("plugin repository is required")
	ErrRegistryRequired            = errors.New("plugin registry is required")
	ErrInvalidInput                = errors.New("invalid plugin input")
	ErrMultipleInstancesNotAllowed = errors.New("plugin type does not allow multiple instances")
	ErrConfigVersionMismatch       = errors.New("plugin config version does not match implementation")
	ErrInvalidCapabilityHost       = errors.New("invalid plugin capability host")
	ErrCapabilityUnavailable       = errors.New("required plugin capability is unavailable")
)

type CreateInput struct {
	Type    Type
	Name    string
	Enabled *bool
	Config  Config
}

type UpdateInput struct {
	Name    *string
	Enabled *bool
	Config  Config
}

type Service struct {
	repository Repository
	registry   *Registry
	createMu   sync.Mutex
}

func NewService(repository Repository, registry *Registry) (*Service, error) {
	if isNil(repository) {
		return nil, ErrRepositoryRequired
	}
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	return &Service{repository: repository, registry: registry}, nil
}

func (service *Service) Types() []Manifest {
	return service.registry.List()
}

func (service *Service) OutputDescriptors(ctx context.Context, id uuid.UUID) ([]OutputDescriptor, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	instance, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	manifest, err := service.registry.Manifest(instance.Type)
	if err != nil {
		return nil, err
	}
	return append([]OutputDescriptor(nil), manifest.Outputs...), nil
}

func (service *Service) Create(ctx context.Context, input CreateInput) (*Instance, error) {
	manifest, err := service.registry.Manifest(input.Type)
	if err != nil {
		return nil, err
	}
	name, err := normalizeInstanceName(input.Name)
	if err != nil {
		return nil, err
	}
	config, err := service.normalizeConfig(ctx, input.Type, input.Config)
	if err != nil {
		return nil, err
	}
	enabled := false
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	instance := &Instance{Type: input.Type, Name: name, Enabled: enabled, Config: config, ConfigVersion: manifest.ConfigVersion}

	service.createMu.Lock()
	defer service.createMu.Unlock()
	if !manifest.MultipleInstances {
		result, listErr := service.repository.List(ctx, ListInput{Type: &input.Type, Page: 1, PerPage: 1})
		if listErr != nil {
			return nil, listErr
		}
		if result.Total > 0 {
			return nil, ErrMultipleInstancesNotAllowed
		}
	}
	if err := service.repository.Create(ctx, instance); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, instance.ID)
}

func (service *Service) Get(ctx context.Context, id uuid.UUID) (*Instance, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	if input.Type != nil {
		if _, err := service.registry.Manifest(*input.Type); err != nil {
			return nil, err
		}
	}
	input.Search = strings.TrimSpace(input.Search)
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PerPage < 1 {
		input.PerPage = defaultInstancePerPage
	}
	if input.PerPage > maxInstancePerPage {
		return nil, ErrInvalidInput
	}
	return service.repository.List(ctx, input)
}

func (service *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (*Instance, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidInput
	}
	instance, err := service.repository.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	manifest, err := service.registry.Manifest(instance.Type)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		instance.Name, err = normalizeInstanceName(*input.Name)
		if err != nil {
			return nil, err
		}
	}
	if input.Config != nil {
		instance.Config, err = service.normalizeConfig(ctx, instance.Type, input.Config)
		if err != nil {
			return nil, err
		}
		instance.ConfigVersion = manifest.ConfigVersion
	} else {
		instance.Config = cloneConfig(instance.Config)
	}
	if input.Enabled != nil {
		if *input.Enabled && instance.ConfigVersion != manifest.ConfigVersion {
			return nil, ErrConfigVersionMismatch
		}
		if *input.Enabled {
			if err := service.registry.ValidateConfig(ctx, instance.Type, instance.Config); err != nil {
				return nil, err
			}
		}
		instance.Enabled = *input.Enabled
	}
	if err := service.repository.Update(ctx, instance); err != nil {
		return nil, err
	}
	return service.repository.Find(ctx, id)
}

func (service *Service) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (*Instance, error) {
	return service.Update(ctx, id, UpdateInput{Enabled: &enabled})
}

func (service *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrInvalidInput
	}
	return service.repository.Delete(ctx, id)
}

func (service *Service) normalizeConfig(ctx context.Context, pluginType Type, config Config) (Config, error) {
	if len(strings.TrimSpace(string(config))) == 0 {
		config = Config(`{}`)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil || object == nil {
		return nil, ErrInvalidConfig
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if err := service.registry.ValidateConfig(ctx, pluginType, canonical); err != nil {
		return nil, err
	}
	return Config(canonical), nil
}

func normalizeInstanceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxPluginNameLength {
		return "", ErrInvalidInput
	}
	return name, nil
}

var _ OutputDescriptorResolver = (*Service)(nil)
