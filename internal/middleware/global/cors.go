package global

import (
	"slices"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

var (
	// allowedRequestHeaders 跨域预检允许携带的请求头。
	allowedRequestHeaders = []string{"Authorization", "Content-Type", "Accept", "x-request-id", "Idempotency-Key", "X-Idempotency-Key"}
	// exposedResponseHeaders 允许浏览器脚本读取的响应头。
	exposedResponseHeaders = []string{"x-request-id", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After", "Idempotent-Replayed", "ETag"}
	// allowedMethods 跨域预检允许的 HTTP 方法。
	allowedMethods = []string{
		fiber.MethodGet,
		fiber.MethodPost,
		fiber.MethodPatch,
		fiber.MethodPut,
		fiber.MethodDelete,
		fiber.MethodOptions,
	}
)

// CORS 【全局中间件】处理跨域预检与响应头配置。
func (m *Middleware) CORS() fiber.Handler {
	allowAll := slices.Contains(m.cors.AllowedOrigins, "*")
	origins := m.cors.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
		allowAll = true
	}

	return cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowHeaders:     allowedRequestHeaders,
		AllowMethods:     allowedMethods,
		AllowCredentials: !allowAll,
		ExposeHeaders:    exposedResponseHeaders,
	})
}
