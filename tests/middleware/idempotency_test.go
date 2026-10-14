package middleware_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
	"bfiber/internal/middleware/global"
	"bfiber/internal/platform/cache"
)

// fakeMemCacheStore 是用于测试 cache.Store 到 fiber.Storage 桥接的内存缓存桩。
type fakeMemCacheStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newFakeMemCacheStore() *fakeMemCacheStore {
	return &fakeMemCacheStore{data: make(map[string]string)}
}

func (s *fakeMemCacheStore) Driver() string { return "memory" }

func (s *fakeMemCacheStore) Ping(_ context.Context) error { return nil }

func (s *fakeMemCacheStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val, ok := s.data[key]
	if !ok {
		return "", cache.ErrNotFound
	}
	return val, nil
}

func (s *fakeMemCacheStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *fakeMemCacheStore) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.data[key]; exists {
		return false, nil
	}
	s.data[key] = value
	return true, nil
}

func (s *fakeMemCacheStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.data[key]
	return exists, nil
}

func (s *fakeMemCacheStore) Increment(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 1, nil
}

func (s *fakeMemCacheStore) TTL(_ context.Context, _ string) (time.Duration, error) {
	return time.Hour, nil
}

func (s *fakeMemCacheStore) Delete(_ context.Context, keys ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for _, k := range keys {
		if _, exists := s.data[k]; exists {
			delete(s.data, k)
			count++
		}
	}
	return count, nil
}

func (s *fakeMemCacheStore) DeleteIfMatch(_ context.Context, key, expected string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if val, ok := s.data[key]; ok && val == expected {
		delete(s.data, key)
		return true, nil
	}
	return false, nil
}

func (s *fakeMemCacheStore) Remember(
	ctx context.Context,
	key string,
	ttl time.Duration,
	load func(context.Context) (string, error),
) (string, error) {
	val, err := s.Get(ctx, key)
	if err == nil {
		return val, nil
	}
	loaded, err := load(ctx)
	if err != nil {
		return "", err
	}
	_ = s.Set(ctx, key, loaded, ttl)
	return loaded, nil
}

func (s *fakeMemCacheStore) Close() error { return nil }

func setupIdempotencyApp(store any, opts ...global.IdempotencyOption) *fiber.App {
	m := global.New(global.Deps{})
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})
	app.Use(global.NewIdempotency(store, opts...))
	return app
}

// TestIdempotency_DefaultPermissive_NoHeader 宽松模式下，未带幂等标头直接放行且不回放。
func TestIdempotency_DefaultPermissive_NoHeader(t *testing.T) {
	store := newFakeMemCacheStore()
	app := setupIdempotencyApp(store)

	callCount := 0
	app.Post("/orders", func(c fiber.Ctx) error {
		callCount++
		return c.JSON(fiber.Map{"status": "created"})
	})

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"item":"book"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)

		require.Equal(t, fiber.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get(global.HeaderIdempotentReplayed))
	}

	assert.Equal(t, 2, callCount, "未带幂等标头时每次请求均应真实执行")
}

// TestIdempotency_SuccessAndReplay 验证标准 36 字符 UUID 幂等键首次执行与二次透明回放。
func TestIdempotency_SuccessAndReplay(t *testing.T) {
	store := newFakeMemCacheStore()
	app := setupIdempotencyApp(store)

	callCount := 0
	app.Post("/payments", func(c fiber.Ctx) error {
		callCount++
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"order_id": "ord_123", "amount": 100})
	})

	const validUUID = "a0000000-0000-0000-0000-000000000001"
	body := `{"amount":100,"currency":"USD"}`

	// 第一次请求：真实执行
	req1 := httptest.NewRequest(http.MethodPost, "/payments", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set(global.AltHeaderIdempotencyKey, validUUID)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer func() { _ = resp1.Body.Close() }()

	require.Equal(t, fiber.StatusCreated, resp1.StatusCode)
	assert.Empty(t, resp1.Header.Get(global.HeaderIdempotentReplayed))
	assert.Equal(t, 1, callCount)

	body1, err := io.ReadAll(resp1.Body)
	require.NoError(t, err)

	// 第二次请求：携带相同 Key 与 Body，命中缓存回放
	req2 := httptest.NewRequest(http.MethodPost, "/payments", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set(global.AltHeaderIdempotencyKey, validUUID)
	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()

	require.Equal(t, fiber.StatusCreated, resp2.StatusCode)
	assert.Equal(t, "true", resp2.Header.Get(global.HeaderIdempotentReplayed), "二次请求应返回回放指示头")

	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, string(body1), string(body2))
	assert.Equal(t, 1, callCount, "后端 Handler 不应被再次调用")
}

// TestIdempotency_InvalidKeyHeader 验证携带非 36 字符非法 UUID 时被官方中间件校验拦截。
func TestIdempotency_InvalidKeyHeader(t *testing.T) {
	store := newFakeMemCacheStore()
	app := setupIdempotencyApp(store)

	app.Post("/orders", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{}`))
	req.Header.Set(global.AltHeaderIdempotencyKey, "invalid-short-key")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode, "非法长度幂等键应当返回 400 Bad Request")
}

// TestIdempotency_SafeMethodSkipped 验证 GET/HEAD 等安全请求自动跳过幂等性处理。
func TestIdempotency_SafeMethodSkipped(t *testing.T) {
	store := newFakeMemCacheStore()
	app := setupIdempotencyApp(store)

	callCount := 0
	app.Get("/status", func(c fiber.Ctx) error {
		callCount++
		return c.JSON(fiber.Map{"count": callCount})
	})

	const validUUID = "b0000000-0000-0000-0000-000000000002"

	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set(global.AltHeaderIdempotencyKey, validUUID)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)

		require.Equal(t, fiber.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get(global.HeaderIdempotentReplayed))
	}

	assert.Equal(t, 2, callCount, "安全方法不进行幂等缓存，两次请求均需执行")
}

// TestIdempotency_CustomHeader 验证自定义标头配置项正常生效。
func TestIdempotency_CustomHeader(t *testing.T) {
	store := newFakeMemCacheStore()
	app := setupIdempotencyApp(store, global.WithIdempotencyKeyHeader(global.HeaderIdempotencyKey))

	callCount := 0
	app.Post("/custom-header", func(c fiber.Ctx) error {
		callCount++
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"called": callCount})
	})

	const validUUID = "c0000000-0000-0000-0000-000000000003"
	body := `{"param":"val"}`

	// 第一次请求
	req1 := httptest.NewRequest(http.MethodPost, "/custom-header", bytes.NewBufferString(body))
	req1.Header.Set(global.HeaderIdempotencyKey, validUUID)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp1.StatusCode)
	assert.Empty(t, resp1.Header.Get(global.HeaderIdempotentReplayed))

	// 第二次请求
	req2 := httptest.NewRequest(http.MethodPost, "/custom-header", bytes.NewBufferString(body))
	req2.Header.Set(global.HeaderIdempotencyKey, validUUID)
	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp2.StatusCode)
	assert.Equal(t, "true", resp2.Header.Get(global.HeaderIdempotentReplayed))
	assert.Equal(t, 1, callCount)
}

// TestIdempotency_RouteLevelWrapper 验证业务中间件层级包装器工作正常。
func TestIdempotency_RouteLevelWrapper(t *testing.T) {
	store := newFakeMemCacheStore()
	m := middleware.New(middleware.Deps{
		Cache: store,
	})

	app := fiber.New()
	callCount := 0
	app.Post("/route-level", m.Idempotency(global.WithIdempotencyLifetime(5*time.Minute)), func(c fiber.Ctx) error {
		callCount++
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"done": true})
	})

	const validUUID = "d0000000-0000-0000-0000-000000000004"
	body := `{"op":"transfer"}`

	req1 := httptest.NewRequest(http.MethodPost, "/route-level", bytes.NewBufferString(body))
	req1.Header.Set(global.AltHeaderIdempotencyKey, validUUID)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp1.StatusCode)

	req2 := httptest.NewRequest(http.MethodPost, "/route-level", bytes.NewBufferString(body))
	req2.Header.Set(global.AltHeaderIdempotencyKey, validUUID)
	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp2.StatusCode)
	assert.Equal(t, "true", resp2.Header.Get(global.HeaderIdempotentReplayed))
	assert.Equal(t, 1, callCount)
}
