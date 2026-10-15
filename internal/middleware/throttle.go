package middleware

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"go.uber.org/zap"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/errs"
)

// Tier 定义业务限流预设档位。
type Tier string

const (
	// TierAnonymous 面向未认证客户端，按客户端 IP 计数。
	TierAnonymous Tier = "anonymous"
	// TierAuthenticated 面向已认证用户，按用户 ID 计数。
	TierAuthenticated Tier = "authenticated"
	// TierSensitive 面向敏感认证端点，按客户端 IP 计数且配额最严。
	TierSensitive Tier = "sensitive"

	rateLimitWindow = time.Minute
)

// ThrottleOption 定义自定义限流选项。
type ThrottleOption func(*throttleOptions)

type throttleOptions struct {
	keyFunc  func(c fiber.Ctx) string
	tier     Tier
	response func(c fiber.Ctx) error
	logger   *zap.Logger
}

// WithThrottleKey 自定义限流区分维度（例如按指定 header 或复合 key）。
func WithThrottleKey(fn func(c fiber.Ctx) string) ThrottleOption {
	return func(o *throttleOptions) {
		o.keyFunc = fn
	}
}

// WithThrottleTier 指定所属业务档位（用于监控与日志分类）。
func WithThrottleTier(tier Tier) ThrottleOption {
	return func(o *throttleOptions) {
		o.tier = tier
	}
}

// WithThrottleResponse 指定自定义限流触发时的响应拦截回调。
func WithThrottleResponse(fn func(c fiber.Ctx) error) ThrottleOption {
	return func(o *throttleOptions) {
		o.response = fn
	}
}

// WithThrottleLogger 指定限流存储故障降级时的告警日志输出器。
func WithThrottleLogger(logger *zap.Logger) ThrottleOption {
	return func(o *throttleOptions) {
		o.logger = logger
	}
}

// RateLimiterStore 定义限流中间件底层存储契约，天然对齐 Fiber v3 官方 fiber.Storage。
type RateLimiterStore = fiber.Storage

// Throttle 【路由/路由组中间件】基于 Fiber v3 官方 limiter + SlidingWindow 策略执行平滑滑动窗口限流。
// 配额自动从系统配置中读取（Anonymous, Authenticated, Sensitive）。
func (m *Middleware) Throttle(tier Tier, opts ...ThrottleOption) fiber.Handler {
	if m != nil && !m.limiter.IsEnabled() {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	allOpts := make([]ThrottleOption, 0, len(opts)+2)
	allOpts = append(allOpts, WithThrottleTier(tier))
	if m != nil && m.logger != nil {
		allOpts = append(allOpts, WithThrottleLogger(m.logger))
	}
	allOpts = append(allOpts, opts...)
	return ThrottleWithStore(m.resolveLimiterStorage(), m.limitFor(tier), rateLimitWindow, allOpts...)
}

// ThrottleRate 【路由/路由组中间件】按指定的具体配额与时间周期执行平滑滑动窗口限流。
func (m *Middleware) ThrottleRate(limit int, window time.Duration, opts ...ThrottleOption) fiber.Handler {
	if m != nil && !m.limiter.IsEnabled() {
		return func(c fiber.Ctx) error { return c.Next() }
	}
	allOpts := make([]ThrottleOption, 0, len(opts)+1)
	if m != nil && m.logger != nil {
		allOpts = append(allOpts, WithThrottleLogger(m.logger))
	}
	allOpts = append(allOpts, opts...)
	return ThrottleWithStore(m.resolveLimiterStorage(), limit, window, allOpts...)
}

func (m *Middleware) resolveLimiterStorage() fiber.Storage {
	if m == nil {
		return nil
	}
	if m.limiterStore != nil {
		return m.limiterStore
	}
	if m.cache != nil {
		return cache.AsFiberStorage(m.cache)
	}
	return nil
}

// ThrottleWithStore 构造独立的平滑滑动窗口限流中间件实例，底层驱动采用 Fiber v3 limiter.SlidingWindow。
func ThrottleWithStore(storage fiber.Storage, limit int, window time.Duration, opts ...ThrottleOption) fiber.Handler {
	var options throttleOptions
	for _, opt := range opts {
		opt(&options)
	}
	if window <= 0 {
		window = rateLimitWindow
	}

	if storage == nil || limit <= 0 {
		return func(c fiber.Ctx) error {
			return c.Next()
		}
	}

	failOpenStore := cache.FailOpen(storage, options.logger)

	limiterHandler := limiter.New(limiter.Config{
		LimiterMiddleware: limiter.SlidingWindow{},
		Storage:           failOpenStore,
		Max:               limit,
		Expiration:        window,
		KeyGenerator: func(c fiber.Ctx) string {
			switch {
			case options.keyFunc != nil:
				return options.keyFunc(c)
			case options.tier != "":
				return scopeKey(options.tier, c)
			default:
				return defaultScopeKey(c)
			}
		},
		LimitReached: func(c fiber.Ctx) error {
			c.Set("X-RateLimit-Limit", strconv.Itoa(limit))
			c.Set("X-RateLimit-Remaining", "0")
			retryAfter := c.GetRespHeader("Retry-After")
			if retryAfter == "" {
				retryAfter = "1"
				c.Set("Retry-After", retryAfter)
			}
			resetSec, _ := strconv.ParseInt(retryAfter, 10, 64)
			if resetSec <= 0 {
				resetSec = 1
			}
			c.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Unix()+resetSec, 10))

			if options.response != nil {
				return options.response(c)
			}
			return errs.RateLimited("Too many requests")
		},
	})

	return limiterHandler
}

// ThrottleWithStorage 构造强类型官方 Storage 的平滑滑动窗口限流中间件实例。
func ThrottleWithStorage(storage fiber.Storage, limit int, window time.Duration, opts ...ThrottleOption) fiber.Handler {
	return ThrottleWithStore(storage, limit, window, opts...)
}

// limitFor 按档位读取配额，未知档位回退至未认证配额。
func (m *Middleware) limitFor(tier Tier) int {
	if m != nil && !m.limiter.IsEnabled() {
		return 0
	}
	switch tier {
	case TierAuthenticated:
		return m.limiter.Authenticated
	case TierSensitive:
		return m.limiter.Sensitive
	default:
		return m.limiter.Anonymous
	}
}

// defaultScopeKey 默认限流维度：优先使用已认证用户 ID，未认证时回退为客户端 IP。
func defaultScopeKey(c fiber.Ctx) string {
	if id, ok := authentication.FromContext(c.Context()); ok && id.UserID != "" {
		return "user:" + id.UserID
	}
	return "ip:" + c.IP()
}

// scopeKey 确定特定档位的限流维度：TierAuthenticated 优先使用 UserID，其余均使用客户端 IP。
func scopeKey(tier Tier, c fiber.Ctx) string {
	if tier == TierAuthenticated {
		return defaultScopeKey(c)
	}
	return "ip:" + c.IP()
}
