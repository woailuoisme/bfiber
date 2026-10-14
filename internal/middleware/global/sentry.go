package global

import (
	"cmp"
	"strings"
	"time"

	fiberSentry "github.com/gofiber/contrib/v3/sentry"
	"github.com/gofiber/fiber/v3"

	platformsentry "bfiber/internal/platform/sentry"
)

// Sentry 【全局中间件】挂载 Sentry / GlitchTip 请求作用域 Hub、关联 request_id 与 HTTP 事务。
func (m *Middleware) Sentry() fiber.Handler {
	if !platformsentry.IsInitialized() {
		return func(c fiber.Ctx) error {
			return c.Next()
		}
	}

	handler := fiberSentry.New(fiberSentry.Config{
		Repanic:         true,
		WaitForDelivery: false,
		Timeout:         2 * time.Second,
	})

	return func(c fiber.Ctx) error {
		// 对高频系统探针路由忽略 Sentry Hub 装配与事务采样，降低打点开销与内存分配
		if isProbePath(c.Path()) || (c.Route() != nil && isProbePath(c.Route().Path)) {
			return c.Next()
		}

		err := handler(c)

		// 为请求作用域的 Sentry Hub 注入链路追踪与上下文标签
		if hub := fiberSentry.GetHubFromContext(c); hub != nil {
			if reqID := cmp.Or(
				strings.TrimSpace(c.GetRespHeader(headerRequestID)),
				strings.TrimSpace(c.Get(headerRequestID)),
			); reqID != "" {
				hub.Scope().SetTag("request_id", reqID)
			}
			if route := c.Route(); route != nil && route.Path != "" {
				hub.Scope().SetTag("route", route.Path)
			}
			hub.Scope().SetTag("method", c.Method())
		}

		return err
	}
}

// SentryReportedKey 是用于在 fiber.Ctx.Locals 中标记当前请求是否已向 Sentry 上报异常的键名。
const SentryReportedKey = "sentry_reported"

// MarkSentryReported 标记当前请求已向 Sentry / GlitchTip 上报过异常，避免重复告警。
func MarkSentryReported(c fiber.Ctx) {
	if c != nil {
		c.Locals(SentryReportedKey, true)
	}
}

// IsSentryReported 检查当前请求是否已向 Sentry / GlitchTip 上报过异常。
func IsSentryReported(c fiber.Ctx) bool {
	if c == nil {
		return false
	}
	reported, _ := c.Locals(SentryReportedKey).(bool)
	return reported
}

// captureHTTPError 向 Sentry / GlitchTip 上报 5xx 服务端错误（过滤正常 4xx 业务与入参校验错误）。
func captureHTTPError(c fiber.Ctx, err error) {
	if !platformsentry.IsInitialized() || err == nil {
		return
	}
	if c.Response().StatusCode() < 500 {
		return
	}
	if IsSentryReported(c) {
		return
	}
	MarkSentryReported(c)

	if hub := fiberSentry.GetHubFromContext(c); hub != nil {
		hub.CaptureException(err)
		return
	}
	platformsentry.CaptureExceptionWithContext(c.Context(), err)
}
