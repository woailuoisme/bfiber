// Package payment_test 验证支付与退款 Web 回调模块的路由、验签、业务分发与渠道应答契约。
package payment_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"bfiber/internal/config"
	featurePayment "bfiber/internal/features/payment"
	"bfiber/internal/middleware/global"
	platformPayment "bfiber/internal/platform/payment"
	"bfiber/internal/platform/payment/enums"
)

// setupPaymentTest 构造测试用的 Fiber 引擎、服务实例与 Fake 支付管理器。
func setupPaymentTest(t *testing.T) (*fiber.App, *featurePayment.Service, *platformPayment.Manager) {
	t.Helper()

	cfg := &config.Config{
		App: config.AppConfig{
			URL: "https://shop.example.com",
			Env: "testing",
		},
		Payment: config.PaymentConfig{
			FakeEnabled: true,
			CertsDir:    "./storage/app/certs",
		},
	}

	mgr, err := platformPayment.NewManager(cfg, zap.NewNop())
	require.NoError(t, err)

	svc := featurePayment.NewService(mgr, nil, cfg, nil)
	handler := featurePayment.NewHandler(svc)

	app := fiber.New(fiber.Config{
		ErrorHandler: global.New(global.Deps{}).ErrorHandler(),
	})
	v1 := app.Group("/v1")
	featurePayment.RegisterRoutes(v1, handler, nil)

	return app, svc, mgr
}

func serveHTTP(t *testing.T, app *fiber.App, w *httptest.ResponseRecorder, req *http.Request) {
	t.Helper()
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	w.Code = resp.StatusCode
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	body, _ := io.ReadAll(resp.Body)
	w.Body.Write(body)
}

// getFakeProvider 从管理器中提取指定渠道的 Fake Provider。
func getFakeProvider(t *testing.T, mgr *platformPayment.Manager, channel enums.PaymentChannel) *platformPayment.FakeProvider {
	t.Helper()
	provider, err := mgr.For(channel, platformPayment.FakeApplicationKey)
	require.NoError(t, err)
	fakeProvider, ok := provider.(*platformPayment.FakeProvider)
	require.True(t, ok)
	return fakeProvider
}

// failingCallbackHandler 模拟业务消费时发生异常的场景。
type failingCallbackHandler struct {
	err error
}

func (f *failingCallbackHandler) OnPaymentSuccess(_ context.Context, _ *platformPayment.PaymentResult) error {
	return f.err
}

func (f *failingCallbackHandler) OnRefundSuccess(_ context.Context, _ *platformPayment.RefundResult) error {
	return f.err
}

// recordingCallbackHandler 记录被回调事件的业务处理器。
type recordingCallbackHandler struct {
	payments []*platformPayment.PaymentResult
	refunds  []*platformPayment.RefundResult
}

func (r *recordingCallbackHandler) OnPaymentSuccess(_ context.Context, res *platformPayment.PaymentResult) error {
	r.payments = append(r.payments, res)
	return nil
}

func (r *recordingCallbackHandler) OnRefundSuccess(_ context.Context, res *platformPayment.RefundResult) error {
	r.refunds = append(r.refunds, res)
	return nil
}

func TestHandleNotification_WechatPaymentSuccess(t *testing.T) {
	engine, svc, mgr := setupPaymentTest(t)
	fake := getFakeProvider(t, mgr, enums.PaymentChannelWechat)

	recorder := &recordingCallbackHandler{}
	svc.SetCallbackHandler(recorder)

	// 预先入队微信支付成功通知
	fake.QueueNotification(&platformPayment.Notification{
		ID:   "NOTIFY_WX_001",
		Kind: enums.PaymentNotificationKindPayment,
		Payment: &platformPayment.PaymentResult{
			OutTradeNo:     "ORDER_WX_001",
			ChannelTradeNo: "WX_TX_12345678",
			PaidAmount:     8800,
			Status:         enums.PaymentStatusSucceeded,
		},
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/wechat",
		bytes.NewBufferString(`{"id":"NOTIFY_WX_001"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.JSONEq(t, `{"code":"SUCCESS","message":"OK"}`, w.Body.String())

	// 验证业务钩子被成功调用
	require.Len(t, recorder.payments, 1)
	assert.Equal(t, "ORDER_WX_001", recorder.payments[0].OutTradeNo)
	assert.Equal(t, "WX_TX_12345678", recorder.payments[0].ChannelTradeNo)
}

func TestHandleNotification_WechatRefundSuccess(t *testing.T) {
	engine, svc, mgr := setupPaymentTest(t)
	fake := getFakeProvider(t, mgr, enums.PaymentChannelWechat)

	recorder := &recordingCallbackHandler{}
	svc.SetCallbackHandler(recorder)

	// 预先入队微信退款成功通知
	fake.QueueNotification(&platformPayment.Notification{
		ID:   "NOTIFY_WX_REFUND_001",
		Kind: enums.PaymentNotificationKindRefund,
		Refund: &platformPayment.RefundResult{
			OutTradeNo:      "ORDER_WX_001",
			OutRefundNo:     "REF_WX_001",
			ChannelRefundNo: "WX_REF_987654",
			RefundAmount:    8800,
			Status:          enums.PaymentRefundStatusSucceeded,
		},
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/wechat",
		bytes.NewBufferString(`{"id":"NOTIFY_WX_REFUND_001"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"code":"SUCCESS","message":"OK"}`, w.Body.String())

	require.Len(t, recorder.refunds, 1)
	assert.Equal(t, "ORDER_WX_001", recorder.refunds[0].OutTradeNo)
	assert.Equal(t, "REF_WX_001", recorder.refunds[0].OutRefundNo)
}

func TestHandleNotification_AlipayPaymentSuccess(t *testing.T) {
	engine, svc, mgr := setupPaymentTest(t)
	fake := getFakeProvider(t, mgr, enums.PaymentChannelAlipay)

	recorder := &recordingCallbackHandler{}
	svc.SetCallbackHandler(recorder)

	// 预先入队支付宝支付通知
	fake.QueueNotification(&platformPayment.Notification{
		ID:   "NOTIFY_ALI_001",
		Kind: enums.PaymentNotificationKindPayment,
		Payment: &platformPayment.PaymentResult{
			OutTradeNo:     "ORDER_ALI_001",
			ChannelTradeNo: "ALI_TRADE_888888",
			PaidAmount:     12800,
			Status:         enums.PaymentStatusSucceeded,
		},
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/alipay",
		bytes.NewBufferString(`out_trade_no=ORDER_ALI_001&trade_status=TRADE_SUCCESS`),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
	assert.Equal(t, "success", w.Body.String())

	require.Len(t, recorder.payments, 1)
	assert.Equal(t, "ORDER_ALI_001", recorder.payments[0].OutTradeNo)
}

func TestHandleNotification_CustomAppKey(t *testing.T) {
	engine, svc, mgr := setupPaymentTest(t)
	// 默认回调处理器会失败关闭（未接线业务时不得回执成功），本用例聚焦 app 参数路由，故注入记录器。
	svc.SetCallbackHandler(&recordingCallbackHandler{})
	fake := getFakeProvider(t, mgr, enums.PaymentChannelWechat)

	fake.QueueNotification(&platformPayment.Notification{
		ID:   "NOTIFY_WX_APP_001",
		Kind: enums.PaymentNotificationKindPayment,
		Payment: &platformPayment.PaymentResult{
			OutTradeNo: "ORDER_WX_APP_001",
			Status:     enums.PaymentStatusSucceeded,
		},
	})

	// 指定 URL 路径中的 app 参数
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/wechat/fake",
		bytes.NewBufferString(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"code":"SUCCESS","message":"OK"}`, w.Body.String())
}

func TestHandleNotification_BusinessFailure_TriggersRetry(t *testing.T) {
	t.Run("微信业务消费报错返回FAIL应答", func(t *testing.T) {
		engine, svc, mgr := setupPaymentTest(t)
		fake := getFakeProvider(t, mgr, enums.PaymentChannelWechat)

		// 注入业务处理失败
		svc.SetCallbackHandler(&failingCallbackHandler{err: errors.New("database deadlock on order state update")})

		fake.QueueNotification(&platformPayment.Notification{
			ID:   "NOTIFY_WX_FAIL",
			Kind: enums.PaymentNotificationKindPayment,
			Payment: &platformPayment.PaymentResult{
				OutTradeNo: "ORDER_WX_FAIL",
				Status:     enums.PaymentStatusSucceeded,
			},
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/v1/payments/notify/wechat",
			bytes.NewBufferString(`{}`),
		)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		serveHTTP(t, engine, w, req)

		// 微信端要求返回 200 且 body 包含 FAIL 触发重试
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"code":"FAIL","message":"FAIL"}`, w.Body.String())
	})

	t.Run("支付宝业务消费报错返回fail应答", func(t *testing.T) {
		engine, svc, mgr := setupPaymentTest(t)
		fake := getFakeProvider(t, mgr, enums.PaymentChannelAlipay)

		svc.SetCallbackHandler(&failingCallbackHandler{err: errors.New("external inventory deduction failed")})

		fake.QueueNotification(&platformPayment.Notification{
			ID:   "NOTIFY_ALI_FAIL",
			Kind: enums.PaymentNotificationKindPayment,
			Payment: &platformPayment.PaymentResult{
				OutTradeNo: "ORDER_ALI_FAIL",
				Status:     enums.PaymentStatusSucceeded,
			},
		})

		req := httptest.NewRequest(
			http.MethodPost,
			"/v1/payments/notify/alipay",
			bytes.NewBufferString(`{}`),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		serveHTTP(t, engine, w, req)

		// 支付宝要求返回 fail 字符串触发重试
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "fail", w.Body.String())
	})
}

func TestHandleNotification_UnsupportedChannel(t *testing.T) {
	engine, _, _ := setupPaymentTest(t)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/paypal",
		bytes.NewBufferString(`{}`),
	)
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleNotification_VerifySignatureFailure(t *testing.T) {
	engine, _, _ := setupPaymentTest(t)

	// Fake Provider 队列未预填任何通知时，ParseNotification 会报错模拟验签/解析失败
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/wechat",
		bytes.NewBufferString(`{"invalid":"signature"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"code":"FAIL","message":"FAIL"}`, w.Body.String())
}

func TestHandleAlipayReturn_Success(t *testing.T) {
	engine, _, mgr := setupPaymentTest(t)
	fake := getFakeProvider(t, mgr, enums.PaymentChannelAlipay)

	fake.QueueNotification(&platformPayment.Notification{
		ID:   "RETURN_ALI_001",
		Kind: enums.PaymentNotificationKindPayment,
		Payment: &platformPayment.PaymentResult{
			OutTradeNo: "ORDER_ALI_RETURN_001",
			Status:     enums.PaymentStatusSucceeded,
		},
	})

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/payments/return/alipay?out_trade_no=ORDER_ALI_RETURN_001&trade_status=TRADE_SUCCESS",
		nil,
	)
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	expectedURL := "https://shop.example.com/orders/ORDER_ALI_RETURN_001?status=success"
	assert.Equal(t, expectedURL, w.Header().Get("Location"))
}

func TestHandleAlipayReturn_Failure(t *testing.T) {
	engine, _, _ := setupPaymentTest(t)

	// 未注入通知，模拟验签失败
	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/payments/return/alipay?out_trade_no=ORDER_ALI_RETURN_002",
		nil,
	)
	w := httptest.NewRecorder()

	serveHTTP(t, engine, w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	expectedURL := "https://shop.example.com/orders?status=failed"
	assert.Equal(t, expectedURL, w.Header().Get("Location"))
}

func TestHandleNotification_GatewayNotInitialized(t *testing.T) {
	cfg := &config.Config{
		App: config.AppConfig{URL: "https://shop.example.com"},
	}
	// paymentManager 为 nil
	svc := featurePayment.NewService(nil, nil, cfg, nil)
	handler := featurePayment.NewHandler(svc)

	app := fiber.New()
	v1 := app.Group("/v1")
	featurePayment.RegisterRoutes(v1, handler, nil)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/payments/notify/wechat",
		bytes.NewBufferString(`{}`),
	)
	w := httptest.NewRecorder()

	serveHTTP(t, app, w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"code":"FAIL","message":"FAIL"}`, w.Body.String())
}
