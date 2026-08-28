package asset

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const (
	DefaultAssetsPerPage = 20
	MaxAssetsPerPage     = 100
)

var (
	ErrAssetNotFound        = errors.New("asset not found")
	ErrAssetNameExists      = errors.New("asset name already exists under parent")
	ErrAssetHasDependents   = errors.New("asset has dependent hierarchy or measurements")
	ErrInvalidAssetList     = errors.New("invalid asset list input")
	ErrInvalidBinding       = errors.New("invalid measurement binding")
	ErrBindingSourceExists  = errors.New("measurement source already has an owner")
	ErrBindingSourceMissing = errors.New("measurement binding source not found")
)

type ListInput struct {
	Search  string
	Kind    *Kind
	Enabled *bool
	Page    int
	PerPage int
}

type ListResult struct {
	Data       []Asset `json:"data"`
	Page       int     `json:"page"`
	PerPage    int     `json:"per_page"`
	Total      int64   `json:"total"`
	TotalPages int     `json:"total_pages"`
}

type Repository interface {
	Create(context.Context, *Asset) error
	Find(context.Context, uuid.UUID) (*Asset, error)
	List(context.Context, ListInput) (*ListResult, error)
	ListChildren(context.Context, *uuid.UUID) ([]Asset, error)
	Subtree(context.Context, uuid.UUID) (*TreeNode, error)
	Ancestors(context.Context, uuid.UUID) ([]Asset, error)
	Update(context.Context, *Asset) error
	Move(context.Context, uuid.UUID, *uuid.UUID, int) error
	Delete(context.Context, uuid.UUID) error
	ListBindings(context.Context, uuid.UUID) ([]MeasurementBinding, error)
	ListBindingsByTag(context.Context, uuid.UUID) ([]MeasurementBinding, error)
	ReplaceBindings(context.Context, uuid.UUID, []MeasurementBinding) error
}
