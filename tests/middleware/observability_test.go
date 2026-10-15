package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
)

// TestAccessLog404SingleWarn 验证 404 请求仅输出一条 WARN 访问日志，
// 且错误文本不含内部错误前缀与尾部换行符。
func TestAccessLog404SingleWarn(t *testing.T) {
	var buf bytes.Buffer
	testLogger := logger.NewWithOptions(
		logger.WithLevel("debug"),
		logger.WithChannel("json"),
		logger.WithWriter(&buf),
	)

	mw := middleware.New(middleware.Deps{Logger: testLogger})
	app := fiber.New(fiber.Config{
		ErrorHandler: mw.ErrorHandler(),
	})
	app.Use(
		mw.RequestID(),
		mw.AccessLog(),
	)

	app.Use(func(c fiber.Ctx) error {
		return errs.NotFound("")
	})

	req := httptest.NewRequest(http.MethodGet, "/not-found", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusNotFound, resp.StatusCode)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1, "404 请求必须且仅输出一条结构化访问日志，禁止重复打印告警日志")

	type logEntry struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Status  int    `json:"status"`
		Code    string `json:"code"`
		Error   string `json:"error"`
		Path    string `json:"path"`
		Method  string `json:"method"`
		Stack   string `json:"stack"`
	}

	var entry logEntry
	err = json.Unmarshal([]byte(lines[0]), &entry)
	require.NoError(t, err)

	assert.Equal(t, "warn", entry.Level)
	assert.Equal(t, "access", entry.Message)
	assert.Equal(t, 404, entry.Status)
	assert.Equal(t, "NOT_FOUND", entry.Code)
	assert.Equal(t, "The requested resource was not found", entry.Error)
	assert.NotContains(t, entry.Error, "\n")
	assert.Equal(t, "/not-found", entry.Path)
	assert.Equal(t, "GET", entry.Method)
}

// TestAccessLog500ErrorWithStack 验证 500 服务端内部错误具备调用栈日志与访问日志分工。
func TestAccessLog500ErrorWithStack(t *testing.T) {
	var buf bytes.Buffer
	testLogger := logger.NewWithOptions(
		logger.WithLevel("debug"),
		logger.WithChannel("json"),
		logger.WithWriter(&buf),
	)

	mw := middleware.New(middleware.Deps{Logger: testLogger})
	app := fiber.New(fiber.Config{
		ErrorHandler: mw.ErrorHandler(),
	})
	app.Use(
		mw.RequestID(),
		mw.AccessLog(),
	)

	app.Get("/panic-or-500", func(c fiber.Ctx) error {
		return errs.Internal(errors.New("db connection lost"))
	})

	req := httptest.NewRequest(http.MethodGet, "/panic-or-500", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2, "500 请求应分别输出堆栈异常日志 (request processing failed) 与访问日志 (access)")

	type logEntry struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Status  int    `json:"status"`
		Stack   string `json:"stack"`
	}

	var errLog, accessLog logEntry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &errLog))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &accessLog))

	assert.Equal(t, "error", errLog.Level)
	assert.Equal(t, "request processing failed", errLog.Message)
	assert.NotEmpty(t, errLog.Stack)

	assert.Equal(t, "error", accessLog.Level)
	assert.Equal(t, "access", accessLog.Message)
	assert.Equal(t, 500, accessLog.Status)
}

// TestAccessLog_ProbeSilencing 验证探针路径（/readyz 与 /livez）在正常状态（<400）下静默无日志，
// 但在发生异常（如 500）时依然正常输出日志。
func TestAccessLog_ProbeSilencing(t *testing.T) {
	var buf bytes.Buffer
	testLogger := logger.NewWithOptions(
		logger.WithLevel("debug"),
		logger.WithChannel("json"),
		logger.WithWriter(&buf),
	)

	mw := middleware.New(middleware.Deps{Logger: testLogger})
	app := fiber.New(fiber.Config{
		ErrorHandler: mw.ErrorHandler(),
	})
	app.Use(
		mw.RequestID(),
		mw.AccessLog(),
	)

	app.Get("/readyz", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	app.Get("/livez", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	app.Get("/readyz-fail", func(c fiber.Ctx) error {
		return errs.Internal(errors.New("db ping failed"))
	})

	// 1. 测试正常状态的 /readyz 与 /livez：不产生任何访问日志，且未传追踪头时免于多余内存分配
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Empty(t, strings.TrimSpace(buf.String()), "正常 /readyz 探测禁止输出访问日志")
	assert.Empty(t, resp.Header.Get("x-request-id"), "未提供追踪头的探针免于分配 UUID 追踪标识以优化 GC")

	req = httptest.NewRequest(http.MethodGet, "/livez", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Empty(t, strings.TrimSpace(buf.String()), "正常 /livez 探测禁止输出访问日志")

	// 1.1 显式传递追踪头的探针请求必须被正常透传
	reqWithID := httptest.NewRequest(http.MethodGet, "/livez", nil)
	reqWithID.Header.Set("x-request-id", "probe-trace-123")
	respWithID, err := app.Test(reqWithID, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, "probe-trace-123", respWithID.Header.Get("x-request-id"))

	// 2. 测试异常状态的探针（状态码 >= 400 或 500）：必须输出告警或错误日志
	req = httptest.NewRequest(http.MethodGet, "/readyz-fail", nil)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
	assert.NotEmpty(t, strings.TrimSpace(buf.String()), "异常状态探针必须保留日志用于监控告警")
}

// TestObservability_Recovery_MarksSentryReported 验证 Recovery 中间件捕获 panic 后，
// 会在 fiber.Ctx.Locals 注入染色标记 sentry_reported = true，避免后续 ErrorHandler 二次重复上报。
func TestObservability_Recovery_MarksSentryReported(t *testing.T) {
	var reported bool
	mw := middleware.New(middleware.Deps{})
	app := fiber.New(fiber.Config{
		ErrorHandler: mw.ErrorHandler(),
	})
	app.Use(
		func(c fiber.Ctx) error {
			err := c.Next()
			reported = middleware.IsSentryReported(c)
			return err
		},
		mw.Recovery(),
	)

	app.Get("/panic", func(c fiber.Ctx) error {
		panic("database connection lost")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
	assert.True(t, reported, "发生 panic 时 Recovery 必须将上下文标记为已上报，杜绝重复 Issue")
}

// TestGlobal_SentryReportedHelpers 验证上下文染色辅助函数的安全边界与判断逻辑。
func TestGlobal_SentryReportedHelpers(t *testing.T) {
	assert.False(t, middleware.IsSentryReported(nil))

	app := fiber.New()
	app.Get("/test-sentry-helper", func(c fiber.Ctx) error {
		assert.False(t, middleware.IsSentryReported(c))

		middleware.MarkSentryReported(c)
		assert.True(t, middleware.IsSentryReported(c))

		// 重复标记幂等
		middleware.MarkSentryReported(c)
		assert.True(t, middleware.IsSentryReported(c))
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test-sentry-helper", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}
