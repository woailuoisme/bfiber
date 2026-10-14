package global

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
	var store any
	if m != nil {
		store = m.cache
	}
	return NewIdempotency(store, opts...)
}

// NewIdempotency 构造独立的 Fiber 官方幂等中间件实例，自动对齐底层存储与回放指示头。
func NewIdempotency(store any, opts ...IdempotencyOption) fiber.Handler {
	cfg := idempotency.ConfigDefault
	switch s := store.(type) {
	case fiber.Storage:
		cfg.Storage = s
	case cache.Store:
		cfg.Storage = cache.AsFiberStorage(s, "idempotency")
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

// IdempotencyWithStore 构造独立的 Fiber 官方幂等中间件实例（NewIdempotency 语义别名）。
func IdempotencyWithStore(store any, opts ...IdempotencyOption) fiber.Handler {
	return NewIdempotency(store, opts...)
}
