package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
)

// TestWithTimeout_WithinBudget 验证请求在超时预算内正常返回 200 OK。
func TestWithTimeout_WithinBudget(t *testing.T) {
	m := middleware.New(middleware.Deps{
		RequestTimeout: 100 * time.Millisecond,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/fast", m.WithTimeout(func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	}))

	req := httptest.NewRequest(http.MethodGet, "/fast", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestWithTimeout_Exceeded 验证超出截止期时返回标准 504 Gateway Timeout。
func TestWithTimeout_Exceeded(t *testing.T) {
	m := middleware.New(middleware.Deps{
		RequestTimeout: 20 * time.Millisecond,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/slow", m.WithTimeout(func(c fiber.Ctx) error {
		time.Sleep(100 * time.Millisecond)
		return c.JSON(fiber.Map{"status": "late"})
	}))

	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusGatewayTimeout, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "GATEWAY_TIMEOUT")
}

// TestWithTimeout_CustomDuration 验证路由级自定义超时时长成功覆写全局默认超时。
func TestWithTimeout_CustomDuration(t *testing.T) {
	m := middleware.New(middleware.Deps{
		RequestTimeout: 20 * time.Millisecond,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	// 覆写超时为 200ms，任务耗时 50ms 顺利在预算内完成
	app.Get("/custom-slow", m.WithTimeout(func(c fiber.Ctx) error {
		time.Sleep(50 * time.Millisecond)
		return c.JSON(fiber.Map{"status": "ok"})
	}, 200*time.Millisecond))

	req := httptest.NewRequest(http.MethodGet, "/custom-slow", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestWithTimeout_StreamingBypass 验证 WebSocket 升级与 SSE 流式请求自动豁免协同式超时。
func TestWithTimeout_StreamingBypass(t *testing.T) {
	m := middleware.New(middleware.Deps{
		RequestTimeout: 20 * time.Millisecond,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/stream", m.WithTimeout(func(c fiber.Ctx) error {
		time.Sleep(50 * time.Millisecond)
		return c.JSON(fiber.Map{"status": "streaming"})
	}))

	// 1. 验证 WebSocket Upgrade 标头豁免超时
	reqWS := httptest.NewRequest(http.MethodGet, "/stream", nil)
	reqWS.Header.Set("Upgrade", "websocket")
	respWS, err := app.Test(reqWS, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, respWS.StatusCode)

	// 2. 验证 SSE Accept: text/event-stream 标头豁免超时
	reqSSE := httptest.NewRequest(http.MethodGet, "/stream", nil)
	reqSSE.Header.Set("Accept", "text/event-stream")
	respSSE, err := app.Test(reqSSE, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, respSSE.StatusCode)
}

// TestTimeout_GroupMiddleware 验证通过洋葱模型 Group 级中间件为 /v1 路由挂载协同式超时控制。
func TestTimeout_GroupMiddleware(t *testing.T) {
	m := middleware.New(middleware.Deps{
		RequestTimeout: 20 * time.Millisecond,
	})
	require.Equal(t, 20*time.Millisecond, m.RequestTimeout())

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	v1 := app.Group("/v1", m.Timeout())
	v1.Get("/fast", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	v1.Get("/slow", func(c fiber.Ctx) error {
		time.Sleep(100 * time.Millisecond)
		return c.JSON(fiber.Map{"status": "late"})
	})

	// 非 /v1 路由（如系统就绪探针）不被超时中间件拦截
	app.Get("/livez", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// 1. 验证 /v1/fast 正常 200 返回
	reqFast := httptest.NewRequest(http.MethodGet, "/v1/fast", nil)
	respFast, err := app.Test(reqFast, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, respFast.StatusCode)

	// 2. 验证 /v1/slow 超时返回 504 Gateway Timeout
	reqSlow := httptest.NewRequest(http.MethodGet, "/v1/slow", nil)
	respSlow, err := app.Test(reqSlow, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusGatewayTimeout, respSlow.StatusCode)

	// 3. 验证 /livez 正常 200 返回
	reqLivez := httptest.NewRequest(http.MethodGet, "/livez", nil)
	respLivez, err := app.Test(reqLivez, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, respLivez.StatusCode)
}
