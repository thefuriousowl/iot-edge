package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/auth"
	authhttp "github.com/thefuriousowl/iot-edge/internal/auth/http"
)

func TestProtectedAPIRouteMatrixRejectsUnauthenticatedRequests(t *testing.T) {
	app := newApp("http://localhost:5173")
	authService := &apiRouteAuthService{}
	authhttp.RegisterAuthRoutes(app.Group("/api"), authhttp.NewAuthHandler(authService), authService)
	protected := app.Group("/api", authhttp.RequireAuth(authService))
	registerProtectedAPIRoutes(protected, protectedAPIHandlers{})
	const id = "11111111-1111-4111-8111-111111111111"
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/system/internet-status"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodPost, "/api/auth/change-password"},
		{http.MethodGet, "/api/vgateways/"},
		{http.MethodPost, "/api/vgateways/"},
		{http.MethodPost, "/api/vgateways/test"},
		{http.MethodGet, "/api/vgateways/" + id},
		{http.MethodPut, "/api/vgateways/" + id},
		{http.MethodDelete, "/api/vgateways/" + id},
		{http.MethodPost, "/api/vgateways/" + id + "/connect"},
		{http.MethodPost, "/api/vgateways/" + id + "/disconnect"},
		{http.MethodPost, "/api/vgateways/" + id + "/test"},
		{http.MethodGet, "/api/vgateways/" + id + "/status"},
		{http.MethodGet, "/api/vgateways/" + id + "/devices/"},
		{http.MethodPost, "/api/vgateways/" + id + "/devices/"},
		{http.MethodGet, "/api/devices/"},
		{http.MethodGet, "/api/devices/" + id},
		{http.MethodPut, "/api/devices/" + id},
		{http.MethodDelete, "/api/devices/" + id},
		{http.MethodGet, "/api/devices/" + id + "/datasources"},
		{http.MethodPost, "/api/devices/" + id + "/datasources"},
		{http.MethodPost, "/api/devices/" + id + "/datasources/preview"},
		{http.MethodGet, "/api/datasources/" + id},
		{http.MethodPut, "/api/datasources/" + id},
		{http.MethodDelete, "/api/datasources/" + id},
		{http.MethodPost, "/api/datasources/" + id + "/preview"},
		{http.MethodGet, "/api/datasources/" + id + "/raw"},
		{http.MethodGet, "/api/datasources/" + id + "/stream"},
		{http.MethodGet, "/api/sse/tags"},
		{http.MethodGet, "/api/tags/"},
		{http.MethodPost, "/api/tags/"},
		{http.MethodPost, "/api/tags/preview"},
		{http.MethodPost, "/api/tags/validate-expression"},
		{http.MethodGet, "/api/tags/" + id},
		{http.MethodGet, "/api/tags/" + id + "/values"},
		{http.MethodGet, "/api/tags/" + id + "/stream"},
		{http.MethodPut, "/api/tags/" + id},
		{http.MethodDelete, "/api/tags/" + id},
		{http.MethodPost, "/api/tags/" + id + "/preview"},
		{http.MethodGet, "/api/data-management/overview"},
		{http.MethodGet, "/api/data-loggers/"},
		{http.MethodPost, "/api/data-loggers/"},
		{http.MethodGet, "/api/data-loggers/" + id + "/history"},
		{http.MethodGet, "/api/data-loggers/" + id + "/query"},
		{http.MethodGet, "/api/data-loggers/" + id + "/retention"},
		{http.MethodGet, "/api/data-loggers/" + id + "/retention/preview"},
		{http.MethodPost, "/api/data-loggers/" + id + "/retention/cleanup"},
		{http.MethodGet, "/api/data-loggers/" + id},
		{http.MethodPut, "/api/data-loggers/" + id},
		{http.MethodDelete, "/api/data-loggers/" + id},
		{http.MethodGet, "/api/plugin-types"},
		{http.MethodGet, "/api/plugins/"},
		{http.MethodPost, "/api/plugins/"},
		{http.MethodPost, "/api/plugins/" + id + "/enable"},
		{http.MethodPost, "/api/plugins/" + id + "/disable"},
		{http.MethodPost, "/api/plugins/" + id + "/restart"},
		{http.MethodGet, "/api/plugins/" + id + "/status"},
		{http.MethodGet, "/api/plugins/" + id},
		{http.MethodPut, "/api/plugins/" + id},
		{http.MethodDelete, "/api/plugins/" + id},
		{http.MethodGet, "/api/plugins/" + id + "/energy/overview"},
		{http.MethodGet, "/api/plugins/" + id + "/energy/history"},
		{http.MethodGet, "/api/plugins/" + id + "/energy/export.csv"},
		{http.MethodGet, "/api/plugins/" + id + "/energy/stream"},
		{http.MethodGet, "/api/reports/"},
		{http.MethodPost, "/api/reports/"},
		{http.MethodGet, "/api/reports/" + id + "/query"},
		{http.MethodGet, "/api/reports/" + id + "/export.csv"},
		{http.MethodGet, "/api/reports/" + id},
		{http.MethodPut, "/api/reports/" + id},
		{http.MethodDelete, "/api/reports/" + id},
		{http.MethodGet, "/api/credentials/"},
		{http.MethodPost, "/api/credentials/"},
		{http.MethodPut, "/api/credentials/" + id + "/secrets/mqtt.password"},
		{http.MethodDelete, "/api/credentials/" + id + "/secrets/mqtt.password"},
		{http.MethodGet, "/api/credentials/" + id},
		{http.MethodPut, "/api/credentials/" + id},
		{http.MethodDelete, "/api/credentials/" + id},
		{http.MethodGet, "/api/publisher-types"},
		{http.MethodGet, "/api/publisher-sources"},
		{http.MethodPost, "/api/publisher-payloads/validate"},
		{http.MethodGet, "/api/data-publishers/"},
		{http.MethodPost, "/api/data-publishers/"},
		{http.MethodPost, "/api/data-publishers/" + id + "/enable"},
		{http.MethodPost, "/api/data-publishers/" + id + "/disable"},
		{http.MethodPost, "/api/data-publishers/" + id + "/restart"},
		{http.MethodPost, "/api/data-publishers/" + id + "/probe-listener"},
		{http.MethodGet, "/api/data-publishers/" + id + "/status"},
		{http.MethodGet, "/api/data-publishers/" + id + "/diagnostics"},
		{http.MethodPost, "/api/data-publishers/" + id + "/test-connection"},
		{http.MethodGet, "/api/data-publishers/" + id},
		{http.MethodPut, "/api/data-publishers/" + id},
		{http.MethodDelete, "/api/data-publishers/" + id},
	}

	for _, route := range routes {
		name := fmt.Sprintf("%s %s", route.method, route.path)
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response, err := app.Test(request, -1)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("status = %d, want %d", response.StatusCode, fiber.StatusUnauthorized)
			}
		})
	}
}

func TestPublicAPIRouteMatrixAllowsUnauthenticatedRequests(t *testing.T) {
	app := newApp("http://localhost:5173")
	authService := &apiRouteAuthService{}
	authhttp.RegisterAuthRoutes(app.Group("/api"), authhttp.NewAuthHandler(authService), authService)
	tests := []struct {
		method string
		path   string
		body   string
		cookie *http.Cookie
		status int
	}{
		{method: http.MethodGet, path: "/api/health", status: fiber.StatusOK},
		{method: http.MethodGet, path: "/api/auth/setup/status", status: fiber.StatusOK},
		{method: http.MethodPost, path: "/api/auth/setup", body: `{"username":"admin","password":"SecureP@ss123","confirm_password":"SecureP@ss123"}`, status: fiber.StatusCreated},
		{method: http.MethodPost, path: "/api/auth/login", body: `{"username":"admin","password":"SecureP@ss123"}`, status: fiber.StatusOK},
		{method: http.MethodPost, path: "/api/auth/refresh", cookie: &http.Cookie{Name: "refresh_token", Value: "refresh-token"}, status: fiber.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			}
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			response, err := app.Test(request, -1)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Errorf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

type apiRouteAuthService struct{ auth.AuthService }

func (*apiRouteAuthService) IsSetupRequired(context.Context) (bool, error) { return true, nil }

func (*apiRouteAuthService) Setup(context.Context, string, string) (*auth.User, error) {
	return &auth.User{ID: uuid.New(), Username: "admin"}, nil
}

func (*apiRouteAuthService) Login(context.Context, string, string) (*auth.TokenPair, error) {
	return &auth.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

func (*apiRouteAuthService) Refresh(context.Context, string) (*auth.AccessTokenResult, error) {
	return &auth.AccessTokenResult{AccessToken: "access-token", AccessExpiresAt: time.Now().Add(15 * time.Minute)}, nil
}
