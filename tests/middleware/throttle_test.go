package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"bfiber/internal/config"
	"bfiber/internal/middleware"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/errs"
)

// mockRateLimitStore 模拟基于 fiber.Storage 接口的内存限流存储。
type mockRateLimitStore struct {
	mu   sync.Mutex
	data map[string][]byte
	err  error
}

var _ fiber.Storage = (*mockRateLimitStore)(nil)

func newMockRateLimitStore() *mockRateLimitStore {
	return &mockRateLimitStore{
		data: make(map[string][]byte),
	}
}

func (s *mockRateLimitStore) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.data[key], nil
}

func (s *mockRateLimitStore) GetWithContext(_ context.Context, key string) ([]byte, error) {
	return s.Get(key)
}

func (s *mockRateLimitStore) Set(key string, val []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.data[key] = val
	return nil
}

func (s *mockRateLimitStore) SetWithContext(_ context.Context, key string, val []byte, exp time.Duration) error {
	return s.Set(key, val, exp)
}

func (s *mockRateLimitStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *mockRateLimitStore) DeleteWithContext(_ context.Context, key string) error {
	return s.Delete(key)
}

func (s *mockRateLimitStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string][]byte)
	return nil
}

func (s *mockRateLimitStore) ResetWithContext(_ context.Context) error {
	return s.Reset()
}

func (s *mockRateLimitStore) Close() error {
	return nil
}

// TestRateLimit_Allowed 验证在配额内请求被正常放行并附带标准限流响应头。
func TestRateLimit_Allowed(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New()
	app.Use(middleware.ThrottleWithStore(store, 10, time.Minute, middleware.WithThrottleTier(middleware.TierAnonymous)))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "10", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "9", resp.Header.Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Reset"))
}

// TestRateLimit_Exceeded 验证超出配额时拦截为 429 并附带 Retry-After。
func TestRateLimit_Exceeded(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Use(middleware.ThrottleWithStore(store, 2, time.Minute, middleware.WithThrottleTier(middleware.TierAnonymous)))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// 前 2 次允许
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode)
	}

	// 第 3 次超限 (limit = 2)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "2", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", resp.Header.Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Reset"))
	assert.NotEmpty(t, resp.Header.Get("Retry-After"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(bodyBytes, &body))
	assert.Equal(t, string(errs.CodeRateLimited), body["code"])
}

// TestRateLimit_SlidingWindow 验证滑动窗口策略在请求超过限额时平滑拦截。
func TestRateLimit_SlidingWindow(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Use(middleware.ThrottleWithStore(
		store,
		5,
		time.Minute,
		middleware.WithThrottleTier(middleware.TierAnonymous),
		middleware.WithThrottleKey(func(c fiber.Ctx) string {
			if ip := c.Get("X-Test-IP"); ip != "" {
				return "ip:" + ip
			}
			return "ip:" + c.IP()
		}),
	))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// 发送 5 次请求（均应成功）
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Test-IP", "10.0.0.1")
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should succeed", i+1)
	}

	// 第 6 次请求必须触发 429
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Test-IP", "10.0.0.1")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusTooManyRequests, resp.StatusCode)
}

// TestRateLimit_StoreFailOpen 验证缓存/存储异常时降级放行。
func TestRateLimit_StoreFailOpen(t *testing.T) {
	store := newMockRateLimitStore()
	store.err = errors.New("redis connection refused")

	app := fiber.New()
	app.Use(middleware.ThrottleWithStore(store, 5, time.Minute, middleware.WithThrottleTier(middleware.TierAnonymous)))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	// 存储故障时应当降级放行
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestRateLimit_StoreFailOpen_WithLogger 验证缓存/存储异常时降级放行并记录限频 Warn 告警日志。
func TestRateLimit_StoreFailOpen_WithLogger(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	testLogger := zap.New(core)

	store := newMockRateLimitStore()
	store.err = errors.New("redis connection refused")

	app := fiber.New()
	app.Use(middleware.ThrottleWithStore(
		store,
		5,
		time.Minute,
		middleware.WithThrottleTier(middleware.TierAnonymous),
		middleware.WithThrottleLogger(testLogger),
	))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	allLogs := logs.All()
	require.NotEmpty(t, allLogs)
	assert.Contains(t, allLogs[0].Message, "cache storage error, falling back to fail-open")
}

// TestMiddleware_Throttle_StoreFailOpen_Logs 验证 Middleware 实例通过 Throttle 自动透传 Logger 进行故障记录。
func TestMiddleware_Throttle_StoreFailOpen_Logs(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	testLogger := zap.New(core)

	store := newMockRateLimitStore()
	store.err = errors.New("cluster down")

	m := middleware.New(middleware.Deps{
		Logger: testLogger,
		Limiter: config.LimiterConfig{
			Anonymous: 10,
		},
	})
	m.SetLimiterStore(store)

	app := fiber.New()
	app.Get("/test", m.Throttle(middleware.TierAnonymous), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	allLogs := logs.All()
	require.NotEmpty(t, allLogs)
	assert.Contains(t, allLogs[0].Message, "cache storage error, falling back to fail-open")
}

// TestRateLimit_NilStoreOrZeroLimit 验证存储为空或配额<=0时直接放行。
func TestRateLimit_NilStoreOrZeroLimit(t *testing.T) {
	// 1. nil store
	app1 := fiber.New()
	app1.Use(middleware.ThrottleWithStore(nil, 5, time.Minute, middleware.WithThrottleTier(middleware.TierAnonymous)))
	app1.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp1, err := app1.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp1.StatusCode)

	// 2. 0 limit
	store := newMockRateLimitStore()
	app2 := fiber.New()
	app2.Use(middleware.ThrottleWithStore(store, 0, time.Minute, middleware.WithThrottleTier(middleware.TierAnonymous)))
	app2.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp2, err := app2.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp2.StatusCode)
}

// TestRateLimit_AuthenticatedScope 验证认证用户维度按 UserID 隔离。
func TestRateLimit_AuthenticatedScope(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New()
	// 注入认证用户上下文
	app.Use(func(c fiber.Ctx) error {
		ctx := authentication.With(c.Context(), authentication.Identity{
			UserID: "usr_12345",
		})
		c.SetContext(ctx)
		return c.Next()
	})
	app.Use(middleware.ThrottleWithStore(store, 10, time.Minute, middleware.WithThrottleTier(middleware.TierAuthenticated)))
	app.Get("/test", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	// 验证存储中的 key 包含了 user:usr_12345
	assert.NotEmpty(t, store.data["user:usr_12345"])
}

// TestThrottle_CustomWindow 验证支持任意时间窗口（如 10 秒）。
func TestThrottle_CustomWindow(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New()
	app.Use(middleware.ThrottleWithStore(store, 3, 10*time.Second))
	app.Get("/hourly", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/hourly", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "3", resp.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "2", resp.Header.Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Reset"))
}

// TestThrottle_CustomKey 验证支持自定义分区维度（如按登录账号 email 分区）。
func TestThrottle_CustomKey(t *testing.T) {
	store := newMockRateLimitStore()

	app := fiber.New()
	app.Use(middleware.ThrottleWithStore(
		store,
		5,
		time.Minute,
		middleware.WithThrottleKey(func(c fiber.Ctx) string {
			return "email:" + c.Query("email")
		}),
	))
	app.Get("/login-attempt", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/login-attempt?email=admin@example.com", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, store.data["email:admin@example.com"])
}

// TestThrottle_Tier 验证通过 Middleware.Throttle 挂载特定业务档位（如 TierSensitive）。
func TestThrottle_Tier(t *testing.T) {
	store := newMockRateLimitStore()

	m := middleware.New(middleware.Deps{
		Limiter: config.LimiterConfig{
			Sensitive: 5,
		},
	})
	m.SetLimiterStore(store)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Post("/auth/login", m.Throttle(middleware.TierSensitive), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("X-RateLimit-Limit"))
}

// TestThrottle_ThrottleRate 验证通过 Middleware.ThrottleRate 按自定义限制与周期限流。
func TestThrottle_ThrottleRate(t *testing.T) {
	store := newMockRateLimitStore()

	m := middleware.New(middleware.Deps{})
	m.SetLimiterStore(store)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	// 允许每 1 分钟 2 次请求
	app.Get("/api/fast", m.ThrottleRate(2, time.Minute), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// 第一次正常
	req1 := httptest.NewRequest(http.MethodGet, "/api/fast", nil)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp1.StatusCode)
	assert.Equal(t, "2", resp1.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "1", resp1.Header.Get("X-RateLimit-Remaining"))

	// 第二次正常
	req2 := httptest.NewRequest(http.MethodGet, "/api/fast", nil)
	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp2.StatusCode)
	assert.Equal(t, "0", resp2.Header.Get("X-RateLimit-Remaining"))

	// 第三次触发限流 429
	req3 := httptest.NewRequest(http.MethodGet, "/api/fast", nil)
	resp3, err := app.Test(req3, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusTooManyRequests, resp3.StatusCode)
}

// TestThrottle_CustomResponse 验证自定义超限拦截响应回调。
func TestThrottle_CustomResponse(t *testing.T) {
	store := newMockRateLimitStore()

	m := middleware.New(middleware.Deps{})
	m.SetLimiterStore(store)

	customOpt := middleware.WithThrottleResponse(func(c fiber.Ctx) error {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"custom_error": "please_wait"})
	})

	app := fiber.New()
	app.Get("/custom", m.ThrottleRate(1, time.Minute, customOpt), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req1 := httptest.NewRequest(http.MethodGet, "/custom", nil)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp1.StatusCode)

	req2 := httptest.NewRequest(http.MethodGet, "/custom", nil)
	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusTooManyRequests, resp2.StatusCode)

	bodyBytes, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	var body map[string]string
	require.NoError(t, json.Unmarshal(bodyBytes, &body))
	assert.Equal(t, "please_wait", body["custom_error"])
}

// TestThrottle_DisabledGlobally 验证当 RATELIMIT_ENABLED=false 时，所有限流中间件完全旁路放行。
func TestThrottle_DisabledGlobally(t *testing.T) {
	store := newMockRateLimitStore()

	// 显式将限流总开关设为 false
	m := middleware.New(middleware.Deps{
		Limiter: config.LimiterConfig{
			Enabled:       new(bool),
			Sensitive:     1,
			Anonymous:     1,
			Authenticated: 1,
		},
	})
	m.SetLimiterStore(store)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Post("/auth/login", m.Throttle(middleware.TierSensitive), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	app.Get("/rate", m.ThrottleRate(1, time.Minute), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// 连续发起 5 次敏感登录请求，均应直接放行，且不注入限流响应头
	for i := range 5 {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should succeed", i+1)
		assert.Empty(t, resp.Header.Get("X-RateLimit-Limit"), "headers should not be set when rate limit is disabled")
	}

	// 连续发起 5 次自定义速率请求，同样全量放行
	for i := range 5 {
		req := httptest.NewRequest(http.MethodGet, "/rate", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should succeed", i+1)
		assert.Empty(t, resp.Header.Get("X-RateLimit-Limit"), "headers should not be set when rate limit is disabled")
	}
}

// TestThrottleWithStorage 验证强类型 fiber.Storage 构造器限流行为正常。
func TestThrottleWithStorage(t *testing.T) {
	storage := cache.NewMemoryStorage()
	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/typed-rate", middleware.ThrottleWithStorage(storage, 2, time.Minute), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	for i := range 2 {
		req := httptest.NewRequest(http.MethodGet, "/typed-rate", nil)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode, "request %d should succeed", i+1)
	}

	// 第 3 次请求应被拦截
	req3 := httptest.NewRequest(http.MethodGet, "/typed-rate", nil)
	resp3, err := app.Test(req3, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusTooManyRequests, resp3.StatusCode)
}
