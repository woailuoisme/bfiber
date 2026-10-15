package middleware_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
	"bfiber/internal/platform/errs"
)

func TestCircuitBreaker_NormalOperation(t *testing.T) {
	t.Parallel()

	m := middleware.New(middleware.Deps{})
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/healthy", m.CircuitBreaker(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/healthy", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "ok", string(body))
	}
}

func TestCircuitBreaker_TripsOpenAndRecovers(t *testing.T) {
	t.Parallel()

	m := middleware.New(middleware.Deps{})
	cb := m.NewCircuitBreaker(middleware.CircuitBreakerConfig{
		FailureThreshold: 2,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	shouldFail := true
	app.Get("/flaky", m.CircuitBreaker(middleware.CircuitBreakerConfig{
		FailureThreshold: 2,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 1,
	}), func(c fiber.Ctx) error {
		if shouldFail {
			return errors.New("upstream failed")
		}
		return c.SendString("recovered")
	})

	// 1. 连续产生 2 次失败
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/flaky", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
	}

	// 2. 达到阈值，熔断器打开，第 3 次请求直接被拦截返回 503
	req := httptest.NewRequest(http.MethodGet, "/flaky", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeServiceUnavailable), errResp.Code)

	// 3. 等待超时恢复
	time.Sleep(60 * time.Millisecond)
	shouldFail = false

	// 4. 半开探测请求成功，熔断器恢复关闭
	req = httptest.NewRequest(http.MethodGet, "/flaky", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "recovered", string(body))

	// 验证 cb handle API
	assert.False(t, cb.IsOpen())
	cb.ForceOpen()
	assert.True(t, cb.IsOpen())
	cb.Reset()
	assert.False(t, cb.IsOpen())
}

func TestCircuitBreaker_HealthHandler(t *testing.T) {
	t.Parallel()

	cb := middleware.NewCircuitBreaker()
	app := fiber.New()
	app.Get("/health/cb", cb.HealthHandler())

	req := httptest.NewRequest(http.MethodGet, "/health/cb", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var stats map[string]any
	require.NoError(t, json.Unmarshal(bodyBytes, &stats))
	assert.Equal(t, string(middleware.StateClosed), stats["state"])
}
