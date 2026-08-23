package plugin

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrInstanceNotFound   = errors.New("plugin instance not found")
	ErrInstanceNameExists = errors.New("plugin instance name already exists")
	ErrInvalidInstance    = errors.New("invalid plugin instance")
)

type ListInput struct {
	Type    *Type
	Enabled *bool
	Search  string
	Page    int
	PerPage int
}

type ListResult struct {
	Data       []Instance
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	Create(context.Context, *Instance) error
	Find(context.Context, uuid.UUID) (*Instance, error)
	List(context.Context, ListInput) (*ListResult, error)
	ListEnabled(context.Context) ([]Instance, error)
	Update(context.Context, *Instance) error
	Delete(context.Context, uuid.UUID) error
}
