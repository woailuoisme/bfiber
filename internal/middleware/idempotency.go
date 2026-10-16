package middleware

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/idempotency"

	"bfiber/internal/platform/cache"
)

const (
	// HeaderIdempotencyKey IETF / Stripe 幂等键标头名。
	HeaderIdempotencyKey = "Idempotency-Key"
	// AltHeaderIdempotencyKey 传统与 Fiber v3 官方默认幂等键标头名。
	AltHeaderIdempotencyKey = "X-Idempotency-Key"
	// HeaderIdempotentReplayed 标记响应是否由幂等缓存直接回放。
	HeaderIdempotentReplayed = "Idempotent-Replayed"
)

// IdempotencyConfig 引用 Fiber v3 官方幂等中间件配置结构体。
type IdempotencyConfig = idempotency.Config

// IdempotencyStore 定义幂等中间件底层存储契约，天然对齐 Fiber v3 官方 fiber.Storage。
type IdempotencyStore = fiber.Storage

// IdempotencyOption 定义幂等中间件函数式配置项。
type IdempotencyOption func(*idempotency.Config)

// WithIdempotencyLifetime 设置成功响应的缓存保留时长。
func WithIdempotencyLifetime(d time.Duration) IdempotencyOption {
	return func(c *idempotency.Config) {
		if d > 0 {
			c.Lifetime = d
		}
	}
}

// WithIdempotencyKeyHeader 设置读取幂等键的标头名。
func WithIdempotencyKeyHeader(header string) IdempotencyOption {
	return func(c *idempotency.Config) {
		if header != "" {
			c.KeyHeader = header
		}
	}
}

// WithIdempotencyStorage 设置幂等响应持久化底层存储。
func WithIdempotencyStorage(storage fiber.Storage) IdempotencyOption {
	return func(c *idempotency.Config) {
		if storage != nil {
			c.Storage = storage
		}
	}
}

// Idempotency 【全局中间件】基于 Fiber v3 官方内置 idempotency 中间件构建。
func (m *Middleware) Idempotency(opts ...IdempotencyOption) fiber.Handler {
	var storage fiber.Storage
	if m != nil && m.cache != nil {
		storage = cache.AsFiberStorage(m.cache)
	}
	return NewIdempotency(storage, opts...)
}

// NewIdempotency 构造独立的 Fiber 官方幂等中间件实例，自动对齐底层存储与回放指示头。
func NewIdempotency(storage fiber.Storage, opts ...IdempotencyOption) fiber.Handler {
	cfg := idempotency.ConfigDefault
	if storage != nil {
		cfg.Storage = storage
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	inner := idempotency.New(cfg)

	return func(c fiber.Ctx) error {
		err := inner(c)
		if err != nil {
			if errors.Is(err, idempotency.ErrInvalidIdempotencyKey) {
				return fiber.NewError(fiber.StatusBadRequest, err.Error())
			}
			return err
		}
		if idempotency.IsFromCache(c) {
			c.Set(HeaderIdempotentReplayed, "true")
		}
		return nil
	}
}
