package system

import "github.com/gofiber/fiber/v3"

// RegisterRoutes 注册系统健康探测与根路径路由，端点不挂载常规鉴权与限流中间件。
func RegisterRoutes(router fiber.Router, handler *Handler) {
	router.Get("/", handler.Index)
	router.Get("/livez", handler.Liveness)
	router.Get("/readyz", handler.Readiness)
}
