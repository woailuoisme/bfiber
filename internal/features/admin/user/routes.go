// Package admin 实现后台系统用户管理模块 (/v1/admin/users)。
package admin

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/middleware"
	"bfiber/internal/platform/authorization"
)

// RegisterAdminRoutes 注册管理端点（/v1/admin/users）。
// 每个端点各自声明所需权限码，权限矩阵见 internal/platform/authorization/rbac/policy.csv。
func RegisterAdminRoutes(router fiber.Router, handler *Handler, mw *middleware.Middleware) {
	group := router.Group("/admin/users", mw.Auth("admin"), mw.Throttle(middleware.TierAuthenticated))

	group.Get("", mw.Can(authorization.ResourceUsers, authorization.ActionList), handler.ListUsers).Name("admin.users.list")
	group.Get("/export", mw.Can(authorization.ResourceUsers, authorization.ActionList), handler.ExportUsers).Name("admin.users.export")
	group.Post("", mw.Can(authorization.ResourceUsers, authorization.ActionCreate), handler.CreateUser).Name("admin.users.create")
	group.Get("/:id<user_id>", mw.Can(authorization.ResourceUsers, authorization.ActionRead), handler.GetUser).Name("admin.users.get")
	group.Patch("/:id<user_id>", mw.Can(authorization.ResourceUsers, authorization.ActionUpdate), handler.UpdateUser).Name("admin.users.update")
	group.Delete("/:id<user_id>", mw.Can(authorization.ResourceUsers, authorization.ActionDelete), handler.DeleteUser).Name("admin.users.delete")
}
