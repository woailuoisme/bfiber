package global

import (
	"context"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"bfiber/internal/platform/errs"
)

// Timeout 【全局中间件】向请求上下文注入超时截止期，超时由下层 I/O 响应并经 ErrorHandler 统一渲染为 504 错误。
//
// 自动对 WebSocket 升级握手及 SSE (text/event-stream) 流式长连接进行超时豁免。
func (m *Middleware) Timeout() fiber.Handler {
	return func(c fiber.Ctx) error {
		if m.requestTimeout <= 0 || isStreamingRequest(c) {
			return c.Next()
		}

		ctx, cancel := context.WithTimeout(c.Context(), m.requestTimeout)
		defer cancel()

		c.SetContext(ctx)
		err := c.Next()

		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if m.logger != nil && c.Response().StatusCode() != 0 {
				m.logger.Warn("request timed out after partial response",
					zap.String("path", c.Path()),
					zap.String("method", c.Method()),
				)
			}
			return errs.GatewayTimeout(context.DeadlineExceeded)
		}

		return err
	}
}

// isStreamingRequest 检测是否为 WebSocket 握手或 SSE (Server-Sent Events) 流式长连接请求。
func isStreamingRequest(c fiber.Ctx) bool {
	if strings.EqualFold(c.Get("Upgrade"), "websocket") {
		return true
	}
	if strings.Contains(c.Get("Accept"), "text/event-stream") {
		return true
	}
	return false
}
