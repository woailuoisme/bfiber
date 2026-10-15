package models

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/uptrace/bun"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/hasher"
	"bfiber/internal/shared/enums"
)

var (
	_ authentication.UserProvider[*User] = (*UserProvider)(nil)
	_ authentication.LoginRecorder       = (*UserProvider)(nil)
)

// UserProvider 实现了 authentication.UserProvider[*User] 与 authentication.LoginRecorder，
// 负责作为 Guard 的用户获取与凭据核验适配器。
type UserProvider struct {
	db     *bun.DB
	hasher *hasher.Hasher
}

// NewUserProvider 构造 UserProvider 实例。
func NewUserProvider(db *bun.DB, h *hasher.Hasher) *UserProvider {
	return &UserProvider{db: db, hasher: h}
}

// WithHasher 链式注入或覆盖 Hasher。
func (p *UserProvider) WithHasher(h *hasher.Hasher) *UserProvider {
	p.hasher = h
	return p
}

// DB 返回底层数据库句柄。
func (p *UserProvider) DB() *bun.DB {
	return p.db
}

// Hasher 返回底层密码哈希器。
func (p *UserProvider) Hasher() *hasher.Hasher {
	return p.hasher
}

// FindByID 按主键查找用户主体。
func (p *UserProvider) FindByID(ctx context.Context, id string) (*User, error) {
	return FindUserByID(ctx, p.db, id)
}

// FindByCredentials 根据凭据中的账号标识检索主体（支持邮箱或用户名检索）。
func (p *UserProvider) FindByCredentials(ctx context.Context, creds authentication.Credentials) (*User, error) {
	account := strings.TrimSpace(creds.Account)
	if strings.Contains(account, "@") {
		return FindUserByEmail(ctx, p.db, account)
	}
	user, err := FindUserByUsername(ctx, p.db, account)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return FindUserByEmail(ctx, p.db, account)
}

// ValidateCredentials 校验指定主体是否与凭证匹配（比对密码哈希）。
func (p *UserProvider) ValidateCredentials(ctx context.Context, u *User, creds authentication.Credentials) (bool, error) {
	if u == nil || strings.TrimSpace(creds.Password) == "" {
		return false, nil
	}
	pwdHash, err := FindCredentialPassword(ctx, p.db, u.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if pwdHash == nil || *pwdHash == "" {
		return false, nil
	}
	if p.hasher == nil {
		return false, errors.New("user: provider hasher is not configured")
	}
	return p.hasher.Check(*pwdHash, creds.Password)
}

// RecordLogin 记录用户登录成功的时间与方式。
func (p *UserProvider) RecordLogin(ctx context.Context, id string, method string) error {
	return RecordLogin(ctx, p.db, id, method)
}

// FindCredentialAccount 查找指定用户的本地凭据账户。
func (p *UserProvider) FindCredentialAccount(ctx context.Context, userID string) (*Account, error) {
	return FindCredentialAccount(ctx, p.db, userID)
}

// UpdatePassword 更新指定用户的本地凭据密码哈希。
func (p *UserProvider) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return UpdatePassword(ctx, p.db, userID, passwordHash)
}

// ErrRoleRestricted 声明非目标角色访问受限错误（403 Forbidden）。
var ErrRoleRestricted = errs.Forbidden("Access restricted to administrators")

// ClientPolicy 实现普通客户端用户的守卫校验策略。
type ClientPolicy struct{}

// ValidateUser 校验客户端账号活跃且未被封禁。
func (p ClientPolicy) ValidateUser(_ context.Context, user *User) error {
	return user.GuardState()
}

// AdminPolicy 实现后台管理端管理员的守卫校验策略。
type AdminPolicy struct{}

// ValidateUser 校验管理员账号具备系统管理员角色且处于活跃可用状态。
func (p AdminPolicy) ValidateUser(ctx context.Context, user *User) error {
	if user == nil {
		return errs.Unauthorized(errs.CodeAuthTokenInvalid, "The token is invalid")
	}
	if id, ok := authentication.FromContext(ctx); ok {
		roles := id.AllRoles()
		if len(roles) > 0 && !slices.Contains(roles, enums.RoleAdmin.String()) {
			return ErrRoleRestricted
		}
	}
	if user.Role != enums.RoleAdmin {
		return ErrRoleRestricted
	}
	return user.GuardState()
}

var (
	_ authentication.Policy[*User] = ClientPolicy{}
	_ authentication.Policy[*User] = AdminPolicy{}
)
