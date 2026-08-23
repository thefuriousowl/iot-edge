package credentialhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/credential"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
)

func TestCredentialHandlerCRUDAndWriteOnlySecretMetadata(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC()
	service := &credentialHandlerService{profile: &credential.Profile{ID: id, Type: credential.TypeMQTT, Name: "Plant MQTT", Secrets: []publisher.SecretMetadata{}, CreatedAt: now, UpdatedAt: now}}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))

	response, _ := app.Test(httptest.NewRequest("GET", "/api/credentials?type=mqtt", nil))
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("list status = %d", response.StatusCode)
	}

	response, _ = app.Test(httptest.NewRequest("PUT", "/api/credentials/"+id.String()+"/secrets/mqtt.password", strings.NewReader(`{"value_base64":"bmV2ZXItcmV0dXJuZWQ="}`)))
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || strings.Contains(string(body), "never-returned") || strings.Contains(string(body), "bmV2ZXItcmV0dXJuZWQ=") {
		t.Fatalf("put secret response = %d %s", response.StatusCode, body)
	}
	if string(service.material.Opaque) != "never-returned" || service.slot != credential.MQTTPasswordSlot {
		t.Fatalf("put secret slot=%q material=%q", service.slot, service.material.Opaque)
	}
	var metadata struct {
		Slot string `json:"slot"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil || metadata.Slot != credential.MQTTPasswordSlot {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}

	response, _ = app.Test(httptest.NewRequest("DELETE", "/api/credentials/"+id.String()+"/secrets/mqtt.password", nil))
	if response.StatusCode != fiber.StatusNoContent || service.deleted != credential.MQTTPasswordSlot {
		t.Fatalf("delete secret = %d %q", response.StatusCode, service.deleted)
	}

	response, _ = app.Test(httptest.NewRequest("PUT", "/api/credentials/"+id.String()+"/secrets/unknown", strings.NewReader(`{"value_base64":"dGVzdA=="}`)))
	if response.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("unknown slot status = %d", response.StatusCode)
	}

	service.profile.Type = credential.TypeHTTP
	response, _ = app.Test(httptest.NewRequest("PUT", "/api/credentials/"+id.String()+"/secrets/http.api_key", strings.NewReader(`{"value_base64":"aHR0cC1rZXk="}`)))
	if response.StatusCode != fiber.StatusOK || service.slot != credential.HTTPAPIKeySlot || string(service.material.Opaque) != "http-key" {
		t.Fatalf("HTTP secret response = %d slot=%q material=%q", response.StatusCode, service.slot, service.material.Opaque)
	}
}

type credentialHandlerService struct {
	profile  *credential.Profile
	slot     string
	material publisher.SecretMaterial
	deleted  string
}

func (service *credentialHandlerService) Create(context.Context, credential.CreateInput) (*credential.Profile, error) {
	return service.profile, nil
}
func (service *credentialHandlerService) Get(context.Context, uuid.UUID) (*credential.Profile, error) {
	return service.profile, nil
}
func (service *credentialHandlerService) List(context.Context, credential.ListInput) ([]credential.Profile, error) {
	return []credential.Profile{*service.profile}, nil
}
func (service *credentialHandlerService) Update(context.Context, uuid.UUID, credential.UpdateInput) (*credential.Profile, error) {
	return service.profile, nil
}
func (service *credentialHandlerService) Delete(context.Context, uuid.UUID) error { return nil }
func (service *credentialHandlerService) PutSecret(_ context.Context, id uuid.UUID, slot string, material publisher.SecretMaterial) (*publisher.SecretMetadata, error) {
	service.slot, service.material = slot, material.Clone()
	now := time.Now().UTC()
	return &publisher.SecretMetadata{PublisherID: id, Reference: publisher.SecretReference{Name: slot}, Kind: publisher.SecretKindOpaque, Revision: 1, PublisherRevision: 1, CreatedAt: now, RotatedAt: now}, nil
}
func (service *credentialHandlerService) DeleteSecret(_ context.Context, _ uuid.UUID, slot string) error {
	service.deleted = slot
	return nil
}
