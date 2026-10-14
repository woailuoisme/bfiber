package middleware_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware/global"
)

type testPayload struct {
	Message string `json:"message"`
}

// TestBodyMultiPassBinding 验证 Fiber 原生多次读取与绑定 Body 的能力。
func TestBodyMultiPassBinding(t *testing.T) {
	app := fiber.New()

	var firstRead, secondRead testPayload
	var bodyBytes []byte

	app.Post("/multi-pass", func(c fiber.Ctx) error {
		if err := c.Bind().Body(&firstRead); err != nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}

		if err := c.Bind().Body(&secondRead); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		bodyBytes = c.Body()
		return c.SendStatus(fiber.StatusOK)
	})

	body := []byte(`{"message":"hello fiber"}`)
	req := httptest.NewRequest(http.MethodPost, "/multi-pass", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "hello fiber", firstRead.Message)
	assert.Equal(t, "hello fiber", secondRead.Message)
	assert.Equal(t, body, bodyBytes)
}

// TestSecurityHeaders_Injection 验证安全响应头正确注入。
func TestSecurityHeaders_Injection(t *testing.T) {
	m := global.New(global.Deps{
		Production: true,
	})

	app := fiber.New()
	app.Use(m.SecurityHeaders())
	app.Get("/test", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	assert.Equal(t, "none", resp.Header.Get("X-Permitted-Cross-Domain-Policies"))
	assert.Equal(t, "max-age=31536000; includeSubDomains; preload", resp.Header.Get("Strict-Transport-Security"))
}
