package system

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/healthcheck"
)

// RegisterRoutes 注册系统健康探测与根路径路由，端点不挂载常规鉴权与限流中间件。
func RegisterRoutes(router fiber.Router, handler *Handler) {
	liveChecker := healthcheck.New(healthcheck.Config{
		ResponseFormat: healthcheck.FormatJSON,
	})
	router.Get("/", liveChecker).Name("system.index")
	router.Get(healthcheck.LivenessEndpoint, liveChecker).Name("system.livez")
	router.Get(healthcheck.ReadinessEndpoint, handler.Readiness).Name("system.readyz")
}
