package middleware

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/i18n"
	"bfiber/internal/platform/validator"
)

// ErrorHandler 返回适用于 fiber.Config.ErrorHandler 的错误处理句柄，
// 集中解析业务码、执行多语言翻译并写回统一 JSON 响应。
func (m *Middleware) ErrorHandler() fiber.ErrorHandler {
	var translator *i18n.Translator
	if m != nil {
		translator = m.translator
	}
	return NewErrorHandler(translator)
}

// NewErrorHandler 构造适用于 fiber.Config.ErrorHandler 的独立错误处理句柄。
func NewErrorHandler(translator *i18n.Translator) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		if err == nil {
			return nil
		}

		lang := fiber.GetReqHeader[string](c, fiber.HeaderAcceptLanguage)
		if translator != nil {
			lang = translator.Language(lang)
		}

		if fields, ok := validator.ExtractFields(translator, lang, err); ok {
			err = errs.Validation(fields)
		}

		renderErr := errs.Render(c, err)
		captureHTTPError(c, err)
		return renderErr
	}
}
