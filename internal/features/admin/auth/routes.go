package adminauth

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
)

// RegisterAdminAuthRoutes 注册后台管理员认证端点（/v1/admin/auth/*）。
func RegisterAdminAuthRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/admin/auth")

	// 敏感公开端点（敏感限流防刷）
	sensitive := mw.Throttle(middleware.TierSensitive)
	group.Post("/sign-in/email", sensitive, handler.SignInEmail).Name("admin.auth.signin.email")
	group.Post("/token/refresh", sensitive, handler.Refresh).Name("admin.auth.token.refresh")

	// 受保护会话端点（管理员守卫鉴权，无需额外 Casbin 业务权限码）
	authGuard := mw.Auth("admin")
	authThrottle := mw.Throttle(middleware.TierAuthenticated)
	group.Get("/get-session", authGuard, authThrottle, handler.GetSession).Name("admin.auth.session.get")
	group.Post("/sign-out", authGuard, authThrottle, handler.SignOut).Name("admin.auth.signout")
	group.Post("/change-password", authGuard, authThrottle, handler.ChangePassword).Name("admin.auth.password.change")
}
