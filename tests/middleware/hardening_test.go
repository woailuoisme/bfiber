package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
)

// TestRecovery 锁定 panic 边界的对外行为：状态码统一为 500，响应体不带任何内部细节。
func TestRecovery(t *testing.T) {
	mw := middleware.New(middleware.Deps{})

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
