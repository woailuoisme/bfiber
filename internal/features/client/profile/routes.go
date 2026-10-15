package profile

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
)

// RegisterRoutes 注册前端用户个人档案端点 (/v1/users)。
func RegisterRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/users", mw.Auth("client"), mw.Throttle(middleware.TierAuthenticated))

	group.Get("/me", handler.GetProfile).Name("users.me.get")
	group.Patch("/me", handler.UpdateProfile).Name("users.me.update")
	group.Post("/me/avatar", handler.UploadAvatar).Name("users.me.avatar")
}
