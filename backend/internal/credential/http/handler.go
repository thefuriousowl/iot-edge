package credentialhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/credential"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

type Service interface {
	Create(context.Context, credential.CreateInput) (*credential.Profile, error)
	Get(context.Context, uuid.UUID) (*credential.Profile, error)
	List(context.Context, credential.ListInput) ([]credential.Profile, error)
	Update(context.Context, uuid.UUID, credential.UpdateInput) (*credential.Profile, error)
	Delete(context.Context, uuid.UUID) error
	PutSecret(context.Context, uuid.UUID, string, publisher.SecretMaterial) (*publisher.SecretMetadata, error)
	DeleteSecret(context.Context, uuid.UUID, string) error
}

type Handler struct{ service Service }

type createRequest struct {
	Type        credential.Type `json:"type"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
}

type updateRequest struct {
	Name        *string         `json:"name"`
	Description json.RawMessage `json:"description"`
}

type secretRequest struct {
	ValueBase64          *string `json:"value_base64"`
	CertificatePEMBase64 *string `json:"certificate_pem_base64"`
	PrivateKeyPEMBase64  *string `json:"private_key_pem_base64"`
}

type secretResponse struct {
	Slot      string               `json:"slot"`
	Kind      publisher.SecretKind `json:"kind"`
	Revision  uint64               `json:"revision"`
	CreatedAt time.Time            `json:"created_at"`
	RotatedAt time.Time            `json:"rotated_at"`
}

type profileResponse struct {
	ID             uuid.UUID        `json:"id"`
	Type           credential.Type  `json:"type"`
	Name           string           `json:"name"`
	Description    *string          `json:"description,omitempty"`
	SecretRevision uint64           `json:"secret_revision"`
	UsageCount     int              `json:"usage_count"`
	Secrets        []secretResponse `json:"secrets"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

func NewHandler(service Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Create(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	var request createRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	profile, err := handler.service.Create(c.UserContext(), credential.CreateInput{Type: request.Type, Name: request.Name, Description: request.Description})
	if err != nil {
		return handleError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(project(profile))
}

func (handler *Handler) Get(c *fiber.Ctx) error {
	id, err := handler.parseID(c)
	if err != nil {
		return validation(c, "Invalid Credential Profile ID")
	}
	profile, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(project(profile))
}

func (handler *Handler) List(c *fiber.Ctx) error {
	if handler == nil || handler.service == nil {
		return unavailable(c)
	}
	input := credential.ListInput{Search: c.Query("search")}
	if raw := c.Query("type"); raw != "" {
		profileType := credential.Type(raw)
		input.Type = &profileType
	}
	profiles, err := handler.service.List(c.UserContext(), input)
	if err != nil {
		return handleError(c, err)
	}
	data := make([]profileResponse, len(profiles))
	for index := range profiles {
		data[index] = project(&profiles[index])
	}
	return c.JSON(fiber.Map{"data": data})
}

func (handler *Handler) Update(c *fiber.Ctx) error {
	id, err := handler.parseID(c)
	if err != nil {
		return validation(c, "Invalid Credential Profile ID")
	}
	var request updateRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	input := credential.UpdateInput{Name: request.Name}
	if request.Description != nil {
		input.Description.Set = true
		if !strings.EqualFold(string(bytes.TrimSpace(request.Description)), "null") {
			var description string
			if err := json.Unmarshal(request.Description, &description); err != nil {
				return validation(c, "Invalid description")
			}
			input.Description.Value = &description
		}
	}
	profile, err := handler.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(project(profile))
}

func (handler *Handler) Delete(c *fiber.Ctx) error {
	id, err := handler.parseID(c)
	if err != nil {
		return validation(c, "Invalid Credential Profile ID")
	}
	if err := handler.service.Delete(c.UserContext(), id); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) PutSecret(c *fiber.Ctx) error {
	id, err := handler.parseID(c)
	if err != nil {
		return validation(c, "Invalid Credential Profile ID")
	}
	slot := c.Params("slot")
	profile, err := handler.service.Get(c.UserContext(), id)
	if err != nil {
		return handleError(c, err)
	}
	kind, err := credential.ExpectedSecretKind(profile.Type, slot)
	if err != nil {
		return validation(c, "Invalid Credential secret slot")
	}
	var request secretRequest
	if err := decode(c.Body(), &request); err != nil {
		return validation(c, "Invalid request body")
	}
	material, err := decodeMaterial(kind, request)
	if err != nil {
		return validation(c, "Invalid Credential secret material")
	}
	defer material.Destroy()
	metadata, err := handler.service.PutSecret(c.UserContext(), id, slot, material)
	if err != nil {
		return handleError(c, err)
	}
	return c.JSON(projectSecret(*metadata))
}

func (handler *Handler) DeleteSecret(c *fiber.Ctx) error {
	id, err := handler.parseID(c)
	if err != nil {
		return validation(c, "Invalid Credential Profile ID")
	}
	if err := handler.service.DeleteSecret(c.UserContext(), id, c.Params("slot")); err != nil {
		return handleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (handler *Handler) parseID(c *fiber.Ctx) (uuid.UUID, error) {
	if handler == nil || handler.service == nil {
		return uuid.Nil, credential.ErrInvalidInput
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, credential.ErrInvalidInput
	}
	return id, nil
}

func project(profile *credential.Profile) profileResponse {
	if profile == nil {
		return profileResponse{Secrets: []secretResponse{}}
	}
	secrets := make([]secretResponse, len(profile.Secrets))
	for index := range profile.Secrets {
		secrets[index] = projectSecret(profile.Secrets[index])
	}
	return profileResponse{
		ID: profile.ID, Type: profile.Type, Name: profile.Name, Description: profile.Description,
		SecretRevision: profile.SecretRevision, UsageCount: profile.UsageCount, Secrets: secrets,
		CreatedAt: profile.CreatedAt.UTC(), UpdatedAt: profile.UpdatedAt.UTC(),
	}
}

func projectSecret(metadata publisher.SecretMetadata) secretResponse {
	return secretResponse{Slot: metadata.Reference.Name, Kind: metadata.Kind, Revision: metadata.Revision, CreatedAt: metadata.CreatedAt.UTC(), RotatedAt: metadata.RotatedAt.UTC()}
}

func decodeMaterial(kind publisher.SecretKind, request secretRequest) (publisher.SecretMaterial, error) {
	decodeValue := func(value *string) ([]byte, error) {
		if value == nil {
			return nil, nil
		}
		return base64.StdEncoding.Strict().DecodeString(*value)
	}
	var material publisher.SecretMaterial
	var err error
	switch kind {
	case publisher.SecretKindOpaque:
		if request.ValueBase64 == nil || request.CertificatePEMBase64 != nil || request.PrivateKeyPEMBase64 != nil {
			return material, credential.ErrInvalidInput
		}
		material.Opaque, err = decodeValue(request.ValueBase64)
	case publisher.SecretKindCACertificate:
		if request.ValueBase64 != nil || request.CertificatePEMBase64 == nil || request.PrivateKeyPEMBase64 != nil {
			return material, credential.ErrInvalidInput
		}
		material.CertificatePEM, err = decodeValue(request.CertificatePEMBase64)
	case publisher.SecretKindClientIdentity:
		if request.ValueBase64 != nil || request.CertificatePEMBase64 == nil || request.PrivateKeyPEMBase64 == nil {
			return material, credential.ErrInvalidInput
		}
		material.CertificatePEM, err = decodeValue(request.CertificatePEMBase64)
		if err == nil {
			material.PrivateKeyPEM, err = decodeValue(request.PrivateKeyPEMBase64)
		}
	default:
		return material, credential.ErrInvalidInput
	}
	if err != nil {
		material.Destroy()
		return publisher.SecretMaterial{}, err
	}
	return material, nil
}

func decode(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return credential.ErrInvalidInput
	}
	return nil
}

func handleError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, credential.ErrNotFound):
		return apiError(c, fiber.StatusNotFound, "CRED001", "Credential Profile not found")
	case errors.Is(err, credential.ErrNameExists):
		return apiError(c, fiber.StatusConflict, "CRED002", "Credential Profile name already exists")
	case errors.Is(err, credential.ErrInUse):
		return apiError(c, fiber.StatusConflict, "CRED003", "Credential Profile is in use")
	case errors.Is(err, publisher.ErrSecretNotFound):
		return apiError(c, fiber.StatusNotFound, "CRED004", "Credential secret was not found")
	case errors.Is(err, publisher.ErrSecretKindMismatch):
		return apiError(c, fiber.StatusConflict, "CRED005", "Credential secret kind cannot be changed")
	case errors.Is(err, credential.ErrInvalidInput), errors.Is(err, credential.ErrInvalid), errors.Is(err, credential.ErrUnsupported), errors.Is(err, publisher.ErrInvalidSecretMaterial), errors.Is(err, publisher.ErrInvalidSecretReference):
		return validation(c, "Invalid Credential Profile configuration")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apiError(c, fiber.StatusServiceUnavailable, "CRED006", "Credential operation was interrupted")
	default:
		return apiError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
	}
}

func unavailable(c *fiber.Ctx) error {
	return apiError(c, fiber.StatusServiceUnavailable, "CRED007", "Credential API is unavailable")
}

func validation(c *fiber.Ctx, message string) error {
	return apiError(c, fiber.StatusBadRequest, "VALIDATION_ERROR", message)
}

func apiError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}
