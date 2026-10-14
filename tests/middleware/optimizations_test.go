package middleware_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/middleware/global"
)

// TestFavicon_ShortCircuit 验证 /favicon.ico 请求被短路拦截为 204 No Content，不穿透至后续业务中间件。
func TestFavicon_ShortCircuit(t *testing.T) {
	m := global.New(global.Deps{})
	downstreamExecuted := false

	app := fiber.New()
	app.Use(m.Favicon())
	app.Use(func(c fiber.Ctx) error {
		downstreamExecuted = true
		return c.Next()
	})
	app.Get("/hello", func(c fiber.Ctx) error {
		return c.SendString("world")
	})

	// 1. 请求 /favicon.ico 应当返回 204 且短路
	reqFav := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	respFav, err := app.Test(reqFav, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusNoContent, respFav.StatusCode)
	assert.False(t, downstreamExecuted, "favicon request should short-circuit")

	// 2. 正常请求应当放行至下游
	reqNormal := httptest.NewRequest(http.MethodGet, "/hello", nil)
	respNormal, err := app.Test(reqNormal, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, respNormal.StatusCode)
	assert.True(t, downstreamExecuted, "normal request should pass to downstream")
}

// TestCompress_Gzip 验证客户端携带 Accept-Encoding 时，响应体被透明 gzip 压缩。
func TestCompress_Gzip(t *testing.T) {
	m := global.New(global.Deps{})

	app := fiber.New()
	app.Use(m.Compress())

	sampleText := strings.Repeat("Fiber v3 official compress middleware test payload. ", 20)
	app.Get("/compress-test", func(c fiber.Ctx) error {
		return c.SendString(sampleText)
	})

	req := httptest.NewRequest(http.MethodGet, "/compress-test", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// 使用 gzip 解压读取
	gzReader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	defer func() { _ = gzReader.Close() }()

	decompressed, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	assert.Equal(t, sampleText, string(decompressed))
}

// TestCompress_WithCustomLevel 验证使用自定义压缩级别时的正常压缩行为。
func TestCompress_WithCustomLevel(t *testing.T) {
	m := global.New(global.Deps{})

	app := fiber.New()
	app.Use(m.Compress(global.WithCompressLevel(compress.LevelBestSpeed)))

	sampleText := strings.Repeat("Fiber v3 custom compression level test payload. ", 20)
	app.Get("/compress-speed", func(c fiber.Ctx) error {
		return c.SendString(sampleText)
	})

	req := httptest.NewRequest(http.MethodGet, "/compress-speed", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	gzReader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	defer func() { _ = gzReader.Close() }()

	decompressed, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	assert.Equal(t, sampleText, string(decompressed))
}

// TestETag_Negotiation 验证 ETag 标头生成以及客户端通过 If-None-Match 触发 304 缓存协商。
func TestETag_Negotiation(t *testing.T) {
	m := global.New(global.Deps{})

	app := fiber.New()
	app.Use(m.ETag())

	app.Get("/resource", func(c fiber.Ctx) error {
		return c.SendString("fixed resource content")
	})

	// 1. 首次请求：应当返回 200 并携带 ETag 标头
	req1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	resp1, err := app.Test(req1, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp1.StatusCode)
	defer func() { _ = resp1.Body.Close() }()

	etagVal := resp1.Header.Get("ETag")
	assert.NotEmpty(t, etagVal, "ETag header must be present")

	// 2. 二次请求：携带 If-None-Match，应当协商返回 304 Not Modified
	req2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req2.Header.Set("If-None-Match", etagVal)

	resp2, err := app.Test(req2, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusNotModified, resp2.StatusCode)

	bodyBytes, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Empty(t, bodyBytes, "304 response body should be empty")
}
