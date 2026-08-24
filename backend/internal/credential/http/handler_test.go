package credentialhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

func TestCredentialHandlerCRUDStrictDTOsAndFilters(t *testing.T) {
	id := uuid.New()
	description := "Broker credentials"
	service := &credentialHandlerService{profile: &credential.Profile{ID: id, Type: credential.TypeMQTT, Name: "Plant MQTT", Description: &description, Secrets: []publisher.SecretMetadata{}}}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))

	response := credentialRequest(t, app, http.MethodPost, "/api/credentials/", `{"type":"mqtt","name":"Plant MQTT","description":"Broker credentials"}`)
	assertCredentialStatus(t, response, fiber.StatusCreated)
	decodeCredentialResponse(t, response, &profileResponse{})
	if service.createInput.Type != credential.TypeMQTT || service.createInput.Name != "Plant MQTT" || service.createInput.Description == nil || *service.createInput.Description != description {
		t.Fatalf("Create input = %#v", service.createInput)
	}

	response = credentialRequest(t, app, http.MethodGet, "/api/credentials/?type=mqtt&search=plant", "")
	assertCredentialStatus(t, response, fiber.StatusOK)
	var list struct {
		Data []profileResponse `json:"data"`
	}
	decodeCredentialResponse(t, response, &list)
	if service.listInput.Type == nil || *service.listInput.Type != credential.TypeMQTT || service.listInput.Search != "plant" || len(list.Data) != 1 || list.Data[0].ID != id || list.Data[0].Secrets == nil {
		t.Fatalf("List input/result = %#v / %#v", service.listInput, list)
	}

	response = credentialRequest(t, app, http.MethodGet, "/api/credentials/"+id.String(), "")
	assertCredentialStatus(t, response, fiber.StatusOK)
	var found profileResponse
	decodeCredentialResponse(t, response, &found)
	if service.id != id || found.ID != id || found.Name != "Plant MQTT" {
		t.Fatalf("Get ID/result = %s / %#v", service.id, found)
	}

	newName := "Plant MQTT Updated"
	service.profile.Name = newName
	service.profile.Description = nil
	response = credentialRequest(t, app, http.MethodPut, "/api/credentials/"+id.String(), `{"name":"Plant MQTT Updated","description":null}`)
	assertCredentialStatus(t, response, fiber.StatusOK)
	decodeCredentialResponse(t, response, &profileResponse{})
	if service.updateInput.Name == nil || *service.updateInput.Name != newName || !service.updateInput.Description.Set || service.updateInput.Description.Value != nil {
		t.Fatalf("Update input = %#v", service.updateInput)
	}

	response = credentialRequest(t, app, http.MethodDelete, "/api/credentials/"+id.String(), "")
	assertCredentialStatus(t, response, fiber.StatusNoContent)
	closeCredentialResponse(t, response)
	if service.deletedID != id {
		t.Fatalf("Delete ID = %s, want %s", service.deletedID, id)
	}
}

func TestCredentialHandlerRejectsInvalidIDsStrictJSONAndSecretMaterial(t *testing.T) {
	id := uuid.New()
	service := &credentialHandlerService{profile: &credential.Profile{ID: id, Type: credential.TypeMQTT, Name: "Strict", Secrets: []publisher.SecretMetadata{}}}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/credentials/not-a-uuid", ""},
		{http.MethodDelete, "/api/credentials/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodPost, "/api/credentials/", `{"type":"mqtt","name":"Strict","unknown":true}`},
		{http.MethodPost, "/api/credentials/", `{"type":"mqtt","name":"Strict"} {"name":"Second"}`},
		{http.MethodPut, "/api/credentials/" + id.String(), `{"description":42}`},
		{http.MethodPut, "/api/credentials/" + id.String() + "/secrets/mqtt.password", `{"value_base64":"not base64!"}`},
		{http.MethodPut, "/api/credentials/" + id.String() + "/secrets/mqtt.password", `{"certificate_pem_base64":"Y2VydA=="}`},
		{http.MethodPut, "/api/credentials/" + id.String() + "/secrets/mqtt.client_identity", `{"certificate_pem_base64":"Y2VydA=="}`},
		{http.MethodPut, "/api/credentials/" + id.String() + "/secrets/unknown", `{"value_base64":"dGVzdA=="}`},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := credentialRequest(t, app, test.method, test.path, test.body)
			assertCredentialError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestCredentialHandlerMapsStableSanitizedErrorsAndUnavailableDependency(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"missing", credential.ErrNotFound, fiber.StatusNotFound, "CRED001"},
		{"name conflict", credential.ErrNameExists, fiber.StatusConflict, "CRED002"},
		{"in use", credential.ErrInUse, fiber.StatusConflict, "CRED003"},
		{"secret missing", publisher.ErrSecretNotFound, fiber.StatusNotFound, "CRED004"},
		{"kind mismatch", publisher.ErrSecretKindMismatch, fiber.StatusConflict, "CRED005"},
		{"invalid", credential.ErrInvalid, fiber.StatusBadRequest, "VALIDATION_ERROR"},
		{"cancelled", context.Canceled, fiber.StatusServiceUnavailable, "CRED006"},
		{"deadline", context.DeadlineExceeded, fiber.StatusServiceUnavailable, "CRED006"},
		{"unknown", errors.New("vault token=do-not-expose"), fiber.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &credentialHandlerService{err: test.err}
			app := fiber.New()
			RegisterRoutes(app.Group("/api"), NewHandler(service))
			response := credentialRequest(t, app, http.MethodGet, "/api/credentials/"+uuid.NewString(), "")
			body := assertCredentialError(t, response, test.status, test.code)
			if strings.Contains(body, "do-not-expose") || strings.Contains(body, "token=") {
				t.Errorf("response leaked internal error: %s", body)
			}
		})
	}

	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(nil))
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/credentials/", ""},
		{http.MethodPost, "/api/credentials/", `{"type":"mqtt","name":"Unavailable"}`},
	} {
		response := credentialRequest(t, app, test.method, test.path, test.body)
		assertCredentialError(t, response, fiber.StatusServiceUnavailable, "CRED007")
	}
}

type credentialHandlerService struct {
	profile     *credential.Profile
	err         error
	id          uuid.UUID
	deletedID   uuid.UUID
	createInput credential.CreateInput
	updateInput credential.UpdateInput
	listInput   credential.ListInput
	slot        string
	material    publisher.SecretMaterial
	deleted     string
}

func (service *credentialHandlerService) Create(_ context.Context, input credential.CreateInput) (*credential.Profile, error) {
	service.createInput = input
	return service.profile, service.err
}
func (service *credentialHandlerService) Get(_ context.Context, id uuid.UUID) (*credential.Profile, error) {
	service.id = id
	return service.profile, service.err
}
func (service *credentialHandlerService) List(_ context.Context, input credential.ListInput) ([]credential.Profile, error) {
	service.listInput = input
	if service.err != nil {
		return nil, service.err
	}
	return []credential.Profile{*service.profile}, nil
}
func (service *credentialHandlerService) Update(_ context.Context, id uuid.UUID, input credential.UpdateInput) (*credential.Profile, error) {
	service.id, service.updateInput = id, input
	return service.profile, service.err
}
func (service *credentialHandlerService) Delete(_ context.Context, id uuid.UUID) error {
	service.deletedID = id
	return service.err
}
func (service *credentialHandlerService) PutSecret(_ context.Context, id uuid.UUID, slot string, material publisher.SecretMaterial) (*publisher.SecretMetadata, error) {
	service.slot, service.material = slot, material.Clone()
	if service.err != nil {
		return nil, service.err
	}
	now := time.Now().UTC()
	return &publisher.SecretMetadata{PublisherID: id, Reference: publisher.SecretReference{Name: slot}, Kind: publisher.SecretKindOpaque, Revision: 1, PublisherRevision: 1, CreatedAt: now, RotatedAt: now}, nil
}
func (service *credentialHandlerService) DeleteSecret(_ context.Context, _ uuid.UUID, slot string) error {
	service.deleted = slot
	return service.err
}

func credentialRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, path, err)
	}
	return response
}

func assertCredentialStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		closeCredentialResponse(t, response)
		t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, want, body)
	}
}

func assertCredentialError(t *testing.T, response *http.Response, status int, code string) string {
	t.Helper()
	assertCredentialStatus(t, response, status)
	body, err := io.ReadAll(response.Body)
	closeCredentialResponse(t, response)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decoding error: %v; body=%s", err, body)
	}
	if envelope.Error.Code != code {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, code)
	}
	return string(body)
}

func decodeCredentialResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer closeCredentialResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

func closeCredentialResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if err := response.Body.Close(); err != nil {
		t.Errorf("closing response: %v", err)
	}
}
