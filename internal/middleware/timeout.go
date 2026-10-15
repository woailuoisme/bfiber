package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Timeout 【路由组中间件】基于 Go 标准库 context.WithTimeout 为下游业务提供全链路协同式超时控制。
// 零 Goroutine 调度开销、零 Context 内存泄漏，天然协同 Bun ORM、Pgx、Redis 与 HTTP Client 超时取消。
func (m *Middleware) Timeout(customTimeout ...time.Duration) fiber.Handler {
	return NewTimeout(m.resolveTimeout(customTimeout...))
}

// NewTimeout 构造独立的 Context 超时控制中间件。
func NewTimeout(d time.Duration) fiber.Handler {
	return func(c fiber.Ctx) error {
		return executeWithTimeout(c, d, c.Next)
	}
}

// WithTimeout 【路由级包装器】基于 context.WithTimeout 为指定业务 Handler 封装协同式超时控制。
func (m *Middleware) WithTimeout(h fiber.Handler, customTimeout ...time.Duration) fiber.Handler {
	d := m.resolveTimeout(customTimeout...)
	if d <= 0 {
		return h
	}
	return func(c fiber.Ctx) error {
		return executeWithTimeout(c, d, func() error {
			return h(c)
		})
	}
}

// executeWithTimeout 统一执行协同式超时控制逻辑。
func executeWithTimeout(c fiber.Ctx, d time.Duration, run func() error) error {
	if d <= 0 || isStreamingRequest(c) {
		return run()
	}

	ctx, cancel := context.WithTimeout(c.Context(), d)
	defer cancel()

	c.SetContext(ctx)
	if err := run(); err != nil {
		return err
	}
	return ctx.Err()
}

// resolveTimeout 解析默认超时或自定义覆写超时。
func (m *Middleware) resolveTimeout(customTimeout ...time.Duration) time.Duration {
	if len(customTimeout) > 0 && customTimeout[0] > 0 {
		return customTimeout[0]
	}
	if m != nil {
		return m.requestTimeout
	}
	return 0
}

// isStreamingRequest 检测是否为 WebSocket 握手或 SSE (Server-Sent Events) 流式长连接请求。
func isStreamingRequest(c fiber.Ctx) bool {
	return strings.EqualFold(fiber.GetReqHeader[string](c, fiber.HeaderUpgrade), "websocket") ||
		strings.Contains(fiber.GetReqHeader[string](c, fiber.HeaderAccept), "text/event-stream")
}

// RequestTimeout 返回配置的默认请求超时时长。
func (m *Middleware) RequestTimeout() time.Duration {
	if m == nil {
		return 0
	}
	return m.requestTimeout
}
