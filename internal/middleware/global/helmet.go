package global

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/helmet"
)

// Helmet 【全局中间件】基于 Fiber v3 官方 helmet 中间件注入工业级安全响应标头。
func (m *Middleware) Helmet() fiber.Handler {
	cfg := helmet.Config{
		XSSProtection:             "0",
		ContentTypeNosniff:        "nosniff",
		XFrameOptions:             "DENY",
		ReferrerPolicy:            "no-referrer",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		OriginAgentCluster:        "?1",
		XDNSPrefetchControl:       "off",
		XDownloadOptions:          "noopen",
		XPermittedCrossDomain:     "none",
		PermissionPolicy:          "camera=(), microphone=(), geolocation=(), payment=()",
	}
	if m.production {
		cfg.HSTSMaxAge = 31536000
		cfg.HSTSExcludeSubdomains = false
		cfg.HSTSPreloadEnabled = true
	}
	inner := helmet.New(cfg)
	if !m.production {
		return inner
	}
	// 在生产环境下，若反向代理终结 TLS（或测试运行在模拟 HTTP 下），确保下发 HSTS 防护标头
	return func(c fiber.Ctx) error {
		if err := inner(c); err != nil {
			return err
		}
		if c.GetRespHeader("Strict-Transport-Security") == "" {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}
		return nil
	}
}

// SecurityHeaders 保持向后兼容别名，内部调用 Fiber v3 官方 Helmet 中间件。
func (m *Middleware) SecurityHeaders() fiber.Handler {
	return m.Helmet()
}
