package middleware

import (
	"errors"
	"strings"

	jwtware "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	"github.com/golang-jwt/jwt/v5"

	"bfiber/internal/platform/errs"
)

// authTokenProvenanceLocalsKey 是 Fiber Ctx Locals 中记录令牌提取来源的私有键名。
const authTokenProvenanceLocalsKey = "auth_token_provenance"

// JWTConfig 是 Fiber v3 官方 contrib/jwt 中间件配置别名。
type JWTConfig = jwtware.Config

// SigningKey 是 Fiber v3 官方 contrib/jwt 签名密钥配置别名。
type SigningKey = jwtware.SigningKey

// JWTFromContext 从 Fiber 上下文中提取由官方 contrib/jwt 解析并校验通过的 *jwt.Token。
func JWTFromContext(c fiber.Ctx) *jwt.Token {
	return jwtware.FromContext(c)
}

// JWT 【路由/路由组中间件】基于 Fiber v3 官方 contrib/jwt (jwtware) 进行标准 JWT 令牌校验与上下文注入。
// 校验通过后，后续 Handler 可直接通过 jwtware.FromContext(c) 或 middleware.JWTFromContext(c) 获取强类型 *jwt.Token。
func (m *Middleware) JWT(configs ...jwtware.Config) fiber.Handler {
	cfg := m.resolveJWTConfig(configs)
	return jwtware.New(cfg)
}

// resolveJWTConfig 组装与兜底官方 jwtware 配置，自动注入应用全局的签名密钥、凭据提取器与统一错误渲染。
func (m *Middleware) resolveJWTConfig(configs []jwtware.Config) jwtware.Config {
	var cfg jwtware.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}

	// 1. 自动注入签名密钥兜底
	m.resolveSigningKey(&cfg)

	// 2. 自动注入 Extractors 兜底
	if cfg.Extractor.Extract == nil && m != nil && m.tokenExtractor.Extract != nil {
		cfg.Extractor = m.tokenExtractor
	}

	// 3. 增强 Extractor：支持来源感知 (Provenance) 追踪与突变请求 Query Token 安全拦截
	if cfg.Extractor.Extract != nil {
		cfg.Extractor = wrapTokenProvenanceExtractor(cfg.Extractor)
	}

	// 4. 统一与全系统 errs 规范对齐的错误渲染
	if cfg.ErrorHandler == nil {
		cfg.ErrorHandler = defaultJWTErrorHandler()
	}

	return cfg
}

// resolveSigningKey 自动注入签名密钥兜底。
func (m *Middleware) resolveSigningKey(cfg *jwtware.Config) {
	if cfg.SigningKey.Key != nil || len(cfg.SigningKeys) > 0 || cfg.KeyFunc != nil || len(cfg.JWKSetURLs) > 0 {
		return
	}
	if m != nil && m.tokens != nil && len(m.tokens.Secret()) > 0 {
		cfg.SigningKey = jwtware.SigningKey{Key: m.tokens.Secret(), JWTAlg: jwtware.HS256}
	} else if m != nil && m.signingKey != "" {
		cfg.SigningKey = jwtware.SigningKey{Key: []byte(m.signingKey), JWTAlg: jwtware.HS256}
	}
}

// wrapTokenProvenanceExtractor 增强 Extractor，利用 Fiber v3 extractors.Resolve 追踪来源审计并在突变请求拦截 Query Token。
func wrapTokenProvenanceExtractor(baseExtractor extractors.Extractor) extractors.Extractor {
	return extractors.Extractor{
		Extract: func(c fiber.Ctx) (string, error) {
			return extractAndValidateToken(baseExtractor, c)
		},
		Source:     baseExtractor.Source,
		Key:        baseExtractor.Key,
		AuthScheme: baseExtractor.AuthScheme,
		Chain:      baseExtractor.Chain,
	}
}

// BearerScheme 是 RFC 6750 定义的标准 Bearer 认证方案名称。
const BearerScheme = "Bearer"

// extractAndValidateToken 解析请求中的令牌并执行来源及突变方法安全校验。
func extractAndValidateToken(baseExtractor extractors.Extractor, c fiber.Ctx) (string, error) {
	res, err := extractors.Resolve(baseExtractor, c)
	if err != nil || res.Value == "" {
		authHeader := strings.TrimSpace(fiber.GetReqHeader[string](c, fiber.HeaderAuthorization))
		if authHeader != "" && !strings.HasPrefix(authHeader, BearerScheme+" ") {
			return "", errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authorization header must use the Bearer scheme")
		}
		if err != nil {
			return "", err
		}
		return "", extractors.ErrNotFound
	}

	if res.Source == extractors.SourceQuery && isMutatingHTTPMethod(c.Method()) {
		return "", errs.Unauthorized(
			errs.CodeAuthTokenInvalid,
			"query token authentication is not permitted for mutating requests",
		)
	}

	c.Locals(authTokenProvenanceLocalsKey, res.Source)
	return res.Value, nil
}

// isMutatingHTTPMethod 检查 HTTP 方法是否属于状态变更操作。
func isMutatingHTTPMethod(method string) bool {
	return method == fiber.MethodPost || method == fiber.MethodPut || method == fiber.MethodPatch || method == fiber.MethodDelete
}

// defaultJWTErrorHandler 构造统一与全系统 errs 规范对齐的错误渲染器。
func defaultJWTErrorHandler() fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		if appErr, ok := errors.AsType[*errs.Error](err); ok {
			return errs.Render(c, appErr)
		}
		if errors.Is(err, jwt.ErrTokenExpired) {
			return errs.Render(c, errs.Unauthorized(errs.CodeAuthTokenExpired, "Access token has expired"))
		}
		return errs.Render(c, errs.AuthenticationRequired())
	}
}
