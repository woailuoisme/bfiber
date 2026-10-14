package middleware

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware/global"
)

// IdempotencyOption 定义幂等中间件函数式配置项。
type IdempotencyOption = global.IdempotencyOption

// WithIdempotencyLifetime 设置成功响应的缓存保留时长。
var WithIdempotencyLifetime = global.WithIdempotencyLifetime

// WithIdempotencyKeyHeader 设置读取幂等键的标头名。
var WithIdempotencyKeyHeader = global.WithIdempotencyKeyHeader

// WithIdempotencyStorage 设置幂等响应持久化底层存储。
var WithIdempotencyStorage = global.WithIdempotencyStorage

// Idempotency 构造路由/路由组级请求幂等中间件。
func (m *Middleware) Idempotency(opts ...IdempotencyOption) fiber.Handler {
	return global.NewIdempotency(m.cache, opts...)
}
