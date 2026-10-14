package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"bfiber/internal/middleware/global"
	"bfiber/internal/platform/logger"
)

func TestTelemetryMiddleware_Disabled(t *testing.T) {
	mw := global.New(global.Deps{})
	app := fiber.New()
	app.Use(
		mw.Telemetry(),
		mw.RequestID(),
	)

	app.Get("/ping", func(c fiber.Ctx) error {
		return c.SendString("pong")
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("x-trace-id"))
	assert.NotEmpty(t, resp.Header.Get("x-request-id"))
}

func TestTelemetryMiddleware_TraceLogCorrelation(t *testing.T) {
	// 1. 设置内存 TracerProvider 模拟已激活的 OpenTelemetry
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	var buf bytes.Buffer
	testLogger := logger.NewWithOptions(
		logger.WithLevel("info"),
		logger.WithChannel("json"),
		logger.WithWriter(&buf),
	)

	mw := global.New(global.Deps{Logger: testLogger})
	app := fiber.New()

	app.Use(
		func(c fiber.Ctx) error {
			// 模拟通过 Tracer 创建 Server Span 并下发上下文
			tr := tp.Tracer("test/http")
			ctx, span := tr.Start(c.Context(), "GET /test-trace")
			defer span.End()
			c.SetContext(ctx)
			return c.Next()
		},
		mw.RequestID(),
	)

	app.Get("/test-trace", func(c fiber.Ctx) error {
		log := logger.FromContext(c.Context())
		log.Info("executing test handler")
		return c.SendString("ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test-trace", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	// 验证从活跃 Span 导出的 trace_id 是否已自动对齐为 X-Request-ID
	spans := spanRecorder.Ended()
	require.NotEmpty(t, spans)
	activeSpan := spans[0]
	expectedTraceID := activeSpan.SpanContext().TraceID().String()
	expectedSpanID := activeSpan.SpanContext().SpanID().String()

	assert.Equal(t, expectedTraceID, resp.Header.Get("x-request-id"), "未提供 X-Request-ID 时应自动对齐为 TraceID")

	// 验证 Zap JSON 日志是否已自动注入 trace_id 与 span_id
	line := strings.TrimSpace(buf.String())
	require.NotEmpty(t, line)

	var logData map[string]any
	err = json.Unmarshal([]byte(line), &logData)
	require.NoError(t, err)

	recordedTraceID, _ := logData["trace_id"].(string)
	recordedSpanID, _ := logData["span_id"].(string)
	recordedReqID, _ := logData["request_id"].(string)

	assert.Equal(t, expectedTraceID, recordedTraceID, "Zap 日志应包含匹配的 trace_id")
	assert.Equal(t, expectedSpanID, recordedSpanID, "Zap 日志应包含匹配的 span_id")
	assert.Equal(t, expectedTraceID, recordedReqID, "Zap 日志的 request_id 亦应统一对齐")
}
