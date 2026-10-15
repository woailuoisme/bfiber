package bootstrap

import (
	"errors"
	"fmt"

	contribzap "github.com/gofiber/contrib/v3/zap"
	"github.com/gofiber/fiber/v3"
	fiberlog "github.com/gofiber/fiber/v3/log"
	"github.com/gofiber/fiber/v3/middleware/etag"
	"github.com/gofiber/fiber/v3/middleware/favicon"

	"bfiber/internal/config"
	adminauth "bfiber/internal/features/admin/auth"
	admin "bfiber/internal/features/admin/user"
	"bfiber/internal/features/client/auth"
	"bfiber/internal/features/client/profile"
	paymentFeature "bfiber/internal/features/payment"
	realtimeFeature "bfiber/internal/features/realtime"
	"bfiber/internal/features/system"
	"bfiber/internal/middleware"
	"bfiber/internal/platform/validator"
)

// buildEngine 组装 Fiber 引擎与全部路由。
func buildEngine(
	cfg *config.Config,
	mw *middleware.Middleware,
	systemHandler *system.Handler,
	authHandler *auth.Handler,
	profileHandler *profile.Handler,
	adminHandler *admin.Handler,
	adminAuthHandler *adminauth.Handler,
	realtimeHandler *realtimeFeature.Handler,
	paymentHandler *paymentFeature.Handler,
) (*fiber.App, error) {
	if err := validator.InitEngine(); err != nil {
		return nil, fmt.Errorf("init validator engine failed: %w", err)
	}

	if logger := mw.Logger(); logger != nil {
		fiberlog.SetLogger(contribzap.NewLogger(contribzap.LoggerConfig{
			SetLogger: logger,
		}))
	}

	bodyLimit := int(cfg.Media.MaxFileSize)
	if bodyLimit <= 0 {
		bodyLimit = 32 << 20 // 32 MiB
	}

	app := fiber.New(fiber.Config{
		StructValidator:     validator.NewFiberValidator(),
		ErrorHandler:        mw.ErrorHandler(),
		BodyLimit:           bodyLimit,
		PassLocalsToContext: true,
	})

	// 全局中间件链：按洋葱模型分层编排（快速短路 -> 链路观测 -> 协议跨域 -> 传输优化 -> 异常治理 -> 幂等锁定）
	// 注意：传输压缩（compress）与浏览器安全响应标头（helmet）推荐由前置网关（Caddy/Traefik）接管。
	app.Use(
		favicon.New(),    // 1. 网站图标短路：拦截 /favicon.ico 并以 204 返回，免于穿透路由
		mw.Telemetry(),   // 2. 链路追踪与指标：OpenTelemetry 根 Span 创建与服务度量采集
		mw.RequestID(),   // 3. 请求唯一标识：提取或生成 X-Request-ID 并注入上下文日志器
		mw.AccessLog(),   // 4. 访问日志：记录请求方法、路径、状态码、IP 与端到端耗时
		etag.New(),       // 5. 缓存协商：自动生成 ETag 并支持 304 Not Modified
		mw.CORS(),        // 6. 跨域策略：注入跨域标头并对 OPTIONS 预检请求快速短路
		mw.BodyLimit(),   // 7. 请求体大小限制：普通请求 1 MiB，multipart 上传按 UploadMaxBody 放行
		mw.Recovery(),    // 8. 恐慌兜底恢复：捕获未处理 panic 记录堆栈并转为 500 错误
		mw.Sentry(),      // 9. 异常监控上报：挂载 Sentry 请求 Hub 并捕获 panic 与 5xx 异常
		mw.Idempotency(), // 10. 官方幂等控制：基于 X-Idempotency-Key 进行并发锁定与成功响应回放
	)

	// 注册根路径与系统探针路由（/、/livez、/readyz）
	system.RegisterRoutes(app, systemHandler)

	// 仅在开启 ENABLE_DOCS 时注册 API 文档路由
	if cfg.App.EnableDocs {
		registerDocs(app)
	}

	v1 := app.Group("/v1")
	if mw != nil && mw.RequestTimeout() > 0 {
		v1.Use(mw.Timeout())
	}
	auth.RegisterRoutes(v1, authHandler, mw)
	profile.RegisterRoutes(v1, profileHandler, mw)
	adminauth.RegisterAdminAuthRoutes(v1, adminAuthHandler, mw)
	admin.RegisterAdminRoutes(v1, adminHandler, mw)
	realtimeFeature.RegisterRoutes(v1, realtimeHandler, mw)
	paymentFeature.RegisterRoutes(v1, paymentHandler, mw)

	return app, nil
}

// InspectEngine 组装用于路由探测与文档自省的轻量级 Fiber 应用实例（零数据库与缓存网络连接）。
func InspectEngine(cfg *config.Config) (*fiber.App, error) {
	if cfg == nil {
		return nil, errors.New("bootstrap: inspect engine requires a non-nil config")
	}

	mw := middleware.New(middleware.Deps{
		CORS:           cfg.CORS,
		Limiter:        cfg.Limiter,
		Production:     cfg.IsProduction(),
		RequestTimeout: cfg.App.Server.RequestTimeout,
	})

	return buildEngine(
		cfg,
		mw,
		&system.Handler{},
		&auth.Handler{},
		&profile.Handler{},
		&admin.Handler{},
		&adminauth.Handler{},
		&realtimeFeature.Handler{},
		&paymentFeature.Handler{},
	)
}
