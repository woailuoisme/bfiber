package errs

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/oops"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"

	"bfiber/internal/platform/logger"
)

// PostgreSQL 错误码，用于把约束冲突翻译成对调用方有意义的响应。
const (
	pgErrUniqueViolation           = "23505"
	pgErrForeignKeyViolation       = "23503"
	pgErrNotNullViolation          = "23502"
	pgErrInvalidTextRepresentation = "22P02"
)

// Response 是统一错误响应体。
type Response struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Errors  map[string][]string `json:"errors,omitempty"`
}

// 敏感词特征，用于安全脱敏防止底层 SQL、连接信息与凭证外泄。
var sensitivePatterns = []string{
	"password=", "secret=", "token=", "authorization:",
	"select ", "insert into", "update ", "delete from", "drop table",
	"pq: ", "pgx: ", "sql: ",
}

// SanitizeMessage 对对外输出的错误描述进行脱敏与安全清洗。
func SanitizeMessage(msg string) string {
	lower := strings.ToLower(msg)
	if slices.ContainsFunc(sensitivePatterns, func(p string) bool {
		return strings.Contains(lower, p)
	}) {
		return msgInternal
	}
	return msg
}

// errorCodeContextKey 是 Fiber 上下文 Locals 承载已渲染错误码的键。
const errorCodeContextKey = "error_code"

// SetErrorCodeContext 把最终错误码写入 Fiber 上下文，供访问日志与监控采集。
func SetErrorCodeContext(c fiber.Ctx, code string) {
	if c == nil || code == "" {
		return
	}
	fiber.Locals(c, errorCodeContextKey, code)
}

// ErrorCodeFromContext 读取 Fiber 上下文中的错误码，缺失或类型不符时返回空串。
func ErrorCodeFromContext(c fiber.Ctx) string {
	if c == nil {
		return ""
	}
	return fiber.Locals[string](c, errorCodeContextKey)
}

// Render 映射统一错误响应并写回客户端。
//
// 5xx 记录错误根因与调用堆栈；4xx 由外层 AccessLog 统一输出访问日志，避免重复告警。
func Render(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	status, resp := Map(err)

	// 敏感错误信息脱敏保护
	resp.Message = SanitizeMessage(resp.Message)

	SetErrorCodeContext(c, resp.Code)

	if status >= fiber.StatusInternalServerError {
		// 5xx 记录底层根因与调用堆栈，避免对外统一文案遮蔽排错细节。
		log := logger.FromContext(c.Context())
		if panicErr, ok := AsPanic(err); ok {
			log.Error(
				"request panic recovered",
				zap.Any("panic", panicErr.Value),
				zap.String("stack", panicErr.Stack),
				zap.Int("status", status),
				zap.String("code", resp.Code),
			)
		} else {
			zapFields := []zap.Field{
				zap.String("stack", Stack(err)),
				zap.Error(RootCause(err)),
				zap.Int("status", status),
				zap.String("code", resp.Code),
			}
			if extra := OopsFields(err); len(extra) > 0 {
				zapFields = append(zapFields, extra...)
			}
			log.Error("request processing failed", zapFields...)
		}
	}

	return c.Status(status).JSON(resp)
}

var sensitiveKeyFragments = []string{
	"password", "secret", "token", "credential", "auth", "key",
}

func isSensitiveKey(k string) bool {
	lower := strings.ToLower(k)
	return slices.ContainsFunc(sensitiveKeyFragments, func(frag string) bool {
		return strings.Contains(lower, frag)
	})
}

func sanitizeValue(v any) any {
	if s, ok := v.(string); ok {
		return SanitizeMessage(s)
	}
	return v
}

// OopsFields 提取错误链中 oops 绑定的上下文信息，并自动对敏感键与敏感值执行脱敏处理。
func OopsFields(err error) []zap.Field {
	oopsErr, ok := oops.AsOops(err)
	if !ok {
		return nil
	}

	ctxMap := oopsErr.Context()
	if len(ctxMap) == 0 {
		return nil
	}

	fields := make([]zap.Field, 0, len(ctxMap))
	for k, v := range ctxMap {
		if isSensitiveKey(k) {
			fields = append(fields, zap.String(k, "***"))
			continue
		}
		fields = append(fields, zap.Any(k, sanitizeValue(v)))
	}

	slices.SortFunc(fields, func(a, b zap.Field) int {
		return cmp.Compare(a.Key, b.Key)
	})

	return fields
}

// RootCause 提取用于日志记录的底层真实错误。
func RootCause(err error) error {
	if bizErr, ok := As(err); ok {
		if inner := errors.Unwrap(bizErr); inner != nil {
			return inner
		}
	}
	return err
}

// Map 把任意 error 归一化为 HTTP 状态码与响应体。映射顺序即优先级。
func Map(err error) (int, Response) {
	if bizErr, ok := As(err); ok {
		return Status(bizErr.code), Response{
			Code:    string(bizErr.code),
			Message: bizErr.message,
			Errors:  bizErr.fields,
		}
	}

	if _, ok := AsPanic(err); ok {
		return fiber.StatusInternalServerError, Response{
			Code:    string(CodeInternal),
			Message: msgInternal,
		}
	}

	// 请求体结构错误（JSON 语法、类型不匹配、空体）统一按 400 处理。
	if status, resp, ok := mapMalformedBody(err); ok {
		return status, resp
	}

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return mapPgError(pgErr)
	}

	if IsNoRows(err) {
		return fiber.StatusNotFound, Response{Code: string(CodeNotFound), Message: msgNotFound}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return fiber.StatusGatewayTimeout, Response{Code: string(CodeGatewayTimeout), Message: msgGatewayTimeout}
	}

	if errors.Is(err, context.Canceled) {
		return StatusClientClosedRequest, Response{Code: string(CodeClientClosedRequest), Message: msgClientClosedRequest}
	}

	// 识别 Fiber 原生错误
	if fiberErr, ok := errors.AsType[*fiber.Error](err); ok {
		code := CodeForStatus(fiberErr.Code)
		return fiberErr.Code, Response{
			Code:    string(code),
			Message: cmp.Or(fiberErr.Message, MessageForStatus(fiberErr.Code)),
		}
	}

	// 识别已在 statuses 登记过的 oops 错误。
	// 未主动声明对外文案时按状态码兜底，避免 4xx 也回一句"意外错误"。
	if oopsErr, ok := oops.AsOops(err); ok {
		code := Code(fmt.Sprintf("%v", oopsErr.Code()))
		if code == CodeContextCanceled {
			code = CodeClientClosedRequest
		}
		if status, registered := statuses[code]; registered {
			return status, Response{
				Code:    string(code),
				Message: cmp.Or(oopsErr.Public(), MessageForStatus(status)),
			}
		}
	}

	// 兜底：未识别错误统一返回 500。
	return fiber.StatusInternalServerError, Response{Code: string(CodeInternal), Message: msgInternal}
}

// mapMalformedBody 识别请求体解析、JSON 解码、查询参数转换与载荷超限的失败。
func mapMalformedBody(err error) (int, Response, bool) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok || errors.Is(err, fasthttp.ErrBodyTooLarge) {
		return fiber.StatusRequestEntityTooLarge, Response{
			Code:    string(CodeRequestTooLarge),
			Message: msgRequestTooLarge,
		}, true
	}
	if isBadRequestError(err) {
		return fiber.StatusBadRequest, Response{
			Code:    string(CodeBadRequest),
			Message: msgBadRequest,
		}, true
	}

	return 0, Response{}, false
}

func isBadRequestError(err error) bool {
	if _, ok := errors.AsType[*strconv.NumError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*fiber.BindError](err); ok {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// mapPgError 将数据库约束错误映射为业务状态码与文案。
func mapPgError(err *pgconn.PgError) (int, Response) {
	switch err.Code {
	case pgErrUniqueViolation, pgErrForeignKeyViolation, pgErrNotNullViolation:
		return fiber.StatusConflict, Response{Code: string(CodeConflict), Message: msgConflict}
	case pgErrInvalidTextRepresentation:
		return fiber.StatusBadRequest, Response{Code: string(CodeBadRequest), Message: msgBadRequest}
	default:
		return fiber.StatusInternalServerError, Response{Code: string(CodeInternal), Message: msgInternal}
	}
}
