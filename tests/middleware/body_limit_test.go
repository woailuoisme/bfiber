package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
	"bfiber/internal/platform/errs"
)

// TestBodyLimit 锁定请求体上限这条安全控制的两端：
// 超限的读取必须拿到 413 REQUEST_TOO_LARGE，
// 上限之内的请求必须照常通过。
func TestBodyLimit(t *testing.T) {
	mw := middleware.New(middleware.Deps{})

	newApp := func() *fiber.App {
		app := fiber.New(fiber.Config{
			ErrorHandler: mw.ErrorHandler(),
		})
		app.Use(mw.BodyLimit())
		app.Post("/echo", func(c fiber.Ctx) error {
			body := c.Body()
			return c.Status(http.StatusOK).JSON(fiber.Map{"size": len(body)})
		})
		return app
	}

	t.Run("超过上限返回 413", func(t *testing.T) {
		// 2 MiB，超过设定的 1 MiB 上限。
		oversized := strings.Repeat("a", 2<<20)
		req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(oversized))
		resp, err := newApp().Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		bodyBytes, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(bodyBytes), string(errs.CodeRequestTooLarge))
	})

	t.Run("上限之内正常通过", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"ok":true}`))
		resp, err := newApp().Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		bodyBytes, _ := io.ReadAll(resp.Body)
		assert.JSONEq(t, `{"size":11}`, string(bodyBytes))
	})
}
