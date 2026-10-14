package global

import (
	"cmp"
	"encoding/hex"
	"runtime/debug"
	"strings"

	contribzap "github.com/gofiber/contrib/v3/zap"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
)

// headerRequestID 是全链路追踪请求头。
const headerRequestID = "x-request-id"

// requestIDPrefix 是追踪标识前缀。
const requestIDPrefix = "req_"

// RequestID 【全局中间件】基于 Fiber v3 官方 requestid 中间件生成并透传请求标识，并将其自动同步注入日志上下文。
func (m *Middleware) RequestID() fiber.Handler {
	inner := requestid.New(requestid.Config{
		Header:    headerRequestID,
		Generator: newRequestID,
	})

	return func(c fiber.Ctx) error {
		rid := strings.TrimSpace(c.Get(headerRequestID))
		if rid == "" {
			if isProbeRequest(c) {
				return c.Next()
			}
			// 优先使用当前活跃 OTel Span 的 TraceID（实现请求标识、后端链路与日志三位一体统一）
			if span := trace.SpanFromContext(c.Context()); span.SpanContext().IsValid() {
				rid = span.SpanContext().TraceID().String()
			} else {
				rid = newRequestID()
			}
			c.Request().Header.Set(headerRequestID, rid)
		}

		c.SetContext(logger.WithContext(c.Context(), m.logger, rid))
		return inner(c)
	}
}

// SyncLogger 【已内聚至 RequestID】保留方法签名以保证向后兼容性。
func (m *Middleware) SyncLogger() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.Next()
	}
}

// newRequestID 生成形如 req_<uuidv7> 的追踪标识。
func newRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return requestIDPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	}

	buf := make([]byte, 0, len(requestIDPrefix)+hex.EncodedLen(len(id)))
	buf = append(buf, requestIDPrefix...)
	return string(hex.AppendEncode(buf, id[:]))
}

// AccessLog 【全局中间件】基于 Fiber Contrib 官方 zap 中间件记录请求访问日志。
func (m *Middleware) AccessLog() fiber.Handler {
	log := m.logger
	if log == nil {
		log = logger.Default()
		if log == nil {
			log = zap.NewNop()
		}
	}

	return contribzap.New(contribzap.Config{
		Next:     isProbeRequest,
		Logger:   log,
		Fields:   []string{"method", "path", "status", "latency", "ip", "error"},
		Messages: []string{"access", "access", "access"},
		FieldsFunc: func(c fiber.Ctx) []zap.Field {
			fields := make([]zap.Field, 0, 2)
			if rid := cmp.Or(
				requestid.FromContext(c),
				strings.TrimSpace(c.GetRespHeader(headerRequestID)),
				strings.TrimSpace(c.Get(headerRequestID)),
			); rid != "" {
				fields = append(fields, zap.String("request_id", rid))
			}
			if code := errs.ErrorCodeFromContext(c); code != "" {
				fields = append(fields, zap.String("code", code))
			}
			return fields
		},
	})
}

// isProbeRequest 判断当前请求是否属于高频系统探针心跳路由。
func isProbeRequest(c fiber.Ctx) bool {
	routePath := ""
	if c.Route() != nil {
		routePath = c.Route().Path
	}
	return isProbePath(c.Path()) || isProbePath(routePath)
}

// isProbePath 判断请求是否为高频系统探针路由。
func isProbePath(path string) bool {
	return path == "/livez" || path == "/readyz" || path == "/healthz"
}

// Recovery 【全局中间件】基于 Fiber v3 官方 recover 中间件捕获 panic 转为统一 500 响应，堆栈仅进入日志。
func (m *Middleware) Recovery() fiber.Handler {
	return recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			log := logger.FromContext(c.Context())
			if _, ok := logger.FromContextOK(c.Context()); !ok && m.logger != nil {
				log = m.logger
			}
			log.Error("request panic recovered",
				zap.Any("panic", e),
				zap.String("stack", string(debug.Stack())),
			)

			// 标记当前请求的 panic 异常已由 Zap/Sentry Core 捕获，防止全局 ErrorHandler 二次重复上报
			MarkSentryReported(c)
		},
		PanicHandler: func(c fiber.Ctx, r any) error {
			return errs.Internal(nil)
		},
	})
}
