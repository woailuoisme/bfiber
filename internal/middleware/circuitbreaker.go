package middleware

import (
	"time"

	"github.com/gofiber/contrib/v3/circuitbreaker"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/errs"
)

// CircuitBreakerOption 定义熔断器函数式选项。
type CircuitBreakerOption func(*circuitbreaker.Config)

// WithFailureThreshold 设置触发熔断的连续失败阈值。
func WithFailureThreshold(threshold int) CircuitBreakerOption {
	return func(c *circuitbreaker.Config) {
		if threshold > 0 {
			c.FailureThreshold = threshold
		}
	}
}

// WithBreakerTimeout 设置熔断开启后等待尝试探活的冷却时间。
func WithBreakerTimeout(d time.Duration) CircuitBreakerOption {
	return func(c *circuitbreaker.Config) {
		if d > 0 {
			c.Timeout = d
		}
	}
}

// WithSuccessThreshold 设置半开探活状态下恢复正常所需的成功次数。
func WithSuccessThreshold(threshold int) CircuitBreakerOption {
	return func(c *circuitbreaker.Config) {
		if threshold > 0 {
			c.SuccessThreshold = threshold
		}
	}
}

// CircuitBreaker 【路由中间件】构建基于 Fiber Contrib 官方 circuitbreaker 的端点熔断保护中间件。
func (m *Middleware) CircuitBreaker(opts ...CircuitBreakerOption) fiber.Handler {
	cfg := circuitbreaker.DefaultConfig
	cfg.FailureThreshold = 5
	cfg.Timeout = 10 * time.Second
	cfg.SuccessThreshold = 2
	cfg.HalfOpenMaxConcurrent = 1
	cfg.Interval = 30 * time.Second
	cfg.IsFailure = func(c fiber.Ctx, err error) bool {
		if err != nil {
			return true
		}
		return c.Response().StatusCode() >= 500
	}
	cfg.OnOpen = func(c fiber.Ctx) error {
		return errs.ServiceUnavailable("Service circuit breaker open")
	}
	cfg.OnHalfOpen = func(c fiber.Ctx) error {
		return errs.ServiceUnavailable("Service is recovering")
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	cb := circuitbreaker.New(cfg)
	return circuitbreaker.Middleware(cb)
}
