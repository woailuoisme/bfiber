package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/middleware"
)

// TestCORS_OptionsPreflightShortCircuit 验证 OPTIONS 预检请求返回 204 并且直接短路阻断后续中间件与路由。
func TestCORS_OptionsPreflightShortCircuit(t *testing.T) {
	m := middleware.New(middleware.Deps{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"https://example.com"},
		},
	})

	downstreamExecuted := false
	app := fiber.New()
	app.Use(m.CORS())
	app.Use(func(c fiber.Ctx) error {
		downstreamExecuted = true
		return c.Next()
	})
	app.Post("/api/test", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/test", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "https://example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "OPTIONS")
	assert.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Headers"))
	assert.False(t, downstreamExecuted, "OPTIONS preflight must short-circuit without executing downstream middleware")
}

// TestCORS_WildcardOrigin 验证通配符来源设置及非 OPTIONS 正常请求的头部回显。
func TestCORS_WildcardOrigin(t *testing.T) {
	m := middleware.New(middleware.Deps{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"*"},
		},
	})

	app := fiber.New()
	app.Use(m.CORS())
	app.Get("/api/test", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Origin", "https://other.com")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"), "Wildcard origin must not allow credentials")
}
