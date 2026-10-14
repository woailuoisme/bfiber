package adminauth

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
)

// RegisterAdminAuthRoutes 注册后台管理员认证端点（/v1/admin/auth/*）。
func RegisterAdminAuthRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/admin/auth")

	// 敏感公开端点（敏感限流防刷）
	sensitive := group.Group("", mw.Throttle(middleware.TierSensitive))
	sensitive.Post("/sign-in/email", handler.SignInEmail)
	sensitive.Post("/token/refresh", handler.Refresh)

	// 受保护会话端点（管理员守卫鉴权，无需额外 Casbin 业务权限码）
	protected := group.Group("", mw.Auth("admin"), mw.Throttle(middleware.TierAuthenticated))
	protected.Get("/get-session", handler.GetSession)
	protected.Post("/sign-out", handler.SignOut)
	protected.Post("/change-password", handler.ChangePassword)
}
