package publisher

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrPublisherNotFound          = errors.New("Data Publisher not found")
	ErrPublisherNameExists        = errors.New("Data Publisher name already exists")
	ErrInvalidPublisher           = errors.New("invalid Data Publisher")
	ErrInvalidInput               = errors.New("invalid Data Publisher input")
	ErrRepositoryRequired         = errors.New("Data Publisher repository is required")
	ErrSourceResolverRequired     = errors.New("Publisher source resolver is required")
	ErrDefinitionRegistryRequired = errors.New("Publisher definition registry is required")
	ErrConfigVersionMismatch      = errors.New("Publisher config version does not match implementation")
	ErrInvalidPublisherConfig     = errors.New("invalid Data Publisher config")
	ErrUnsupportedPublisherType   = errors.New("unsupported Data Publisher type")
	ErrIncompatibleSource         = errors.New("source is incompatible with Data Publisher type")
)

type ListInput struct {
	Type    *Type
	Enabled *bool
	Search  string
	Page    int
	PerPage int
}

type ListResult struct {
	Data       []Publisher
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	Create(context.Context, *Publisher) error
	Find(context.Context, uuid.UUID) (*Publisher, error)
	List(context.Context, ListInput) (*ListResult, error)
	ListEnabled(context.Context) ([]Publisher, error)
	Update(context.Context, *Publisher) error
	Delete(context.Context, uuid.UUID) error
}

type SourceResolver interface {
	Resolve(context.Context, []SourceSelection) ([]ResolvedSource, error)
}
