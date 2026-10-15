// Package mailer_test 验证邮件服务抽象、Mailpit/Resend 驱动契约与 HTML 模板渲染。
package mailer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/platform/mailer"
)

// testMailable 模拟带模板的结构化邮件。
type testMailable struct {
	subject string
	tmpl    string
	data    map[string]any
}

func (m *testMailable) Subject() string {
	return m.subject
}

func (m *testMailable) Template() (string, map[string]any) {
	return m.tmpl, m.data
}

func TestMailpitDriverLifecycle(t *testing.T) {
	cfg := config.MailConfig{
		Driver:      "mailpit",
		FromAddress: "no-reply@bfiber.local",
		FromName:    "bfiber",
		Mailpit: config.MailpitConfig{
			Host: "127.0.0.1",
			Port: 1025,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, client)

	// 验证收件人空校验
	err = client.Send(context.Background(), mailer.Message{
		To:      "",
		Subject: "Test",
		Body:    "Body",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recipient 'To' cannot be empty")

	// 验证安全关闭
	require.NoError(t, client.Close())
}

func TestResendDriverMissingAPIKey(t *testing.T) {
	cfg := config.MailConfig{
		Driver: "resend",
		Resend: config.ResendConfig{
			APIKey: "",
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "api key cannot be empty")
}

func TestResendDriverSendSuccess(t *testing.T) {
	receivedAuth := ""
	receivedAgent := ""
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedAgent = r.Header.Get("User-Agent")

		err := json.NewDecoder(r.Body).Decode(&receivedBody)
		assert.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_12345678"}`))
	}))
	defer server.Close()

	cfg := config.MailConfig{
		Driver:      "resend",
		FromAddress: "noreply@example.com",
		FromName:    "Shop",
		Resend: config.ResendConfig{
			APIKey:  "re_test_secret_key",
			BaseURL: server.URL,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		require.NoError(t, client.Close())
	}()

	msg := mailer.Message{
		To:      "buyer@example.com",
		Cc:      []string{"cc@example.com"},
		Bcc:     []string{"audit@example.com"},
		Subject: "订单支付凭据",
		Body:    "您的订单已成功支付",
		IsHTML:  false,
	}

	err = client.Send(context.Background(), msg)
	require.NoError(t, err)

	assert.Equal(t, "Bearer re_test_secret_key", receivedAuth)
	assert.Equal(t, "bfiber-mailer/1.0", receivedAgent)
	assert.Equal(t, "Shop <noreply@example.com>", receivedBody["from"])
	assert.Equal(t, []any{"buyer@example.com"}, receivedBody["to"])
	assert.Equal(t, "订单支付凭据", receivedBody["subject"])
	assert.Equal(t, "您的订单已成功支付", receivedBody["text"])
}

func TestResendDriverSendWithAttachments(t *testing.T) {
	tempDir := t.TempDir()
	invoiceFile := filepath.Join(tempDir, "invoice.pdf")
	require.NoError(t, os.WriteFile(invoiceFile, []byte("%PDF-1.4 Mock Invoice Content"), 0o600))

	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_with_attach"}`))
	}))
	defer server.Close()

	cfg := config.MailConfig{
		Driver: "resend",
		Resend: config.ResendConfig{
			APIKey:  "re_test_key",
			BaseURL: server.URL,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)

	err = client.Send(context.Background(), mailer.Message{
		To:          "buyer@example.com",
		Subject:     "发票附件",
		Body:        "请查收附件",
		Attachments: []string{invoiceFile},
	})
	require.NoError(t, err)

	attachments, ok := receivedBody["attachments"].([]any)
	require.True(t, ok)
	require.Len(t, attachments, 1)

	attMap, ok := attachments[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "invoice.pdf", attMap["filename"])
	assert.NotEmpty(t, attMap["content"])
}

func TestResendDriverServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"statusCode":422,"name":"validation_error","message":"Domain not verified"}`))
	}))
	defer server.Close()

	cfg := config.MailConfig{
		Driver: "resend",
		Resend: config.ResendConfig{
			APIKey:  "re_invalid_key",
			BaseURL: server.URL,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)

	err = client.Send(context.Background(), mailer.Message{
		To:      "buyer@example.com",
		Subject: "测试失败",
		Body:    "正文",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Domain not verified")
}

func TestResendDriverRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("retry-after", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"Too many requests"}`))
	}))
	defer server.Close()

	cfg := config.MailConfig{
		Driver: "resend",
		Resend: config.ResendConfig{
			APIKey:  "re_rate_limited",
			BaseURL: server.URL,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)

	err = client.Send(context.Background(), mailer.Message{
		To:      "buyer@example.com",
		Subject: "测试限流",
		Body:    "正文",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestSendMailableTemplateRendering(t *testing.T) {
	var receivedHTML string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if html, ok := body["html"].(string); ok {
			receivedHTML = html
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"mailable_test"}`))
	}))
	defer server.Close()

	cfg := config.MailConfig{
		Driver: "resend",
		Resend: config.ResendConfig{
			APIKey:  "re_test_key",
			BaseURL: server.URL,
		},
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)

	mailable := &testMailable{
		subject: "欢迎加入系统",
		tmpl:    "welcome",
		data: map[string]any{
			"Name": "Alex",
			"Button": map[string]any{
				"Text": "进入系统",
				"URL":  "https://example.com/dashboard",
			},
		},
	}

	err = client.SendMailable(context.Background(), "alex@example.com", mailable)
	require.NoError(t, err)
	assert.Contains(t, receivedHTML, "Alex")
	assert.Contains(t, receivedHTML, "进入系统")
	assert.Contains(t, receivedHTML, "https://example.com/dashboard")

	// 验证收件人空拦截卫语句
	err = client.SendMailable(context.Background(), "   ", mailable)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recipient email cannot be empty")

	// 验证 nil mailable 拦截
	err = client.SendMailable(context.Background(), "alex@example.com", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mailable cannot be nil")
}

func TestUnsupportedDriver(t *testing.T) {
	cfg := config.MailConfig{
		Driver: "sendgrid",
	}

	client, err := mailer.New(cfg, zap.NewNop())
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "unsupported mail driver")
}

func TestConfigValidation(t *testing.T) {
	t.Run("mailpit合法端口", func(t *testing.T) {
		mailCfg := config.MailConfig{
			Driver: "mailpit",
			Mailpit: config.MailpitConfig{
				Host: "127.0.0.1",
				Port: 1025,
			},
		}
		require.NoError(t, mailCfg.Validate())
	})

	t.Run("mailpit非法端口", func(t *testing.T) {
		mailCfg := config.MailConfig{
			Driver: "mailpit",
			Mailpit: config.MailpitConfig{
				Port: 99999,
			},
		}
		err := mailCfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mailpit port must be between 1 and 65535")
	})

	t.Run("resend缺少APIKey", func(t *testing.T) {
		mailCfg := config.MailConfig{
			Driver: "resend",
			Resend: config.ResendConfig{
				APIKey: "",
			},
		}
		err := mailCfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resend api key cannot be empty")
	})
	t.Run("非法驱动校验", func(t *testing.T) {
		mailCfg := config.MailConfig{
			Driver: "invalid_driver",
		}
		err := mailCfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported mail driver")
		assert.Contains(t, err.Error(), "(supported: 'mailpit', 'resend')")
	})

	t.Run("空驱动默认归一化回填为mailpit", func(t *testing.T) {
		mailCfg := config.MailConfig{
			Driver: "",
			Mailpit: config.MailpitConfig{
				Port: 1025,
			},
		}
		require.NoError(t, mailCfg.Validate())
		assert.Equal(t, "mailpit", mailCfg.Driver)
	})
}

func TestDefaultDriverIsMailpit(t *testing.T) {
	cfg := config.MailConfig{
		Driver: "",
	}
	client, err := mailer.New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, client)
	if da, ok := client.(interface{ Driver() string }); ok {
		assert.Equal(t, "mailpit", da.Driver())
	}
	require.NoError(t, client.Close())
}

func TestAttachmentAndAllAttachments(t *testing.T) {
	// 临时文件用于路径附件测试
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("disk content"), 0o600))

	t.Run("内存附件与路径附件 Bytes", func(t *testing.T) {
		memAtt := mailer.NewAttachmentFromBytes("doc.pdf", []byte("pdf data"), "application/pdf")
		data, err := memAtt.Bytes()
		require.NoError(t, err)
		assert.Equal(t, []byte("pdf data"), data)
		assert.Equal(t, "doc.pdf", memAtt.Filename)
		assert.Equal(t, "application/pdf", memAtt.ContentType)

		fileAtt := mailer.NewAttachmentFromPath(tmpFile)
		data, err = fileAtt.Bytes()
		require.NoError(t, err)
		assert.Equal(t, []byte("disk content"), data)
		assert.Equal(t, "test.txt", fileAtt.Filename)

		emptyAtt := mailer.Attachment{}
		_, err = emptyAtt.Bytes()
		require.Error(t, err)
	})

	t.Run("AllAttachments 合并路径与内存附件", func(t *testing.T) {
		msg := mailer.Message{
			Attachments: []string{tmpFile, ""},
			RawAttachments: []mailer.Attachment{
				mailer.NewAttachmentFromBytes("report.pdf", []byte("pdf content")),
			},
		}

		all := msg.AllAttachments()
		require.Len(t, all, 2)
		assert.Equal(t, "test.txt", all[0].Filename)
		assert.Equal(t, "report.pdf", all[1].Filename)
	})
}

type mockCustomMailer struct{}

func (m *mockCustomMailer) Send(_ context.Context, _ mailer.Message) error { return nil }
func (m *mockCustomMailer) SendMailable(_ context.Context, _ string, _ mailer.Mailable) error {
	return nil
}
func (m *mockCustomMailer) Close() error                 { return nil }
func (m *mockCustomMailer) Ping(_ context.Context) error { return nil }

func TestCustomDriverRegistry(t *testing.T) {
	mailer.RegisterDriver("custom-mock", func(_ config.MailConfig, _ *zap.Logger) (mailer.Mailer, error) {
		return &mockCustomMailer{}, nil
	})

	client, err := mailer.New(config.MailConfig{Driver: "custom-mock"}, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NoError(t, client.Close())
}

// TestMail_DisabledDelivery 验证当 MAIL_ENABLED=false 时跳过网络投递。
func TestMail_DisabledDelivery(t *testing.T) {
	for _, driver := range []string{"mailpit", "resend"} {
		t.Run(driver, func(t *testing.T) {
			cfg := config.MailConfig{
				Enabled:     new(bool), // *Enabled = false
				Driver:      driver,
				FromAddress: "test@example.com",
				Resend: config.ResendConfig{
					APIKey: "re_mock_key",
				},
			}
			require.False(t, cfg.IsEnabled())
			require.NoError(t, cfg.Validate())

			client, err := mailer.New(cfg, zap.NewNop())
			require.NoError(t, err)
			require.NotNil(t, client)

			err = client.Send(t.Context(), mailer.Message{
				To:      "user@example.com",
				Subject: "Test Subject",
				Body:    "Hello world",
			})
			require.NoError(t, err)
			require.NoError(t, client.Close())
		})
	}
}

// TestMailerPingerContract 验证 Mailer 驱动实现 Driver 与 Enabled 探针契约。
func TestMailerPingerContract(t *testing.T) {
	t.Run("mailpit", func(t *testing.T) {
		cfg := config.MailConfig{
			Driver: "mailpit",
		}
		client, err := mailer.New(cfg, zap.NewNop())
		require.NoError(t, err)
		if da, ok := client.(interface{ Driver() string }); ok {
			assert.Equal(t, "mailpit", da.Driver())
		}
		if ea, ok := client.(interface{ Enabled() bool }); ok {
			assert.True(t, ea.Enabled())
		}
		require.NoError(t, client.Close())
	})

	t.Run("resend", func(t *testing.T) {
		cfg := config.MailConfig{
			Driver: "resend",
			Resend: config.ResendConfig{
				APIKey: "re_test_123",
			},
		}
		client, err := mailer.New(cfg, zap.NewNop())
		require.NoError(t, err)
		require.NoError(t, client.Ping(t.Context()))
		if da, ok := client.(interface{ Driver() string }); ok {
			assert.Equal(t, "resend", da.Driver())
		}
		if ea, ok := client.(interface{ Enabled() bool }); ok {
			assert.True(t, ea.Enabled())
		}
		require.NoError(t, client.Close())
	})
}
