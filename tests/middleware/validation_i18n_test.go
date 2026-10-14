package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/middleware/global"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/i18n"
	"bfiber/internal/platform/validator"
)

type updateProfilePayload struct {
	Name string `json:"name" validate:"required"`
}

// TestValidationMessageTranslation 验证入参校验文案会在中间件边界按 Accept-Language 进行多语言渲染。
func TestValidationMessageTranslation(t *testing.T) {
	translator, err := i18n.New(&config.Config{I18n: config.I18nConfig{DefaultLanguage: "en"}})
	require.NoError(t, err)

	cases := []struct {
		name     string
		language string
		want     string
	}{
		{name: "未指定语言时回退默认语言", want: "The name field is required."},
		{name: "按 Accept-Language 渲染中文", language: "zh-CN", want: "名称为必填项。"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := global.New(global.Deps{Translator: translator})
			app := fiber.New(fiber.Config{
				StructValidator: validator.NewFiberValidator(),
				ErrorHandler:    m.ErrorHandler(),
			})
			app.Patch("/profile", func(c fiber.Ctx) error {
				var p updateProfilePayload
				return c.Bind().Body(&p)
			})

			req := httptest.NewRequest(http.MethodPatch, "/profile", bytes.NewBufferString(`{}`))
			req.Header.Set("Content-Type", "application/json")
			if tc.language != "" {
				req.Header.Set("Accept-Language", tc.language)
			}
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

			bodyBytes, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var body errs.Response
			require.NoError(t, json.Unmarshal(bodyBytes, &body))
			assert.Equal(t, string(errs.CodeValidationFailed), body.Code)
			assert.Equal(t, []string{tc.want}, body.Errors["name"])
		})
	}
}
