package realtime

import (
	"cmp"

	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	platformRealtime "bfiber/internal/platform/realtime"
	"bfiber/internal/platform/response"
)

// Handler 是实时通信端点的 HTTP 适配层。
type Handler struct {
	service *Service
}

// NewHandler 构造 Handler。
func NewHandler(service *Service) *Handler {
	return &Handler{
		service: service,
	}
}

// GetToken 处理 GET /v1/realtime/token。
//
// @Summary      获取实时通信连接凭据
// @Description  为当前已认证用户签发直连 Centrifugo (WSS/SSE) 所需的 JWT 令牌，有效期由 CENTRIFUGO_TOKEN_TTL 决定（默认 2 小时）。
// @Tags         实时通信
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} TokenResource "实时通信连接凭据"
// @x-order      1
// @Router       /realtime/token [get]
func (h *Handler) GetToken(c fiber.Ctx) error {
	id, err := authentication.RequireIdentity(c.Context())
	if err != nil {
		return err
	}

	res, err := h.service.IssueToken(c.Context(), id.UserID, id.Role)
	if err != nil {
		return err
	}
	return response.OK(c, res)
}

// ProxySubscribe 处理 POST /v1/realtime/proxy/subscribe。
//
// @Summary      Centrifugo 订阅鉴权代理端点
// @Description  接收 Centrifugo 反向代理发起的频道订阅鉴权请求，校验 HMAC-SHA256 签名并调度频道权限策略。
// @Tags         实时通信
// @Accept       json
// @Produce      json
// @Param        payload body SubscribeBody true "Centrifugo 订阅代理请求"
// @Success      200 {object} SubscribeResource "Centrifugo 订阅代理响应"
// @x-order      2
// @Router       /realtime/proxy/subscribe [post]
func (h *Handler) ProxySubscribe(c fiber.Ctx) error {
	body := c.Body()

	// 签名头优先读取 X-Centrifugo-Signature 或 X-Signature
	sig := cmp.Or(
		fiber.GetReqHeader[string](c, platformRealtime.CentrifugoSignatureHeader),
		fiber.GetReqHeader[string](c, platformRealtime.CentrifugoSignatureLegacyHeader),
	)

	resp, err := h.service.AuthorizeSubscribe(c.Context(), body, sig)
	if err != nil {
		// 本端点的错误体遵循 Centrifugo Proxy 协议（error.code 为状态码），
		// 因此这里不用 errs.Render 的应用统一错误模型，只用它定状态码与文案。
		status, mapped := errs.Map(err)
		return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": status, "message": mapped.Message}})
	}

	return c.Status(fiber.StatusOK).JSON(resp)
}
