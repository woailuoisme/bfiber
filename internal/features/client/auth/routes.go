package auth

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
)

// RegisterRoutes 在 /v1/auth 下注册全部认证端点。
// 敏感端点走最严配额，需鉴权的端点走已认证配额，两者互不叠加。
func RegisterRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/auth")

	// 敏感端点：通过语义化子路由统一级联敏感配额限流中间件。
	sensitive := group.Group("", mw.Throttle(middleware.TierSensitive))
	sensitive.Post("/sign-up/email", handler.SignUpEmail)
	sensitive.Post("/sign-in/email", handler.SignInEmail)
	sensitive.Post("/sign-in/username", handler.SignInUsername)
	sensitive.Post("/token/refresh", handler.Refresh)
	sensitive.Post("/send-verification-otp", handler.SendVerificationOTP)
	sensitive.Post("/verify-email", handler.VerifyEmail)
	sensitive.Post("/forget-password", handler.ForgetPassword)
	sensitive.Post("/request-password-reset", handler.RequestPasswordReset)
	sensitive.Post("/reset-password", handler.ResetPassword)

	// 需鉴权端点：通过语义化子路由统一级联已认证配额与身份校验中间件。
	protected := group.Group("", mw.Auth("client"), mw.Throttle(middleware.TierAuthenticated))
	protected.Get("/get-session", handler.GetSession)
	protected.Post("/sign-out", handler.SignOut)
	protected.Post("/change-password", handler.ChangePassword)
}
