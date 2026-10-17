package bootstrap

import (
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"

	"bfiber/internal/config"
	adminauth "bfiber/internal/features/admin/auth"
	admin "bfiber/internal/features/admin/user"
	"bfiber/internal/features/client/auth"
	"bfiber/internal/features/client/profile"
	paymentFeature "bfiber/internal/features/payment"
	realtimeFeature "bfiber/internal/features/realtime"
	"bfiber/internal/features/system"
	"bfiber/internal/middleware"
)

// RouteDeps 汇聚全系统路由组装所需的运行时依赖与切片处理器。
type RouteDeps struct {
	Config     *config.Config
	Middleware *middleware.Middleware
	DB         *bun.DB

	// 核心业务 Handler 句柄
	SystemHandler    *system.Handler
	AuthHandler      *auth.Handler
	ProfileHandler   *profile.Handler
	AdminHandler     *admin.Handler
	AdminAuthHandler *adminauth.Handler
	RealtimeHandler  *realtimeFeature.Handler
	PaymentHandler   *paymentFeature.Handler
}

// DBConn 安全返回底层数据库连接句柄（自省模式下为 nil）。
func (d *RouteDeps) DBConn() bun.IDB {
	if d == nil {
		return nil
	}
	return d.DB
}

// NewInspectRouteDeps 构造用于路由探测与文档自省的轻量级 RouteDeps 实例。
func NewInspectRouteDeps(cfg *config.Config, mw *middleware.Middleware) *RouteDeps {
	return &RouteDeps{
		Config:           cfg,
		Middleware:       mw,
		DB:               nil,
		SystemHandler:    &system.Handler{},
		AuthHandler:      &auth.Handler{},
		ProfileHandler:   &profile.Handler{},
		AdminHandler:     &admin.Handler{},
		AdminAuthHandler: &adminauth.Handler{},
		RealtimeHandler:  &realtimeFeature.Handler{},
		PaymentHandler:   &paymentFeature.Handler{},
	}
}

// registerAllRoutes 统一编排系统所有路由、组前缀及动态 CRUD 业务切片。
func registerAllRoutes(app *fiber.App, deps *RouteDeps) {
	if deps == nil {
		return
	}

	mw := deps.Middleware

	// 1. 注册根路径与系统探针路由（/、/livez、/readyz）
	if deps.SystemHandler != nil {
		system.RegisterRoutes(app, deps.SystemHandler)
	}

	// 2. 仅在开启 ENABLE_DOCS 时注册 API 文档路由
	if deps.Config != nil && deps.Config.App.EnableDocs {
		registerDocs(app)
	}

	// 3. /v1 业务主路由分组
	v1 := app.Group("/v1")
	if mw != nil && mw.RequestTimeout() > 0 {
		v1.Use(mw.Timeout())
	}

	// 客户端内置认证与资料路由
	if deps.AuthHandler != nil {
		auth.RegisterRoutes(v1, deps.AuthHandler, mw)
	}
	if deps.ProfileHandler != nil {
		profile.RegisterRoutes(v1, deps.ProfileHandler, mw)
	}

	// 管理端内置认证与用户管理路由
	if deps.AdminAuthHandler != nil {
		adminauth.RegisterAdminAuthRoutes(v1, deps.AdminAuthHandler, mw)
	}
	if deps.AdminHandler != nil {
		admin.RegisterAdminRoutes(v1, deps.AdminHandler, mw)
	}

	// 实时与支付拓展功能路由
	if deps.RealtimeHandler != nil {
		realtimeFeature.RegisterRoutes(v1, deps.RealtimeHandler, mw)
	}
	if deps.PaymentHandler != nil {
		paymentFeature.RegisterRoutes(v1, deps.PaymentHandler, mw)
	}

	// 动态注入的 CRUD 业务切片路由
	registerCrudRoutes(v1, deps)
}
