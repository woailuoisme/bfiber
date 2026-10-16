package errs

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

// 面向调用方的固定描述文案。
const (
	msgValidationFailed    = "Validation failed"
	msgInternal            = "An unexpected error occurred"
	msgBadRequest          = "The request could not be understood"
	msgNotFound            = "The requested resource was not found"
	msgMethodNotAllowed    = "The requested HTTP method is not allowed"
	msgForbidden           = "You are not allowed to perform this action"
	msgConflict            = "The request conflicts with the current state of the resource"
	msgRateLimited         = "Too many requests"
	msgRequestTooLarge     = "The request body is too large"
	msgGatewayTimeout      = "The request timed out while waiting for a response"
	msgClientClosedRequest = "Client closed request"
	msgInvalidLogin        = "Invalid email or password"
	msgPasswordMismatch    = "The current password is incorrect"
	msgUnauthorized        = "Authentication is required"
	msgTokenInvalid        = "The token is invalid"
	msgUserNotFound        = "The requested user does not exist"
	msgServiceUnavailable  = "Service is temporarily unavailable"
)

// Error 是携带结构化错误码与可选字段级校验明细的业务错误。
type Error struct {
	code    Code
	message string
	fields  map[string][]string
	cause   error
}

// Error 实现 error 接口，返回面向调用方的文案。
// 空接收者返回空串：值为 nil 的 *Error 一旦被装箱进 error 接口，任何日志打印都会落到这里。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

// Unwrap 暴露底层错误，使 errors.Is/As 可以继续向下匹配。
// 空接收者返回 nil：errors.Is/As 会遍历错误链上的每个节点，此处不设防会让整条链的遍历 panic。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Code 返回错误码。
func (e *Error) Code() Code { return e.code }

// Message 返回面向调用方的英文描述。
func (e *Error) Message() string { return e.message }

// Status 返回错误对应的 HTTP 状态码。
func (e *Error) Status() int {
	return Status(e.code)
}

// FiberError 生成 Fiber 原生 *fiber.Error 实例，便于直接在 Fiber Handler 中作为错误返回。
func (e *Error) FiberError() *fiber.Error {
	return fiber.NewError(e.Status(), e.message)
}

// Fields 返回按字段路径归集的校验明细，无明细时为 nil。
func (e *Error) Fields() map[string][]string { return e.fields }

// WithField 追加一条字段级明细并返回自身，供构造时链式调用。
func (e *Error) WithField(field, message string) *Error {
	if e.fields == nil {
		e.fields = make(map[string][]string)
	}
	e.fields[field] = append(e.fields[field], message)
	return e
}

// New 按错误码构造业务错误，并用 oops 捕获堆栈与来源域。
func New(code Code, message string) *Error {
	return &Error{
		code:    code,
		message: message,
		cause:   oops.Code(string(code)).In(domain(code)).Public(message).New(message),
	}
}

// Wrap 在既有错误之上附加错误码语义，同时保留原始错误。
func Wrap(code Code, message string, cause error) *Error {
	if cause == nil {
		return New(code, message)
	}
	return &Error{
		code:    code,
		message: message,
		cause:   oops.Code(string(code)).In(domain(code)).Public(message).Wrapf(cause, "%s", message),
	}
}

// BadRequest 构造 400，用于请求体无法解析等结构性错误。
func BadRequest(message string) *Error {
	return New(CodeBadRequest, cmp.Or(message, msgBadRequest))
}

// NotFound 构造 404。
func NotFound(message string) *Error {
	return New(CodeNotFound, cmp.Or(message, msgNotFound))
}

// MethodNotAllowed 构造 405。
func MethodNotAllowed(message string) *Error {
	return New(CodeMethodNotAllowed, cmp.Or(message, msgMethodNotAllowed))
}

// Forbidden 构造 403。
func Forbidden(message string) *Error {
	return New(CodeForbidden, cmp.Or(message, msgForbidden))
}

// Conflict 构造 409。
func Conflict(message string) *Error {
	return New(CodeConflict, cmp.Or(message, msgConflict))
}

// IdempotencyConflict 构造 409，表示同一幂等键正在执行中。
func IdempotencyConflict(message string) *Error {
	return New(CodeIdempotencyConflict, cmp.Or(message, "A request with the same idempotency key is currently being processed"))
}

// IdempotencyMismatch 构造 422，表示相同幂等键被复用于不同的请求载荷。
func IdempotencyMismatch(message string) *Error {
	return New(CodeIdempotencyMismatch, cmp.Or(message, "Idempotency key was previously used with different request parameters"))
}

// IdempotencyRequired 构造 400，表示当前端点强制要求携带幂等键。
func IdempotencyRequired(message string) *Error {
	return New(CodeIdempotencyRequired, cmp.Or(message, "Idempotency-Key header is required for this request"))
}

// RateLimited 构造 429。
func RateLimited(message string) *Error {
	return New(CodeRateLimited, cmp.Or(message, msgRateLimited))
}

// RequestTooLarge 构造 413，用于请求体超过中间件设定的上限。
func RequestTooLarge(cause error) *Error {
	return Wrap(CodeRequestTooLarge, msgRequestTooLarge, cause)
}

// GatewayTimeout 构造 504，用于请求在等待外部依赖或完成处理时超时。
func GatewayTimeout(cause error) *Error {
	return Wrap(CodeGatewayTimeout, msgGatewayTimeout, cause)
}

// ClientClosedRequest 构造 499，用于客户端在响应完成前主动终止连接。
func ClientClosedRequest(cause error) *Error {
	return Wrap(CodeClientClosedRequest, msgClientClosedRequest, cause)
}

// ServiceUnavailable 构造 503，用于外部依赖或本服务暂不可用。
func ServiceUnavailable(message string) *Error {
	return New(CodeServiceUnavailable, cmp.Or(message, msgServiceUnavailable))
}

// InvalidCredentials 是登录失败的唯一对外口径，不区分"邮箱不存在"与"密码错误"。
func InvalidCredentials() *Error { return New(CodeAuthInvalidCredentials, msgInvalidLogin) }

// PasswordMismatch 构造 422，表示需要核验的当前口令不正确。
func PasswordMismatch() *Error { return New(CodeAuthPasswordMismatch, msgPasswordMismatch) }

// Unauthorized 构造 401，用于凭证缺失、格式错误、过期或已撤销。
func Unauthorized(code Code, message string) *Error { return New(code, message) }

// TokenInvalid 构造 401，表示令牌本身有效但指向的主体已不存在。
func TokenInvalid() *Error { return Unauthorized(CodeAuthTokenInvalid, msgTokenInvalid) }

// AuthenticationRequired 构造 401，表示请求缺少可用凭证（未携带、格式错误或无法解析）。
//
// 与 TokenInvalid 共用 AUTH_TOKEN_INVALID 码是刻意的：api.md 用该码定义"未通过认证"这一族 401，
// 客户端据此触发重新登录；为"凭证缺失"另立新码会同时改动契约与既有客户端的判定分支。
// 两者的区别只在文案：缺失讲"需要认证"，失效讲"令牌无效"。
func AuthenticationRequired() *Error {
	return Unauthorized(CodeAuthTokenInvalid, msgUnauthorized)
}

// UserNotFound 构造 404，表示目标用户不存在（对外不区分内部原因）。
func UserNotFound() *Error { return NotFound(msgUserNotFound) }

// Internal 把未预期错误收敛为 500；原始错误只进日志，绝不进响应体。
func Internal(cause error) *Error { return Wrap(CodeInternal, msgInternal, cause) }

// IsNoRows 判断错误链中是否包含"记录不存在"信号（database/sql 与 pgx 两种驱动口径）。
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// From 把数据层错误归一为对外语义错误：无错误返回 nil，无记录返回 noRows，其余一律 500。
// noRows 为 nil 时回落到通用 404——否则"无记录"会变成 nil 错误，让调用方以 200 返回零值对象。
//
// 之所以必须在此处归一，而不是依赖 Render 的映射优先级：Internal 会先把底层错误包成
// *Error，而 Render 对 *Error 的匹配早于 sql.ErrNoRows，未归一就会把"记录不存在"渲染成 500。
func From(cause error, noRows *Error) *Error {
	switch {
	case cause == nil:
		return nil
	case IsNoRows(cause):
		return cmp.Or(noRows, NotFound(""))
	default:
		return Internal(cause)
	}
}

// Validation 构造 422，fields 为按字段路径归集的校验失败文案。
func Validation(fields map[string][]string) *Error {
	e := New(CodeValidationFailed, msgValidationFailed)
	e.fields = fields
	return e
}

// As 从错误链中提取 *Error，是 errors.AsType 的语义化封装。
// 值为 nil 的 *Error 视为未命中：它一旦从返回 error 的函数中透出，就会是"接口非 nil 但内部为空"的陷阱，
// 直接解引用会导致 panic。
func As(err error) (*Error, bool) {
	bizErr, ok := errors.AsType[*Error](err)
	return bizErr, ok && bizErr != nil
}

// Stack 提取错误链中的 oops 堆栈文本，非 oops 错误返回空串。
func Stack(err error) string {
	if oopsErr, ok := oops.AsOops(err); ok {
		return oopsErr.Stacktrace()
	}
	return ""
}

// PanicError 描述捕获的运行时恐慌及其调用堆栈信息。
type PanicError struct {
	Value any
	Stack string
}

// Error 实现 error 接口。
func (p *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", p.Value)
}

// NewPanicError 构造包含恐慌对象与调用堆栈的 PanicError。
func NewPanicError(v any, stack string) *PanicError {
	return &PanicError{Value: v, Stack: stack}
}

// AsPanic 判断并提取错误链中的 PanicError。
func AsPanic(err error) (*PanicError, bool) {
	return errors.AsType[*PanicError](err)
}
