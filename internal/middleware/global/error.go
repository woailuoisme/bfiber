package global

import (
	"errors"

	govalidator "github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/validator"
)

// ErrorHandler 返回适用于 fiber.Config.ErrorHandler 的错误处理句柄，
// 集中解析业务码、执行多语言翻译并写回统一 JSON 响应。
func (m *Middleware) ErrorHandler() fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		if err == nil {
			return nil
		}

		if fields, ok := m.extractValidationFields(c, err); ok {
			err = errs.Validation(fields)
		}

		renderErr := errs.Render(c, err)
		captureHTTPError(c, err)
		return renderErr
	}
}

// extractValidationFields 提取并翻译入参校验错误字典。
func (m *Middleware) extractValidationFields(c fiber.Ctx, err error) (map[string][]string, bool) {
	if valErr, ok := errors.AsType[govalidator.ValidationErrors](err); ok {
		lang := c.Get("Accept-Language")
		if m.translator != nil {
			lang = m.translator.Language(lang)
		}
		return validator.Translate(m.translator, lang, valErr), true
	}

	return nil, false
}
