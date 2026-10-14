package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware/global"
)

// TestSecurityHeaders 锁定安全响应头这道防线，以及 HSTS 只在生产下发的边界。
func TestSecurityHeaders(t *testing.T) {
	serve := func(production bool) *http.Response {
		mw := global.New(global.Deps{Production: production})
		app := fiber.New()
		app.Use(mw.SecurityHeaders())
		app.Get("/ping", func(c fiber.Ctx) error {
			return c.SendString("pong")
		})

		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		return resp
	}

	t.Run("基础防护头始终下发", func(t *testing.T) {
		resp := serve(false)
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
		assert.Equal(t, "none", resp.Header.Get("X-Permitted-Cross-Domain-Policies"))
		assert.Equal(t, "camera=(), microphone=(), geolocation=(), payment=()", resp.Header.Get("Permissions-Policy"))
	})

	t.Run("HSTS 只在生产下发", func(t *testing.T) {
		respDev := serve(false)
		defer func() { _ = respDev.Body.Close() }()
		assert.Empty(t, respDev.Header.Get("Strict-Transport-Security"))

		respProd := serve(true)
		defer func() { _ = respProd.Body.Close() }()
		assert.Equal(t, "max-age=31536000; includeSubDomains; preload", respProd.Header.Get("Strict-Transport-Security"))
	})
}

// TestRecovery 锁定 panic 边界的对外行为：状态码统一为 500，响应体不带任何内部细节。
func TestRecovery(t *testing.T) {
	mw := global.New(global.Deps{})

	app := fiber.New(fiber.Config{
		ErrorHandler: mw.ErrorHandler(),
	})
	// 链序与生产一致：Recovery 只负责恢复并记日志，500 由更外层的 ErrorHandler 统一渲染
	app.Use(mw.Recovery())
	app.Get("/boom", func(_ fiber.Ctx) error {
		panic("secret-internal-detail")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(bodyBytes)

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, bodyStr, "INTERNAL_SERVER_ERROR")
	assert.NotContains(t, bodyStr, "secret-internal-detail",
		"panic 详情只能进日志，绝不能出现在响应体里")
}
