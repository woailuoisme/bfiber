package middleware

import (
	"cmp"
	"errors"

	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/errs"
)

// maxBodyBytes 非上传请求的请求体大小上限（1 MiB），与 api.md 的 413 契约保持一致。
const maxBodyBytes = 1 << 20

// defaultUploadBodyBytes multipart 上传请求的兜底上限（32 MiB）。
const defaultUploadBodyBytes = 32 << 20

// BodyLimit 【全局中间件】限制请求体大小上限，防止超大请求耗尽内存。
//
// multipart/form-data 上传按 UploadMaxBody（媒体配额）单独放行，
// 否则 20MB 的媒体上传会被 1 MiB 的普通请求上限提前拦掉。
func (m *Middleware) BodyLimit() fiber.Handler {
	var maxBody, uploadMaxBody int64
	if m != nil {
		maxBody = m.maxBody
		uploadMaxBody = m.uploadMaxBody
	}
	return NewBodyLimit(maxBody, uploadMaxBody)
}

// NewBodyLimit 构造独立的请求体限制中间件。
func NewBodyLimit(maxBody, uploadMaxBody int64) fiber.Handler {
	return func(c fiber.Ctx) error {
		limit := cmp.Or(maxBody, maxBodyBytes)
		if c.Is(fiber.MIMEMultipartForm) {
			limit = cmp.Or(uploadMaxBody, defaultUploadBodyBytes)
		}

		contentLength := int64(c.Request().Header.ContentLength())
		if contentLength > limit {
			return errs.RequestTooLarge(errors.New("http: request body too large"))
		}

		return c.Next()
	}
}
