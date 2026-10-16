package authentication

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
)

// ErrPasswordCredentialMissing 表示主体没有可用的本地密码凭据（例如仅通过第三方登录注册的账号）。
// 调用方据此决定对外语义：管理端按"当前口令不正确"处理以免泄漏账号形态，客户端可明确提示缺少密码凭据。
var ErrPasswordCredentialMissing = errors.New("authentication: password credential is missing")

// ErrProviderLacksCredentials 表示守卫的 Provider 未提供本地密码凭据读写能力，属于装配错误。
var ErrProviderLacksCredentials = errors.New("authentication: user provider does not implement CredentialStore")

// CredentialStore 抽象主体本地密码凭据的读写契约（遵循 ISP，与 UserProvider 分离）。
// 凭据不存在时两个方法都必须返回 sql.ErrNoRows。
type CredentialStore interface {
	// FindCredentialPassword 返回本地密码哈希；凭据不存在时返回 sql.ErrNoRows。
	FindCredentialPassword(ctx context.Context, userID string) (*string, error)
	// UpdatePassword 改写本地密码哈希；凭据不存在时返回 sql.ErrNoRows。
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
}

// PasswordHasher 声明口令哈希的比对与生成能力。
type PasswordHasher interface {
	// Check 比对明文口令与已编码哈希是否匹配。
	Check(encoded, password string) (bool, error)
	// Make 生成明文口令的编码哈希。
	Make(password string) (string, error)
}

// CredentialManager 统管一类可认证主体的本地密码凭据生命周期：登录口令核验与改密。
//
// 凭据读取、口令比对、哈希写入与令牌作废全部内聚于此，各入口只表达意图，
// 不再各自拼装读取-比对-写入链路；凭据仓储沿用 Guard 的 Provider（可选扩展接口，
// 与 LoginRecorder 同构），因此不需要额外的装配参数。
type CredentialManager[T Authenticatable] struct {
	guard       *Guard[T]
	credentials CredentialStore
	hasher      PasswordHasher
}

// NewCredentialManager 从守卫派生主体密码凭据模块。
// 守卫的 Provider 若未实现 CredentialStore，口令核验与改密会返回装配错误。
func NewCredentialManager[T Authenticatable](guard *Guard[T], hasher PasswordHasher) *CredentialManager[T] {
	var credentials CredentialStore
	if guard != nil && guard.Provider() != nil {
		credentials, _ = any(guard.Provider()).(CredentialStore)
	}
	return &CredentialManager[T]{
		guard:       guard,
		credentials: credentials,
		hasher:      hasher,
	}
}

// VerifyPassword 核验主体的本地密码。
// 凭据缺失与口令不匹配统一返回同一个 401 口径，杜绝通过错误差异枚举账号。
func (m *CredentialManager[T]) VerifyPassword(ctx context.Context, userID, password string) error {
	encoded, err := m.findCredential(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrPasswordCredentialMissing) {
			return errs.InvalidCredentials()
		}
		return err
	}

	matched, err := m.checkPassword(encoded, password)
	if err != nil {
		return err
	}
	if !matched {
		return errs.InvalidCredentials()
	}
	return nil
}

// ChangePassword 核验当前口令后写入新口令，并作废该主体在此之前签发的全部令牌。
// 凭据缺失时返回 ErrPasswordCredentialMissing，由调用方决定对外语义。
func (m *CredentialManager[T]) ChangePassword(ctx context.Context, userID, current, next string) error {
	encoded, err := m.findCredential(ctx, userID)
	if err != nil {
		return err
	}

	matched, err := m.checkPassword(encoded, current)
	if err != nil {
		return err
	}
	if !matched {
		return errs.PasswordMismatch()
	}

	hash, err := m.hasher.Make(next)
	if err != nil {
		return errs.Internal(err)
	}
	if err := m.updateCredential(ctx, userID, hash); err != nil {
		return err
	}

	// 密码变更成功后作废该主体在此之前签发的所有会话令牌。
	// 沿用既有取舍：令牌作废是尽力而为，失败不影响已完成的改密结果——这是 ADR-0001 记录在案的取舍。
	// 但失败必须留痕：它与"改密后旧令牌立即失效"的安全预期相悖，属于需要被观测到的异常。
	if m.guard != nil {
		if err := m.guard.InvalidateAll(ctx, userID); err != nil {
			logger.FromContext(ctx).Warn("failed to invalidate sessions after password change", zap.Error(err))
		}
	}
	return nil
}

// checkPassword 比对明文口令与已存哈希。
// 只负责比对本身：哈希计算失败属服务端故障，归一为 500 语义；不匹配由调用方决定对外口径
// （登录用统一凭据错误防枚举，改密明确指出当前口令不正确），因此这里不合并两者的语义。
func (m *CredentialManager[T]) checkPassword(encoded *string, password string) (bool, error) {
	if encoded == nil {
		return false, nil
	}
	matched, err := m.hasher.Check(*encoded, password)
	if err != nil {
		return false, errs.Internal(err)
	}
	return matched, nil
}

// findCredential 读取本地密码哈希，把"凭据不存在"的两种驱动口径归一为 ErrPasswordCredentialMissing。
func (m *CredentialManager[T]) findCredential(ctx context.Context, userID string) (*string, error) {
	if m.credentials == nil {
		return nil, errs.Internal(ErrProviderLacksCredentials)
	}

	encoded, err := m.credentials.FindCredentialPassword(ctx, userID)
	switch {
	case errs.IsNoRows(err), err == nil && (encoded == nil || *encoded == ""):
		return nil, ErrPasswordCredentialMissing
	case err != nil:
		return nil, errs.Internal(err)
	default:
		return encoded, nil
	}
}

// updateCredential 写入新的密码哈希，把"凭据不存在"归一为 ErrPasswordCredentialMissing。
func (m *CredentialManager[T]) updateCredential(ctx context.Context, userID, hash string) error {
	if m.credentials == nil {
		return errs.Internal(ErrProviderLacksCredentials)
	}

	if err := m.credentials.UpdatePassword(ctx, userID, hash); err != nil {
		if errs.IsNoRows(err) {
			return ErrPasswordCredentialMissing
		}
		return errs.Internal(err)
	}
	return nil
}
