package payment

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"go.uber.org/zap"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
	platformPayment "bfiber/internal/platform/payment"
	"bfiber/internal/platform/payment/enums"
)

// Handler 承接支付渠道的 Webhook 异步通知与客户端同步跳转。
type Handler struct {
	service *Service
	gateway platformPayment.Gateway
}

// NewHandler 构造支付回调 HTTP 处理器，网关自 service 取得，无需重复注入。
func NewHandler(service *Service) *Handler {
	var gw platformPayment.Gateway
	if service != nil {
		gw = service.Gateway()
	}
	return &Handler{service: service, gateway: gw}
}

// HandleNotification 接收微信与支付宝支付/退款异步回调通知。
//
// @Summary      支付与退款异步通知回调
// @Description  接收微信或支付宝服务端的支付与退款异步通知，由网关执行公钥/证书验签、幂等防重检查与业务状态机推进，返回渠道约定的确认应答。
// @Tags         支付中心
// @Accept       json,x-www-form-urlencoded
// @Produce      json,plain
// @Param        channel path string true "支付渠道 (wechat, alipay)" enums(wechat, alipay)
// @Param        app     path string false "应用标识 (可选，缺省时使用渠道默认应用)"
// @Success      200 {object} CallbackAckResource "回调验签与业务消费成功"
// @x-order      1
// @Router       /payments/notify/{channel} [post]
func (h *Handler) HandleNotification(c fiber.Ctx) error {
	var pathParams PaymentNotifyPathParams
	if err := c.Bind().URI(&pathParams); err != nil {
		return err
	}

	channel := enums.PaymentChannel(pathParams.Channel)
	if !channel.IsValid() {
		// 报文里不拼接调用方原始输入，渠道值只进结构化日志。
		logger.FromContext(c.Context()).Warn("unsupported payment channel callback received", zap.String("channel", pathParams.Channel))
		return errs.BadRequest(errUnsupportedChannel)
	}

	if h.gateway == nil {
		return replyAck(c, platformPayment.FailureAck(channel))
	}

	httpReq, err := adaptor.ConvertRequest(c, false)
	if err != nil {
		logger.FromContext(c.Context()).Warn("failed to convert request for payment callback", zap.Error(err))
		return replyAck(c, platformPayment.FailureAck(channel))
	}

	// 1. 严格验签并解析 HTTP 报文 (微信商户证书/微信平台证书/支付宝公钥证书)
	notification, err := h.gateway.ParseNotification(c.Context(), channel, pathParams.App, httpReq)
	if err != nil {
		logger.FromContext(c.Context()).Warn("payment callback verification or parsing failed",
			zap.Error(err),
			zap.String("channel", channel.String()),
			zap.String("app_key", pathParams.App),
		)

		return replyAck(c, platformPayment.FailureAck(channel))
	}

	// 2. 将解析好的纯领域通知交给 Service 协调幂等控制与事件分发
	ack, processErr := h.service.ProcessNotification(c.Context(), channel, notification)
	if processErr != nil {
		logger.FromContext(c.Context()).Warn("payment notification processing failed", zap.Error(processErr))
	}

	// 渠道统一按渠道协议返回 200 及对应报文内容。
	return replyAck(c, ack)
}

// HandleAlipayReturn 接收支付宝前端客户端同步重定向跳转 (return_url)。
//
// @Summary      支付宝客户端同步重定向跳转
// @Description  用户在支付宝完成付款后由浏览器重定向跳回，经公钥验签通过后 302 跳转至前端订单结果展示页。仅作展示导流，实际支付状态严格以异步通知为准。
// @Tags         支付中心
// @Accept       html
// @Produce      html
// @Success      302 {string} string "重定向至前端订单展示页"
// @x-order      2
// @Router       /payments/return/alipay [get]
func (h *Handler) HandleAlipayReturn(c fiber.Ctx) error {
	ctx := c.Context()
	log := logger.FromContext(ctx)

	if h.gateway == nil {
		return c.Redirect().Status(fiber.StatusFound).To(h.service.FailureRedirectURL())
	}

	httpReq, err := adaptor.ConvertRequest(c, false)
	if err != nil {
		log.Warn("failed to convert request for alipay return", zap.Error(err))
		return c.Redirect().Status(fiber.StatusFound).To(h.service.FailureRedirectURL())
	}

	// 对 URL Query 参数进行支付宝公钥验签
	notification, err := h.gateway.ParseNotification(ctx, enums.PaymentChannelAlipay, "", httpReq)
	if err != nil {
		log.Warn("alipay return verification failed, redirecting to order failure page", zap.Error(err))
		return c.Redirect().Status(fiber.StatusFound).To(h.service.FailureRedirectURL())
	}

	var queryParams AlipayReturnQueryParams
	_ = c.Bind().Query(&queryParams)

	outTradeNo := queryParams.OutTradeNo
	if notification != nil && notification.Payment != nil && notification.Payment.OutTradeNo != "" {
		outTradeNo = notification.Payment.OutTradeNo
	}

	redirectURL := h.service.BuildAlipayReturnURL(outTradeNo)
	log.Info("alipay return callback verified, redirecting to frontend",
		zap.String("out_trade_no", outTradeNo),
		zap.String("redirect_url", redirectURL),
	)

	return c.Redirect().Status(fiber.StatusFound).To(redirectURL)
}

// replyAck 将渠道应答报文写回 HTTP 响应。
func replyAck(c fiber.Ctx, ack platformPayment.NotificationAck) error {
	c.Set("Content-Type", ack.ContentType)
	return c.Status(fiber.StatusOK).Send([]byte(ack.Body))
}
