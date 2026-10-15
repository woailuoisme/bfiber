package middleware

import (
	"time"

	"github.com/gofiber/contrib/v3/circuitbreaker"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/errs"
)

// CircuitBreakerConfig 是 Fiber v3 官方 contrib/circuitbreaker 配置别名。
type CircuitBreakerConfig = circuitbreaker.Config

// CircuitBreaker 是 Fiber v3 官方 contrib/circuitbreaker 熔断器实例别名。
type CircuitBreaker = circuitbreaker.CircuitBreaker

// CircuitBreakerState 是熔断器状态别名。
type CircuitBreakerState = circuitbreaker.State

// 熔断器三种运行状态。
const (
	StateClosed   = circuitbreaker.StateClosed
	StateHalfOpen = circuitbreaker.StateHalfOpen
	StateOpen     = circuitbreaker.StateOpen
)

// CircuitBreaker 【路由/路由组中间件】基于 Fiber v3 官方 contrib/circuitbreaker 提供高并发熔断防护。
// 当连续失败次数达到阈值后自动打开熔断器拦截请求，并在恢复超时后允许探测请求，全部错误响应统一按全系统 errs 规范渲染。
func (m *Middleware) CircuitBreaker(configs ...circuitbreaker.Config) fiber.Handler {
	cb := m.NewCircuitBreaker(configs...)
	return circuitbreaker.Middleware(cb)
}

// NewCircuitBreaker 构造一个独立的 CircuitBreaker 熔断器实例，支持直接挂载健康检查与监控探针。
func (m *Middleware) NewCircuitBreaker(configs ...circuitbreaker.Config) *circuitbreaker.CircuitBreaker {
	return NewCircuitBreaker(configs...)
}

// NewCircuitBreaker 构造独立的 CircuitBreaker 实例并自动补充与 bfiber 契约一致的统一错误响应处理函数。
func NewCircuitBreaker(configs ...circuitbreaker.Config) *circuitbreaker.CircuitBreaker {
	var cfg circuitbreaker.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}

	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 1
	}
	if cfg.HalfOpenMaxConcurrent <= 0 {
		cfg.HalfOpenMaxConcurrent = 1
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(c fiber.Ctx, err error) bool {
			return err != nil || c.Response().StatusCode() >= fiber.StatusInternalServerError
		}
	}
	if cfg.OnOpen == nil {
		cfg.OnOpen = func(c fiber.Ctx) error {
			return errs.Render(c, errs.ServiceUnavailable("Service is temporarily unavailable due to open circuit"))
		}
	}
	if cfg.OnHalfOpen == nil {
		cfg.OnHalfOpen = func(c fiber.Ctx) error {
			return errs.Render(c, errs.RateLimited("Service is recovering, please retry later"))
		}
	}
	if cfg.OnClose == nil {
		cfg.OnClose = func(c fiber.Ctx) error {
			return nil
		}
	}

	return circuitbreaker.New(cfg)
}
