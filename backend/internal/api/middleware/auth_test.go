package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thefuriousowl/iot-edge/internal/service"
)

type fakeAccessTokenParser struct {
	claims       *service.TokenClaims
	err          error
	callCount    int
	receivedRaw  string
	receivedType string
}

func (f *fakeAccessTokenParser) ParseToken(
	tokenString string,
	expectedType string,
) (*service.TokenClaims, error) {
	f.callCount++
	f.receivedRaw = tokenString
	f.receivedType = expectedType
	return f.claims, f.err
}

func TestRequireAuth_ValidAccessTokenStoresAuthLocalsAndCallsNext(t *testing.T) {
	userID := uuid.New()
	claims := &service.TokenClaims{
		Username:         "admin",
		RegisteredClaims: jwtClaims(userID.String()),
	}

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		t.Run(scheme, func(t *testing.T) {
			parser := &fakeAccessTokenParser{claims: claims}
			app := fiber.New()
			var (
				handlerCalled bool
				gotUserID     uuid.UUID
				gotUsername   string
				gotClaims     *service.TokenClaims
			)

			app.Get("/protected", RequireAuth(parser), func(c *fiber.Ctx) error {
				handlerCalled = true
				var ok bool
				gotUserID, ok = c.Locals(LocalUserID).(uuid.UUID)
				if !ok {
					t.Error("LocalUserID is not a uuid.UUID")
				}
				gotUsername, ok = c.Locals(LocalUsername).(string)
				if !ok {
					t.Error("LocalUsername is not a string")
				}
				gotClaims, ok = c.Locals(LocalClaims).(*service.TokenClaims)
				if !ok {
					t.Error("LocalClaims is not *service.TokenClaims")
				}
				return c.SendStatus(fiber.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set(fiber.HeaderAuthorization, scheme+" test.jwt.value")
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("app.Test() error: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusNoContent {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusNoContent)
			}
			if !handlerCalled {
				t.Fatal("protected handler was not called")
			}
			if parser.callCount != 1 {
				t.Errorf("ParseToken() calls = %d, want 1", parser.callCount)
			}
			if parser.receivedRaw != "test.jwt.value" {
				t.Errorf("ParseToken() token = %q, want test JWT value", parser.receivedRaw)
			}
			if parser.receivedType != service.TokenTypeAccess {
				t.Errorf("ParseToken() expected type = %q, want %q", parser.receivedType, service.TokenTypeAccess)
			}
			if gotUserID != userID {
				t.Errorf("LocalUserID = %s, want %s", gotUserID, userID)
			}
			if gotUsername != claims.Username {
				t.Errorf("LocalUsername = %q, want %q", gotUsername, claims.Username)
			}
			if gotClaims != claims {
				t.Error("LocalClaims does not contain the parser claims")
			}
		})
	}
}

func TestRequireAuth_InvalidAuthorizationHeaderReturnsUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "missing header"},
		{name: "wrong scheme", header: "Basic test.jwt.value"},
		{name: "missing token", header: "Bearer"},
		{name: "extra fields", header: "Bearer test.jwt.value extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &fakeAccessTokenParser{}
			response, handlerCalled := performProtectedRequest(t, parser, tt.header)
			defer response.Body.Close()

			assertUnauthorized(t, response)
			if handlerCalled {
				t.Error("protected handler was called")
			}
			if parser.callCount != 0 {
				t.Errorf("ParseToken() calls = %d, want 0", parser.callCount)
			}
		})
	}
}

func TestRequireAuth_InvalidClaimsReturnUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		parser *fakeAccessTokenParser
	}{
		{
			name:   "parser error",
			parser: &fakeAccessTokenParser{err: errors.New("test parser error")},
		},
		{
			name:   "nil claims",
			parser: &fakeAccessTokenParser{},
		},
		{
			name: "invalid user ID",
			parser: &fakeAccessTokenParser{claims: &service.TokenClaims{
				Username:         "admin",
				RegisteredClaims: jwtClaims("not-a-uuid"),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, handlerCalled := performProtectedRequest(t, tt.parser, "Bearer test.jwt.value")
			defer response.Body.Close()

			assertUnauthorized(t, response)
			if handlerCalled {
				t.Error("protected handler was called")
			}
			if tt.parser.callCount != 1 {
				t.Errorf("ParseToken() calls = %d, want 1", tt.parser.callCount)
			}
		})
	}
}

func jwtClaims(subject string) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{Subject: subject}
}

func performProtectedRequest(
	t *testing.T,
	parser AccessTokenParser,
	authorization string,
) (*http.Response, bool) {
	t.Helper()

	app := fiber.New()
	handlerCalled := false
	app.Get("/protected", RequireAuth(parser), func(c *fiber.Ctx) error {
		handlerCalled = true
		return c.SendStatus(fiber.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if authorization != "" {
		request.Header.Set(fiber.HeaderAuthorization, authorization)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	return response, handlerCalled
}

func assertUnauthorized(t *testing.T, response *http.Response) {
	t.Helper()

	if response.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
	}
	if got := response.Header.Get(fiber.HeaderWWWAuthenticate); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want %q", got, "Bearer")
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding unauthorized response: %v", err)
	}
	if body.Error.Code != "AUTH004" || body.Error.Message != "Invalid token" {
		t.Errorf("error response = %#v, want AUTH004 Invalid token", body.Error)
	}
}
