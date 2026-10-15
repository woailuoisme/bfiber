// Package middleware 提供全链路 HTTP 传输层中间件。
//
// 统一编排并承载全局洋葱模型中间件（Recover, CORS, Telemetry, RequestID, Sentry, Timeout, AccessLog, Idempotency）
// 与端点级路由保护中间件（Auth, Can, Throttle）。
package middleware

import (
	"time"

	"github.com/gofiber/fiber/v3/extractors"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/authorization"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/i18n"
)

// Deps 描述 HTTP 传输层中间件所需的外部依赖。
type Deps struct {
	Cache          cache.Store
	Logger         *zap.Logger
	Translator     *i18n.Translator
	Tokens         *authentication.TokenIssuer
	AuthManager    *authentication.Manager
	Authz          *authorization.Enforcer
	CORS           config.CORSConfig
	Limiter        config.LimiterConfig
	TokenExtractor extractors.Extractor
	MaxBody        int64
	UploadMaxBody  int64
	Production     bool
	RequestTimeout time.Duration
	SigningKey     string
}

// Middleware 承载全链路 HTTP 传输层中间件的实例依赖。
type Middleware struct {
	cache          cache.Store
	logger         *zap.Logger
	translator     *i18n.Translator
	tokens         *authentication.TokenIssuer
	tokenExtractor extractors.Extractor
	authManager    *authentication.Manager
	authz          *authorization.Enforcer
	cors           config.CORSConfig
	limiter        config.LimiterConfig
	limiterStore   RateLimiterStore
	maxBody        int64
	uploadMaxBody  int64
	production     bool
	requestTimeout time.Duration
	signingKey     string
}

// New 构造统一的 HTTP 传输层中间件实例。
func New(deps Deps) *Middleware {
	tokenExtractor := deps.TokenExtractor
	if tokenExtractor.Extract == nil {
		tokenExtractor = extractors.Chain(
			extractors.FromAuthHeader("Bearer"),
			extractors.FromCookie("access_token"),
			extractors.FromQuery("token"),
		)
	}

	return &Middleware{
		cache:          deps.Cache,
		logger:         deps.Logger,
		translator:     deps.Translator,
		tokens:         deps.Tokens,
		tokenExtractor: tokenExtractor,
		authManager:    deps.AuthManager,
		authz:          deps.Authz,
		cors:           deps.CORS,
		limiter:        deps.Limiter,
		maxBody:        deps.MaxBody,
		uploadMaxBody:  deps.UploadMaxBody,
		production:     deps.Production,
		requestTimeout: deps.RequestTimeout,
		signingKey:     deps.SigningKey,
	}
}

// Logger 返回中间件持有的 Zap 实例。
func (m *Middleware) Logger() *zap.Logger {
	return m.logger
}

// SetLimiterStore 替换限流器底层存储实现（主要用于单元测试与自定义存储注入）。
func (m *Middleware) SetLimiterStore(store RateLimiterStore) {
	m.limiterStore = store
}
