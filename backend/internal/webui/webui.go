package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
)

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

//go:embed assets
var embeddedAssets embed.FS

type asset struct {
	content []byte
	etag    string
}

func RegisterEmbedded(app *fiber.App) error {
	assets, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return fmt.Errorf("open embedded frontend: %w", err)
	}
	return Register(app, assets)
}

func Register(app *fiber.App, filesystem fs.FS) error {
	if app == nil || filesystem == nil {
		return errors.New("frontend application and filesystem are required")
	}
	loaded := make(map[string]asset)
	err := fs.WalkDir(filesystem, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasPrefix(path.Base(name), "_") {
			return nil
		}
		content, err := fs.ReadFile(filesystem, name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		loaded[name] = asset{content: content, etag: `"` + hex.EncodeToString(digest[:]) + `"`}
		return nil
	})
	if err != nil {
		return fmt.Errorf("load embedded frontend: %w", err)
	}
	if _, exists := loaded["index.html"]; !exists {
		return errors.New("embedded frontend index.html is missing; run the frontend sync script")
	}

	app.Use(func(c *fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}
		requestPath := c.Path()
		if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") {
			return c.Next()
		}
		name := strings.TrimPrefix(requestPath, "/")
		if name == "" {
			name = "index.html"
		}
		name = path.Clean(name)
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
			return c.SendStatus(fiber.StatusNotFound)
		}
		selected, exists := loaded[name]
		if !exists {
			if path.Ext(name) != "" {
				return c.SendStatus(fiber.StatusNotFound)
			}
			name = "index.html"
			selected = loaded[name]
		}
		c.Set(fiber.HeaderContentSecurityPolicy, contentSecurityPolicy)
		c.Set(fiber.HeaderETag, selected.etag)
		if c.Get(fiber.HeaderIfNoneMatch) == selected.etag {
			return c.SendStatus(fiber.StatusNotModified)
		}
		if name == "index.html" {
			c.Set(fiber.HeaderCacheControl, "no-store")
		} else if strings.HasPrefix(name, "assets/") {
			c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
		} else {
			c.Set(fiber.HeaderCacheControl, "public, max-age=3600")
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType != "" {
			c.Set(fiber.HeaderContentType, contentType)
		}
		if c.Method() == fiber.MethodHead {
			c.Set(fiber.HeaderContentLength, fmt.Sprintf("%d", len(selected.content)))
			return c.SendStatus(fiber.StatusOK)
		}
		return c.Send(selected.content)
	})
	return nil
}
