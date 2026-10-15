package auth

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
)

// RegisterRoutes 在 /v1/auth 下注册全部认证端点。
// 敏感端点走最严配额，需鉴权的端点走已认证配额，两者互不叠加。
func RegisterRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/auth")

	// 敏感端点：级联敏感配额限流中间件。
	sensitive := mw.Throttle(middleware.TierSensitive)
	group.Post("/sign-up/email", sensitive, handler.SignUpEmail).Name("auth.signup.email")
	group.Post("/sign-in/email", sensitive, handler.SignInEmail).Name("auth.signin.email")
	group.Post("/sign-in/username", sensitive, handler.SignInUsername).Name("auth.signin.username")
	group.Post("/token/refresh", sensitive, handler.Refresh).Name("auth.token.refresh")
	group.Post("/send-verification-otp", sensitive, handler.SendVerificationOTP).Name("auth.otp.send")
	group.Post("/verify-email", sensitive, handler.VerifyEmail).Name("auth.email.verify")
	group.Post("/forget-password", sensitive, handler.ForgetPassword).Name("auth.password.forget")
	group.Post("/request-password-reset", sensitive, handler.RequestPasswordReset).Name("auth.password.request_reset")
	group.Post("/reset-password", sensitive, handler.ResetPassword).Name("auth.password.reset")

	// 需鉴权端点：级联已认证配额与身份校验中间件。
	authGuard := mw.Auth("client")
	authThrottle := mw.Throttle(middleware.TierAuthenticated)
	group.Get("/get-session", authGuard, authThrottle, handler.GetSession).Name("auth.session.get")
	group.Post("/sign-out", authGuard, authThrottle, handler.SignOut).Name("auth.signout")
	group.Post("/change-password", authGuard, authThrottle, handler.ChangePassword).Name("auth.password.change")
}
