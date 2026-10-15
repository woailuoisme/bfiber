package middleware

import (
	"github.com/gofiber/contrib/v3/otel"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/telemetry"
)

const (
	headerTraceID = "x-trace-id"
)

// Telemetry 【全局中间件】基于 Fiber Contrib 官方 OTel 中间件采集 HTTP 链路追踪 (Traces) 与服务度量指标 (Metrics)。
func (m *Middleware) Telemetry() fiber.Handler {
	return otel.New(
		otel.WithNext(func(c fiber.Ctx) bool {
			return !telemetry.IsInitialized() || isProbeRequest(c)
		}),
		otel.WithTraceResponseHeader(headerTraceID),
		otel.WithClientIP(true),
		otel.WithCapturedRequestHeaders(headerRequestID, HeaderIdempotencyKey, AltHeaderIdempotencyKey),
	)
}
