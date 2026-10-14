package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
	"bfiber/internal/middleware/global"
	"bfiber/internal/platform/errs"
)

// TestCircuitBreaker_TripsOnFailures 验证当服务连续失败达到阈值时，断路器进入开启状态并拦截请求为 503。
func TestCircuitBreaker_TripsOnFailures(t *testing.T) {
	mw := middleware.New(middleware.Deps{})
	globalMW := global.New(global.Deps{})

	app := fiber.New(fiber.Config{
		ErrorHandler: globalMW.ErrorHandler(),
	})

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	// 设置失败阈值为 2 次，冷却时间 200ms
	cb := mw.CircuitBreaker(
		middleware.WithFailureThreshold(2),
		middleware.WithBreakerTimeout(200*time.Millisecond),
	)

	app.Get("/fragile", cb, func(c fiber.Ctx) error {
		if shouldFail.Load() {
			return errs.Internal(errors.New("downstream service timeout"))
		}
		return c.SendStatus(fiber.StatusOK)
	})

	// 第 1 次失败 (500)
	req := httptest.NewRequest(http.MethodGet, "/fragile", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)

	// 第 2 次失败 (500，此时达到阈值 2 次)
	req = httptest.NewRequest(http.MethodGet, "/fragile", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)

	// 第 3 次应被断路器直接短路拦截为 503 Service Unavailable
	req = httptest.NewRequest(http.MethodGet, "/fragile", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)

	// 模拟等待冷却时间过去，下游服务恢复
	time.Sleep(250 * time.Millisecond)
	shouldFail.Store(false)

	// 第 4 次进入半开探活，下游成功响应 200 OK
	req = httptest.NewRequest(http.MethodGet, "/fragile", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}
