package credential

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

type Type string

const TypeMQTT Type = "mqtt"

const (
	MQTTUsernameSlot       = "mqtt.username"
	MQTTPasswordSlot       = "mqtt.password"
	MQTTCustomCASlot       = "mqtt.custom_ca"
	MQTTClientIdentitySlot = "mqtt.client_identity"
)

var (
	ErrNotFound       = errors.New("Credential Profile not found")
	ErrNameExists     = errors.New("Credential Profile name already exists")
	ErrInUse          = errors.New("Credential Profile is in use")
	ErrInvalid        = errors.New("invalid Credential Profile")
	ErrInvalidInput   = errors.New("invalid Credential Profile input")
	ErrUnsupported    = errors.New("unsupported Credential Profile type")
	ErrRepository     = errors.New("Credential Profile repository is required")
	ErrSecretService  = errors.New("Credential Profile secret service is required")
	ErrVault          = errors.New("Credential Profile vault is required")
	ErrPublisherStore = errors.New("Data Publisher credential lookup is required")
)

type Profile struct {
	ID             uuid.UUID                  `gorm:"type:uuid;primaryKey" json:"id"`
	Type           Type                       `gorm:"type:varchar(32);not null" json:"type"`
	Name           string                     `gorm:"type:varchar(100);not null" json:"name"`
	Description    *string                    `gorm:"type:text" json:"description,omitempty"`
	SecretRevision uint64                     `gorm:"column:secret_revision;->;-:migration" json:"secret_revision"`
	UsageCount     int                        `gorm:"column:usage_count;->;-:migration" json:"usage_count"`
	Secrets        []publisher.SecretMetadata `gorm:"-" json:"-"`
	CreatedAt      time.Time                  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time                  `gorm:"autoUpdateTime" json:"updated_at"`
}

type CreateInput struct {
	Type        Type
	Name        string
	Description *string
}

type UpdateInput struct {
	Name        *string
	Description publisher.OptionalPublisherDescription
}

type ListInput struct {
	Type   *Type
	Search string
}

type Repository interface {
	Create(context.Context, *Profile) error
	Find(context.Context, uuid.UUID) (*Profile, error)
	List(context.Context, ListInput) ([]Profile, error)
	Update(context.Context, *Profile) error
	Delete(context.Context, uuid.UUID) error
}

type PublisherStore interface {
	Find(context.Context, uuid.UUID) (*publisher.Publisher, error)
}

func ExpectedSecretKind(profileType Type, slot string) (publisher.SecretKind, error) {
	if profileType != TypeMQTT {
		return "", ErrUnsupported
	}
	switch slot {
	case MQTTUsernameSlot, MQTTPasswordSlot:
		return publisher.SecretKindOpaque, nil
	case MQTTCustomCASlot:
		return publisher.SecretKindCACertificate, nil
	case MQTTClientIdentitySlot:
		return publisher.SecretKindClientIdentity, nil
	default:
		return "", ErrInvalidInput
	}
}
