package publisher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewPublisherServiceValidatesDependencies(t *testing.T) {
	t.Parallel()

	repository := newSourceTestPublisherRepository()
	resolver := &sourceTestResolver{}
	definitions, _ := NewDefaultDefinitionRegistry()
	var nilRepository *sourceTestPublisherRepository
	var nilResolver *sourceTestResolver
	tests := []struct {
		name        string
		repository  Repository
		resolver    SourceResolver
		definitions *DefinitionRegistry
		want        error
	}{
		{name: "repository", resolver: resolver, definitions: definitions, want: ErrRepositoryRequired},
		{name: "typed nil repository", repository: nilRepository, resolver: resolver, definitions: definitions, want: ErrRepositoryRequired},
		{name: "resolver", repository: repository, definitions: definitions, want: ErrSourceResolverRequired},
		{name: "typed nil resolver", repository: repository, resolver: nilResolver, definitions: definitions, want: ErrSourceResolverRequired},
		{name: "definitions", repository: repository, resolver: resolver, want: ErrDefinitionRegistryRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.repository, test.resolver, test.definitions); !errors.Is(err, test.want) {
				t.Fatalf("NewService() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPublisherServiceCRUDNormalizesAndRevalidatesSources(t *testing.T) {
	t.Parallel()

	service, repository, resolver, tagReference, outputReference := newSourceTestPublisherService(t)
	description := "  External plant snapshot  "
	enabled := true
	created, err := service.Create(context.Background(), CreateInput{
		Type: TypeHTTPServer, Name: "  Plant API  ", Description: &description, Enabled: &enabled,
		Sources: []SourceSelection{{Alias: "voltage", Reference: tagReference}, {Alias: "energy", Reference: outputReference}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == uuid.Nil || created.Type != TypeHTTPServer || created.Name != "Plant API" || created.Description == nil || *created.Description != "External plant snapshot" || !created.Enabled || string(created.Config) != defaultHTTPConfigJSON || created.ConfigVersion != 3 || created.SourceCount != 2 {
		t.Fatalf("created Publisher = %#v", created)
	}
	if resolver.calls != 1 || len(resolver.last) != 2 {
		t.Errorf("resolver calls = %d, selections %#v", resolver.calls, resolver.last)
	}
	created.Config[0] = '['
	created.Sources[0].Alias = "mutated"
	found, err := service.Get(context.Background(), created.ID)
	if err != nil || string(found.Config) != defaultHTTPConfigJSON || found.Sources[0].Alias != "voltage" {
		t.Fatalf("Get() aliases caller state: %#v, %v", found, err)
	}

	name := "Plant Snapshot API"
	replacementSources := []SourceSelection{{Alias: "energy", Reference: outputReference}}
	updated, err := service.Update(context.Background(), created.ID, UpdateInput{
		Name: &name, Description: OptionalPublisherDescription{Set: true}, Sources: &replacementSources,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Type != TypeHTTPServer || updated.Name != name || updated.Description != nil || len(updated.Sources) != 1 || updated.Sources[0].Alias != "energy" {
		t.Fatalf("updated Publisher = %#v", updated)
	}
	if resolver.calls != 2 {
		t.Errorf("resolver calls after update = %d", resolver.calls)
	}

	resolver.err = errors.New("source backend unavailable")
	disabled, err := service.SetEnabled(context.Background(), created.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("SetEnabled(false) = %#v, %v", disabled, err)
	}
	if resolver.calls != 2 {
		t.Errorf("disable unexpectedly resolved sources %d times", resolver.calls)
	}
	if _, err := service.SetEnabled(context.Background(), created.ID, true); !errors.Is(err, resolver.err) {
		t.Errorf("SetEnabled(true) error = %v", err)
	}
	resolver.err = nil

	typeFilter := TypeHTTPServer
	result, err := service.List(context.Background(), ListInput{Type: &typeFilter, Search: "Snapshot", Page: 1, PerPage: 10})
	if err != nil || result.Total != 1 || len(result.Data) != 1 {
		t.Fatalf("List() = %#v, %v", result, err)
	}
	if types := service.Types(); len(types) != 3 {
		t.Errorf("Types() = %#v", types)
	}
	if err := service.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repository.Find(context.Background(), created.ID); !errors.Is(err, ErrPublisherNotFound) {
		t.Errorf("Find(deleted) error = %v", err)
	}
}

func TestPublisherServiceRejectsInvalidConfigSourcesAndCompatibility(t *testing.T) {
	t.Parallel()

	service, repository, resolver, tagReference, outputReference := newSourceTestPublisherService(t)
	validSources := []SourceSelection{{Alias: "value", Reference: tagReference}}
	for _, test := range []struct {
		name  string
		input CreateInput
		want  error
	}{
		{name: "unsupported type", input: CreateInput{Type: "future", Name: "Future", Sources: validSources}, want: ErrUnsupportedPublisherType},
		{name: "blank name", input: CreateInput{Type: TypeMQTT, Sources: validSources}, want: ErrInvalidInput},
		{name: "long name", input: CreateInput{Type: TypeMQTT, Name: strings.Repeat("x", 101), Sources: validSources}, want: ErrInvalidInput},
		{name: "array config", input: CreateInput{Type: TypeMQTT, Name: "Array", Config: Config(`[]`), Sources: validSources}, want: ErrInvalidPublisherConfig},
		{name: "future config", input: CreateInput{Type: TypeMQTT, Name: "Future Config", Config: Config(`{"broker":"later"}`), Sources: validSources}, want: ErrInvalidPublisherConfig},
		{name: "large config", input: CreateInput{Type: TypeMQTT, Name: "Large", Config: Config(strings.Repeat(" ", maxPublisherConfigBytes+1)), Sources: validSources}, want: ErrInvalidPublisherConfig},
		{name: "no sources", input: CreateInput{Type: TypeMQTT, Name: "Empty"}, want: ErrInvalidSourceSelection},
		{name: "duplicate alias", input: CreateInput{Type: TypeMQTT, Name: "Aliases", Sources: []SourceSelection{{Alias: "value", Reference: tagReference}, {Alias: "value", Reference: outputReference}}}, want: ErrDuplicateSourceAlias},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(context.Background(), test.input); !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}

	stringReference := PluginOutputSource(uuid.New(), "status")
	resolver.descriptors[stringReference] = SourceDescriptor{Reference: stringReference, Name: "Status", SchemaVersion: 1, DataType: SourceDataTypeString, PeriodKind: SourcePeriodInstantaneous, Enabled: true}
	stringSources := []SourceSelection{{Alias: "status", Reference: stringReference}}
	if _, err := service.Create(context.Background(), CreateInput{Type: TypeModbusTCPServer, Name: "Modbus", Sources: stringSources}); !errors.Is(err, ErrIncompatibleSource) {
		t.Errorf("Modbus string source error = %v", err)
	}
	if _, err := service.Create(context.Background(), CreateInput{Type: TypeMQTT, Name: "MQTT", Sources: stringSources}); err != nil {
		t.Errorf("MQTT string source error = %v", err)
	}

	created, err := service.Create(context.Background(), CreateInput{Type: TypeHTTPServer, Name: "Stale", Sources: validSources})
	if err != nil {
		t.Fatalf("Create(stale) error = %v", err)
	}
	repository.setConfigVersion(created.ID, 99)
	if _, err := service.SetEnabled(context.Background(), created.ID, true); !errors.Is(err, ErrConfigVersionMismatch) {
		t.Errorf("stale enable error = %v", err)
	}
	config := Config(`{}`)
	if _, err := service.Update(context.Background(), created.ID, UpdateInput{Config: &config}); err != nil {
		t.Errorf("upgrading config error = %v", err)
	}

	onChange := Config(`{"trigger":{"mode":"on_change","source_alias":"missing"}}`)
	if _, err := service.Update(context.Background(), created.ID, UpdateInput{Config: &onChange}); !errors.Is(err, ErrInvalidPublisherConfig) {
		t.Errorf("missing trigger source error = %v", err)
	}
	onChange = Config(`{"trigger":{"mode":"on_change","source_alias":"value"}}`)
	if _, err := service.Update(context.Background(), created.ID, UpdateInput{Config: &onChange}); err != nil {
		t.Errorf("valid trigger source error = %v", err)
	}
}

func TestPublisherServiceValidatesListAndResolverIntegrity(t *testing.T) {
	t.Parallel()

	service, _, resolver, tagReference, _ := newSourceTestPublisherService(t)
	selection := SourceSelection{Alias: "value", Reference: tagReference}
	invalidType := Type("future")
	for _, input := range []ListInput{
		{Type: &invalidType},
		{Page: -1},
		{PerPage: -1},
		{PerPage: maxPublishersPerPage + 1},
		{Search: strings.Repeat("x", maxPublisherSearchLength+1)},
	} {
		if _, err := service.List(context.Background(), input); err == nil {
			t.Errorf("List(%#v) succeeded", input)
		}
	}

	resolver.omit = true
	if _, err := service.Create(context.Background(), CreateInput{Type: TypeHTTPServer, Name: "Omitted", Sources: []SourceSelection{selection}}); !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("omitted source error = %v", err)
	}
	resolver.omit = false
	resolver.mismatch = true
	if _, err := service.Create(context.Background(), CreateInput{Type: TypeHTTPServer, Name: "Mismatch", Sources: []SourceSelection{selection}}); !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("mismatched source error = %v", err)
	}
	if _, err := (*Service)(nil).Get(context.Background(), uuid.New()); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nil service Get() error = %v", err)
	}
	if err := service.Delete(nil, uuid.New()); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nil context Delete() error = %v", err)
	}
}

func newSourceTestPublisherService(t *testing.T) (*Service, *sourceTestPublisherRepository, *sourceTestResolver, SourceReference, SourceReference) {
	t.Helper()
	tagReference := TagSource(uuid.New())
	outputReference := PluginOutputSource(uuid.New(), "today.energy_kwh")
	resolver := &sourceTestResolver{descriptors: map[SourceReference]SourceDescriptor{
		tagReference:    {Reference: tagReference, Name: "Voltage", SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous, Enabled: true},
		outputReference: {Reference: outputReference, Name: "Today energy", OwnerName: "Plant Energy", SchemaVersion: 1, DataType: SourceDataTypeFloat64, Unit: "kWh", PeriodKind: SourcePeriodWindowed, Enabled: true},
	}}
	repository := newSourceTestPublisherRepository()
	definitions, err := NewDefaultDefinitionRegistry()
	if err != nil {
		t.Fatalf("NewDefaultDefinitionRegistry() error = %v", err)
	}
	service, err := NewService(repository, resolver, definitions)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository, resolver, tagReference, outputReference
}

type sourceTestResolver struct {
	descriptors map[SourceReference]SourceDescriptor
	err         error
	calls       int
	last        []SourceSelection
	omit        bool
	mismatch    bool
}

func (resolver *sourceTestResolver) Resolve(ctx context.Context, selections []SourceSelection) ([]ResolvedSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolver.calls++
	resolver.last = append([]SourceSelection(nil), selections...)
	if resolver.err != nil {
		return nil, resolver.err
	}
	resolved := make([]ResolvedSource, 0, len(selections))
	for _, selection := range selections {
		descriptor, exists := resolver.descriptors[selection.Reference]
		if !exists {
			return nil, ErrSourceNotFound
		}
		resolved = append(resolved, ResolvedSource{Alias: selection.Alias, Descriptor: descriptor})
	}
	if resolver.omit && len(resolved) > 0 {
		resolved = resolved[:len(resolved)-1]
	}
	if resolver.mismatch && len(resolved) > 0 {
		resolved[0].Alias = "different"
	}
	return resolved, nil
}

type sourceTestPublisherRepository struct {
	mu       sync.Mutex
	entities map[uuid.UUID]Publisher
}

func newSourceTestPublisherRepository() *sourceTestPublisherRepository {
	return &sourceTestPublisherRepository{entities: make(map[uuid.UUID]Publisher)}
}

func (repository *sourceTestPublisherRepository) Create(_ context.Context, entity *Publisher) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, current := range repository.entities {
		if strings.EqualFold(current.Name, entity.Name) {
			return ErrPublisherNameExists
		}
	}
	if entity.ID == uuid.Nil {
		entity.ID = uuid.New()
	}
	now := time.Now().UTC()
	entity.CreatedAt = now
	entity.UpdatedAt = now
	repository.entities[entity.ID] = clonePublisher(*entity)
	return nil
}

func (repository *sourceTestPublisherRepository) Find(_ context.Context, id uuid.UUID) (*Publisher, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	entity, exists := repository.entities[id]
	if !exists {
		return nil, ErrPublisherNotFound
	}
	cloned := clonePublisher(entity)
	return &cloned, nil
}

func (repository *sourceTestPublisherRepository) List(_ context.Context, input ListInput) (*ListResult, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	data := make([]Publisher, 0)
	for _, entity := range repository.entities {
		if input.Type != nil && entity.Type != *input.Type || input.Enabled != nil && entity.Enabled != *input.Enabled || input.Search != "" && !strings.Contains(strings.ToLower(entity.Name), strings.ToLower(input.Search)) {
			continue
		}
		data = append(data, clonePublisher(entity))
	}
	return &ListResult{Data: data, Page: input.Page, PerPage: input.PerPage, Total: int64(len(data)), TotalPages: 1}, nil
}

func (repository *sourceTestPublisherRepository) ListEnabled(ctx context.Context) ([]Publisher, error) {
	result, err := repository.List(ctx, ListInput{Enabled: boolPointerPublisher(true), Page: 1, PerPage: 100})
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (repository *sourceTestPublisherRepository) Update(_ context.Context, entity *Publisher) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.entities[entity.ID]
	if !exists {
		return ErrPublisherNotFound
	}
	for id, other := range repository.entities {
		if id != entity.ID && strings.EqualFold(other.Name, entity.Name) {
			return ErrPublisherNameExists
		}
	}
	updated := clonePublisher(*entity)
	updated.Type = current.Type
	updated.CreatedAt = current.CreatedAt
	updated.UpdatedAt = time.Now().UTC()
	repository.entities[entity.ID] = updated
	return nil
}

func (repository *sourceTestPublisherRepository) Delete(_ context.Context, id uuid.UUID) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.entities[id]; !exists {
		return ErrPublisherNotFound
	}
	delete(repository.entities, id)
	return nil
}

func (repository *sourceTestPublisherRepository) setConfigVersion(id uuid.UUID, version uint) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	entity := repository.entities[id]
	entity.ConfigVersion = version
	repository.entities[id] = entity
}

func boolPointerPublisher(value bool) *bool { return &value }
