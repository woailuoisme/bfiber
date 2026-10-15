package system

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"bfiber/internal/platform/ready"
	"bfiber/internal/platform/response"
)

// probeTimeout 是就绪探针调度的总超时时限。
const probeTimeout = 2 * time.Second

// Handler 是探针端点的 HTTP 适配层。
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler 构造系统探针 Handler。
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{service: service, logger: logger}
}

// Readiness 处理 GET /readyz 就绪探针请求：并发探测各依赖，关键项失败返回 503，非关键项失败返回 200 degraded。
func (h *Handler) Readiness(c fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), probeTimeout)
	defer cancel()

	result, errs := h.service.Check(ctx)
	for name, err := range errs {
		h.logger.Warn("readiness probe failed", zap.Error(err), zap.String("component", name))
	}

	if result.Status == ready.StatusNotReady {
		return c.Status(result.StatusCode()).JSON(result)
	}
	return response.OK(c, result)
}
