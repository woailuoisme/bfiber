package bootstrap

import (
	"errors"
	"fmt"

	contribzap "github.com/gofiber/contrib/v3/zap"
	"github.com/gofiber/fiber/v3"
	fiberlog "github.com/gofiber/fiber/v3/log"

	"bfiber/internal/config"
	adminauth "bfiber/internal/features/admin/auth"
	admin "bfiber/internal/features/admin/user"
	"bfiber/internal/features/client/auth"
	"bfiber/internal/features/client/profile"
	paymentFeature "bfiber/internal/features/payment"
	realtimeFeature "bfiber/internal/features/realtime"
	"bfiber/internal/features/system"
	"bfiber/internal/middleware"
	"bfiber/internal/middleware/global"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/validator"
)

// buildEngine 组装 Fiber 引擎与全部路由。
func buildEngine(
	cfg *config.Config,
	globalMW *global.Middleware,
	routeMW *middleware.Middleware,
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

	if logger := globalMW.Logger(); logger != nil {
		fiberlog.SetLogger(contribzap.NewLogger(contribzap.LoggerConfig{
			SetLogger: logger,
		}))
	}

	bodyLimit := int(cfg.Media.MaxFileSize)
	if bodyLimit <= 0 {
		bodyLimit = 32 << 20 // 32 MiB
	}

	app := fiber.New(fiber.Config{
		StructValidator: validator.NewFiberValidator(),
		ErrorHandler:    globalMW.ErrorHandler(),
		BodyLimit:       bodyLimit,
	})

	// 全局中间件链：按洋葱模型分层编排（快速短路 -> 链路观测 -> 协议安全 -> 传输优化 -> 异常治理 -> 载荷控制）
	app.Use(
		globalMW.Favicon(),     // 1. 网站图标短路：拦截 /favicon.ico 并以 204 返回，免于穿透路由
		globalMW.Telemetry(),   // 2. 链路追踪与指标：OpenTelemetry 根 Span 创建与服务度量采集
		globalMW.RequestID(),   // 3. 请求唯一标识：提取或生成 X-Request-ID 并注入上下文日志器
		globalMW.AccessLog(),   // 4. 访问日志：记录请求方法、路径、状态码、IP 与端到端耗时
		globalMW.Compress(),    // 5. 传输压缩：响应体透明 Gzip/Brotli 压缩
		globalMW.ETag(),        // 6. 缓存协商：自动生成 ETag 并支持 304 Not Modified
		globalMW.CORS(),        // 7. 跨域策略：注入跨域标头并对 OPTIONS 预检请求快速短路
		globalMW.Helmet(),      // 8. 浏览器安全头：Fiber v3 官方 Helmet 注入 nosniff、DENY、no-referrer 与 HSTS 等标头
		globalMW.Recovery(),    // 9. 恐慌兜底恢复：捕获未处理 panic 记录堆栈并转为 500 错误
		globalMW.Sentry(),      // 10. 异常监控上报：挂载 Sentry 请求 Hub 并捕获 panic 与 5xx 异常
		globalMW.Timeout(),     // 11. 超时控制：请求生命周期截止期控制，超时抛出 504（长连接豁免）
		globalMW.BodyLimit(),   // 12. 请求体限额：限制请求体大小（普通 1MiB / 上传 32MiB），防大包攻击
		globalMW.Idempotency(), // 13. 官方幂等控制：基于 X-Idempotency-Key 进行并发锁定与成功响应回放
	)

	// 注册根路径与系统探针路由（/、/livez、/readyz）
	system.RegisterRoutes(app, systemHandler)

	// 仅在开启 ENABLE_DOCS 时注册 API 文档路由
	if cfg.App.EnableDocs {
		registerDocs(app)
	}

	v1 := app.Group("/v1")
	auth.RegisterRoutes(v1, authHandler, routeMW)
	profile.RegisterRoutes(v1, profileHandler, routeMW)
	adminauth.RegisterAdminAuthRoutes(v1, adminAuthHandler, routeMW)
	admin.RegisterAdminRoutes(v1, adminHandler, routeMW)
	realtimeFeature.RegisterRoutes(v1, realtimeHandler, routeMW)
	paymentFeature.RegisterRoutes(v1, paymentHandler, routeMW)

	// 统一 404 兜底路由
	app.Use(func(c fiber.Ctx) error {
		return errs.NotFound("")
	})

	return app, nil
}

// InspectEngine 组装用于路由探测与文档自省的轻量级 Fiber 应用实例（零数据库与缓存网络连接）。
func InspectEngine(cfg *config.Config) (*fiber.App, error) {
	if cfg == nil {
		return nil, errors.New("bootstrap: inspect engine requires a non-nil config")
	}

	globalMW := global.New(global.Deps{
		CORS:           cfg.CORS,
		Production:     cfg.IsProduction(),
		RequestTimeout: cfg.App.Server.RequestTimeout,
	})

	routeMW := middleware.New(middleware.Deps{
		Limiter: cfg.Limiter,
	})

	return buildEngine(
		cfg,
		globalMW,
		routeMW,
		&system.Handler{},
		&auth.Handler{},
		&profile.Handler{},
		&admin.Handler{},
		&adminauth.Handler{},
		&realtimeFeature.Handler{},
		&paymentFeature.Handler{},
	)
}
