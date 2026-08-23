package plugin

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := NewService(nil, registry); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("NewService(nil repository) error = %v", err)
	}
	var typedNil *pluginMemoryRepository
	if _, err := NewService(typedNil, registry); !errors.Is(err, ErrRepositoryRequired) {
		t.Fatalf("NewService(typed nil repository) error = %v", err)
	}
	if _, err := NewService(newPluginMemoryRepository(), nil); !errors.Is(err, ErrRegistryRequired) {
		t.Fatalf("NewService(nil registry) error = %v", err)
	}
}

func TestServiceCreatesCanonicalManifestVersionedInstances(t *testing.T) {
	t.Parallel()

	definition := validRegistryDefinition("energy_management")
	definition.manifest.ConfigVersion = 3
	validated := ""
	definition.validate = func(config Config) error {
		validated = string(config)
		return nil
	}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	service, err := NewService(repository, registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	config := Config(`{"tariff":4.2,"logger_id":"logger-a"}`)
	instance, err := service.Create(context.Background(), CreateInput{Type: "energy_management", Name: "  Plant Energy  ", Config: config})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if instance.ID == uuid.Nil || instance.Name != "Plant Energy" || instance.Enabled || instance.ConfigVersion != 3 {
		t.Fatalf("Create() = %#v", instance)
	}
	if string(instance.Config) != `{"logger_id":"logger-a","tariff":4.2}` || validated != string(instance.Config) {
		t.Errorf("canonical config = %s, validated = %s", instance.Config, validated)
	}
	if string(config) != `{"tariff":4.2,"logger_id":"logger-a"}` {
		t.Errorf("Create() mutated input config to %s", config)
	}

	enabled := true
	defaultConfig, err := service.Create(context.Background(), CreateInput{Type: "energy_management", Name: "Default Config", Enabled: &enabled})
	if err != nil {
		t.Fatalf("Create(default config) error = %v", err)
	}
	if !defaultConfig.Enabled || string(defaultConfig.Config) != `{}` {
		t.Errorf("default config instance = %#v", defaultConfig)
	}
	manifests := service.Types()
	manifests[0].Name = "Changed"
	if service.Types()[0].Name != "Energy Management" {
		t.Error("Types() returned mutable registry state")
	}
}

func TestServiceResolvesImmutableOutputDescriptorsForPersistedInstance(t *testing.T) {
	t.Parallel()

	definition := validRegistryDefinition("energy_management")
	definition.manifest.Capabilities = []Capability{CapabilityPluginOutputsPublish}
	definition.manifest.Outputs = []OutputDescriptor{validOutputDescriptor("demand_kw")}
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	service, _ := NewService(repository, registry)
	instance, err := service.Create(context.Background(), CreateInput{Type: "energy_management", Name: "Plant", Config: Config(`{}`)})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	descriptors, err := service.OutputDescriptors(context.Background(), instance.ID)
	if err != nil || len(descriptors) != 1 || descriptors[0].Key != "demand_kw" {
		t.Fatalf("OutputDescriptors() = %#v, %v", descriptors, err)
	}
	descriptors[0].Name = "Changed"
	again, err := service.OutputDescriptors(context.Background(), instance.ID)
	if err != nil || again[0].Name != "Metric" {
		t.Fatalf("OutputDescriptors(second) = %#v, %v", again, err)
	}
	if _, err := service.OutputDescriptors(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("OutputDescriptors(nil ID) error = %v", err)
	}
	if _, err := service.OutputDescriptors(context.Background(), uuid.New()); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("OutputDescriptors(missing) error = %v", err)
	}
}

func TestServiceRejectsInvalidDefinitionsConfigsAndSingleInstanceDuplicates(t *testing.T) {
	t.Parallel()

	validationError := errors.New("missing logger")
	energy := validRegistryDefinition("energy_management")
	energy.validate = func(config Config) error {
		if strings.Contains(string(config), "reject") {
			return validationError
		}
		return nil
	}
	single := validRegistryDefinition("single_plugin")
	single.manifest.MultipleInstances = false
	registry, err := NewRegistry(energy, single)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	service, err := NewService(newPluginMemoryRepository(), registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	invalidInputs := []CreateInput{
		{Type: "unknown", Name: "Unknown", Config: Config(`{}`)},
		{Type: "energy_management", Name: " ", Config: Config(`{}`)},
		{Type: "energy_management", Name: strings.Repeat("x", maxPluginNameLength+1), Config: Config(`{}`)},
		{Type: "energy_management", Name: "Array", Config: Config(`[]`)},
		{Type: "energy_management", Name: "Null", Config: Config(`null`)},
		{Type: "energy_management", Name: "Malformed", Config: Config(`{"value":`)},
	}
	for _, input := range invalidInputs {
		if _, err := service.Create(context.Background(), input); err == nil {
			t.Errorf("Create(%#v) succeeded", input)
		}
	}
	if _, err := service.Create(context.Background(), CreateInput{Type: "energy_management", Name: "Rejected", Config: Config(`{"reject":true}`)}); !errors.Is(err, ErrInvalidConfig) || !errors.Is(err, validationError) {
		t.Fatalf("Create(rejected config) error = %v", err)
	}
	if _, err := service.Create(context.Background(), CreateInput{Type: "single_plugin", Name: "Only", Config: Config(`{}`)}); err != nil {
		t.Fatalf("Create(single) error = %v", err)
	}
	if _, err := service.Create(context.Background(), CreateInput{Type: "single_plugin", Name: "Second", Config: Config(`{}`)}); !errors.Is(err, ErrMultipleInstancesNotAllowed) {
		t.Fatalf("Create(second single) error = %v", err)
	}
}

func TestServiceSerializesConcurrentSingleInstanceCreation(t *testing.T) {
	t.Parallel()

	definition := validRegistryDefinition("single_plugin")
	definition.manifest.MultipleInstances = false
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	service, err := NewService(newPluginMemoryRepository(), registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	const attempts = 16
	start := make(chan struct{})
	results := make(chan error, attempts)
	var waitGroup sync.WaitGroup
	for index := 0; index < attempts; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, createErr := service.Create(context.Background(), CreateInput{Type: "single_plugin", Name: "Instance " + string(rune('A'+index)), Config: Config(`{}`)})
			results <- createErr
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(results)
	successes := 0
	duplicates := 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrMultipleInstancesNotAllowed):
			duplicates++
		default:
			t.Errorf("Create() unexpected error = %v", result)
		}
	}
	if successes != 1 || duplicates != attempts-1 {
		t.Errorf("successes/duplicates = %d/%d", successes, duplicates)
	}
}

func TestServiceUpdatesListsEnablesDeletesAndPropagatesErrors(t *testing.T) {
	t.Parallel()

	definition := validRegistryDefinition("energy_management")
	definition.manifest.ConfigVersion = 2
	registry, err := NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := newPluginMemoryRepository()
	service, err := NewService(repository, registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	instance, err := service.Create(context.Background(), CreateInput{Type: "energy_management", Name: "Original", Config: Config(`{"logger":"a"}`)})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	name := " Updated "
	enabled := true
	updated, err := service.Update(context.Background(), instance.ID, UpdateInput{Name: &name, Enabled: &enabled, Config: Config(`{"tariff":5,"logger":"b"}`)})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Updated" || !updated.Enabled || updated.Type != "energy_management" || updated.ConfigVersion != 2 || string(updated.Config) != `{"logger":"b","tariff":5}` {
		t.Errorf("updated = %#v", updated)
	}

	repository.mutate(instance.ID, func(stored *Instance) {
		stored.Enabled = false
		stored.ConfigVersion = 1
	})
	if _, err := service.SetEnabled(context.Background(), instance.ID, true); !errors.Is(err, ErrConfigVersionMismatch) {
		t.Fatalf("SetEnabled(old config) error = %v", err)
	}
	updated, err = service.Update(context.Background(), instance.ID, UpdateInput{Enabled: &enabled, Config: Config(`{"logger":"migrated"}`)})
	if err != nil || !updated.Enabled || updated.ConfigVersion != 2 {
		t.Fatalf("Update(migrate and enable) = %#v, %v", updated, err)
	}

	result, err := service.List(context.Background(), ListInput{Search: " Updated "})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if result.Page != 1 || result.PerPage != defaultInstancePerPage || result.Total != 1 || repository.lastList.Search != "Updated" {
		t.Errorf("List() = %#v, input = %#v", result, repository.lastList)
	}
	unknown := Type("unknown")
	if _, err := service.List(context.Background(), ListInput{Type: &unknown}); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("List(unknown type) error = %v", err)
	}
	if _, err := service.List(context.Background(), ListInput{PerPage: maxInstancePerPage + 1}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("List(large page) error = %v", err)
	}
	if _, err := service.Get(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Get(nil) error = %v", err)
	}
	if _, err := service.Update(context.Background(), uuid.Nil, UpdateInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Update(nil) error = %v", err)
	}
	if err := service.Delete(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Delete(nil) error = %v", err)
	}
	if err := service.Delete(context.Background(), instance.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(context.Background(), instance.ID); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("Get(deleted) error = %v", err)
	}

	repository.err = errors.New("database unavailable")
	if _, err := service.List(context.Background(), ListInput{}); !errors.Is(err, repository.err) {
		t.Errorf("List(repository failure) error = %v", err)
	}
}

type pluginMemoryRepository struct {
	mutex    sync.Mutex
	items    map[uuid.UUID]Instance
	lastList ListInput
	err      error
}

func newPluginMemoryRepository() *pluginMemoryRepository {
	return &pluginMemoryRepository{items: make(map[uuid.UUID]Instance)}
}

func (repository *pluginMemoryRepository) Create(_ context.Context, instance *Instance) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return repository.err
	}
	if instance.ID == uuid.Nil {
		instance.ID = uuid.New()
	}
	now := time.Now().UTC()
	instance.CreatedAt = now
	instance.UpdatedAt = now
	repository.items[instance.ID] = cloneTestInstance(*instance)
	return nil
}

func (repository *pluginMemoryRepository) Find(_ context.Context, id uuid.UUID) (*Instance, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return nil, repository.err
	}
	instance, exists := repository.items[id]
	if !exists {
		return nil, ErrInstanceNotFound
	}
	cloned := cloneTestInstance(instance)
	return &cloned, nil
}

func (repository *pluginMemoryRepository) List(_ context.Context, input ListInput) (*ListResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return nil, repository.err
	}
	repository.lastList = input
	items := repository.filteredLocked(input, false)
	start := (input.Page - 1) * input.PerPage
	if start > len(items) {
		start = len(items)
	}
	end := start + input.PerPage
	if end > len(items) {
		end = len(items)
	}
	data := append([]Instance(nil), items[start:end]...)
	totalPages := 0
	if len(items) > 0 {
		totalPages = (len(items) + input.PerPage - 1) / input.PerPage
	}
	return &ListResult{Data: data, Page: input.Page, PerPage: input.PerPage, Total: int64(len(items)), TotalPages: totalPages}, nil
}

func (repository *pluginMemoryRepository) ListEnabled(_ context.Context) ([]Instance, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return nil, repository.err
	}
	return repository.filteredLocked(ListInput{}, true), nil
}

func (repository *pluginMemoryRepository) Update(_ context.Context, instance *Instance) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return repository.err
	}
	stored, exists := repository.items[instance.ID]
	if !exists {
		return ErrInstanceNotFound
	}
	stored.Name = instance.Name
	stored.Enabled = instance.Enabled
	stored.Config = cloneConfig(instance.Config)
	stored.ConfigVersion = instance.ConfigVersion
	stored.UpdatedAt = time.Now().UTC()
	repository.items[instance.ID] = stored
	return nil
}

func (repository *pluginMemoryRepository) Delete(_ context.Context, id uuid.UUID) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if repository.err != nil {
		return repository.err
	}
	if _, exists := repository.items[id]; !exists {
		return ErrInstanceNotFound
	}
	delete(repository.items, id)
	return nil
}

func (repository *pluginMemoryRepository) mutate(id uuid.UUID, mutation func(*Instance)) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	instance := repository.items[id]
	mutation(&instance)
	repository.items[id] = cloneTestInstance(instance)
}

func (repository *pluginMemoryRepository) filteredLocked(input ListInput, enabledOnly bool) []Instance {
	items := make([]Instance, 0, len(repository.items))
	for _, instance := range repository.items {
		if enabledOnly && !instance.Enabled {
			continue
		}
		if input.Type != nil && instance.Type != *input.Type {
			continue
		}
		if input.Enabled != nil && instance.Enabled != *input.Enabled {
			continue
		}
		if input.Search != "" && !strings.Contains(strings.ToLower(instance.Name), strings.ToLower(input.Search)) {
			continue
		}
		items = append(items, cloneTestInstance(instance))
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].CreatedAt.Equal(items[right].CreatedAt) {
			return items[left].ID.String() < items[right].ID.String()
		}
		return items[left].CreatedAt.Before(items[right].CreatedAt)
	})
	return items
}

func cloneTestInstance(instance Instance) Instance {
	instance.Config = cloneConfig(instance.Config)
	return instance
}
