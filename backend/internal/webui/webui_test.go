package webui

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v2"
)

func TestRegisterServesSPAWithCacheAndSecurityBoundaries(t *testing.T) {
	app := fiber.New()
	app.Get("/api/health", func(c *fiber.Ctx) error { return c.SendString("api") })
	err := Register(app, fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><div id=\"root\"></div>")},
		"assets/index-a1b2c3.js": {Data: []byte("console.log('app')")},
		"favicon.svg":            {Data: []byte("<svg></svg>")},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	for _, test := range []struct {
		path, cache, contains string
		status                int
	}{
		{path: "/", status: 200, cache: "no-store", contains: "id=\"root\""},
		{path: "/plugins/energy-1/energy", status: 200, cache: "no-store", contains: "id=\"root\""},
		{path: "/assets/index-a1b2c3.js", status: 200, cache: "public, max-age=31536000, immutable", contains: "console.log"},
		{path: "/favicon.svg", status: 200, cache: "public, max-age=3600", contains: "<svg"},
		{path: "/assets/missing.js", status: 404},
		{path: "/api/health", status: 200, contains: "api"},
	} {
		response, requestErr := app.Test(httptest.NewRequest(http.MethodGet, test.path, nil), -1)
		if requestErr != nil {
			t.Fatalf("GET %s error = %v", test.path, requestErr)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.status || (test.contains != "" && !strings.Contains(string(body), test.contains)) {
			t.Errorf("GET %s = %d %q", test.path, response.StatusCode, body)
		}
		if test.cache != "" && response.Header.Get("Cache-Control") != test.cache {
			t.Errorf("GET %s cache = %q", test.path, response.Header.Get("Cache-Control"))
		}
		if test.path != "/api/health" && test.status == 200 && response.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
			t.Errorf("GET %s CSP = %q", test.path, response.Header.Get("Content-Security-Policy"))
		}
	}
}

func TestRegisterRequiresProductionIndex(t *testing.T) {
	err := Register(fiber.New(), fstest.MapFS{"placeholder.txt": {Data: []byte("placeholder")}})
	if !errors.Is(err, ErrFrontendUnavailable) {
		t.Fatalf("Register() error = %v, want ErrFrontendUnavailable", err)
	}
	if err == nil {
		t.Fatal("Register() succeeded without index.html")
	}
}
