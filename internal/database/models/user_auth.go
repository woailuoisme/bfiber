package models

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"

	"bfiber/internal/platform/errs"
)

// uniqueViolation 是 PostgreSQL 的唯一约束冲突错误码。
const uniqueViolation = "23505"

// 唯一索引名，用于把冲突翻译成具体业务错误码。
const (
	indexUserEmail       = "idx_user_email"
	indexUserUsername    = "idx_user_username"
	indexUserPhoneNumber = "idx_user_phone_number"
)

// GetAuthIdentifier 返回认证主体唯一标识符（用户 ID）。
func (u *User) GetAuthIdentifier() string {
	if u == nil {
		return ""
	}
	return u.ID
}

// GetAuthRole 返回认证主体的权限角色。
func (u *User) GetAuthRole() string {
	if u == nil {
		return ""
	}
	return string(u.Role)
}

// HasVerifiedEmail 判断用户邮箱是否已通过验证。
func (u *User) HasVerifiedEmail() bool {
	if u == nil {
		return false
	}
	return u.EmailVerified
}

// GetJWTCustomClaims 返回需要嵌入 JWT 载荷的自定义声明字典。
func (u *User) GetJWTCustomClaims() map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{
		"email_verified": u.EmailVerified,
	}
}

// NormaliseEmail 统一邮箱大小写与首尾空白，与库中 idx_user_email 的口径一致。
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormaliseUsername 统一用户名大小写与首尾空白，与库中 idx_user_username 的口径一致。
func NormaliseUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// CheckUsernameAvailable 校验用户名是否已被其他用户占用。
func CheckUsernameAvailable(ctx context.Context, db bun.IDB, userID string, username string) error {
	normalised := NormaliseUsername(username)
	existing, err := FindUserByUsername(ctx, db, normalised)
	switch {
	case err == nil && existing.ID != userID:
		return errs.New(errs.CodeAuthUsernameTaken, "The username has already been taken")
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return errs.Internal(err)
	}
	return nil
}

// MapUserUniqueConflict 把唯一索引冲突翻译成具体的业务错误码。
func MapUserUniqueConflict(err error) error {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != uniqueViolation {
		return errs.Internal(err)
	}

	switch pgErr.ConstraintName {
	case indexUserEmail:
		return errs.New(errs.CodeAuthEmailAlreadyExists, "An account with this email address already exists")
	case indexUserUsername:
		return errs.New(errs.CodeAuthUsernameTaken, "The username has already been taken")
	case indexUserPhoneNumber:
		return errs.New(errs.CodeAuthPhoneNumberTaken, "The phone number has already been taken")
	default:
		return errs.Conflict("The user conflicts with an existing record")
	}
}
