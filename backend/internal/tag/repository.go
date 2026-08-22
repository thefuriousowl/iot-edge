package tag

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrTagNotFound       = errors.New("tag not found")
	ErrTagNameExists     = errors.New("tag name already exists")
	ErrDatasourceMissing = errors.New("datasource not found")
	ErrDependencyMissing = errors.New("tag dependency not found")
	ErrInvalidTag        = errors.New("invalid tag")
	ErrInvalidDependency = errors.New("invalid tag dependency")
)

type ListInput struct {
	Type         *Type
	DataType     *DataType
	Enabled      *bool
	DatasourceID *uuid.UUID
	Search       string
	Page         int
	PerPage      int
}

type ListResult struct {
	Data       []Tag
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
}

type Repository interface {
	Create(context.Context, *Tag, []uuid.UUID) error
	Find(context.Context, uuid.UUID) (*Tag, error)
	List(context.Context, ListInput) (*ListResult, error)
	Update(context.Context, *Tag, []uuid.UUID) error
	Delete(context.Context, uuid.UUID) error
	ListDependencies(context.Context) ([]Dependency, error)
}
