package adminauth

import (
	"context"
	"errors"

	"bfiber/internal/database/models"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/hasher"
	"bfiber/internal/platform/validator"
)

// Deps 声明管理员认证服务的外部依赖。
// 守卫（含其 Provider）由装配点单点构造并注入，服务内不再自建，故不需要数据库句柄与 TokenIssuer。
type Deps struct {
	Guard  *authentication.Guard[*models.User]
	Hasher *hasher.Hasher
}

// Service 实现后台管理员认证的业务逻辑。
type Service struct {
	guard       *authentication.Guard[*models.User]
	credentials *authentication.CredentialManager[*models.User]
}

// NewService 构造 Service。
// 守卫由其 Provider 与装配点单点构造后注入；服务内不再自建第二份守卫。
// 凭据读写能力由 Provider 兼任，不再另开装配参数。
func NewService(deps Deps) *Service {
	return &Service{
		guard:       deps.Guard,
		credentials: authentication.NewCredentialManager(deps.Guard, deps.Hasher),
	}
}

// Guard 返回底层 Guard 实例。
func (s *Service) Guard() *authentication.Guard[*models.User] { return s.guard }

// SignInEmail 管理员邮箱密码登录。
func (s *Service) SignInEmail(ctx context.Context, body SignInEmailBody) (SessionResource, error) {
	user, sess, err := s.guard.Attempt(ctx, authentication.Credentials{
		Account:  body.Email,
		Password: body.Password,
	})
	if err != nil {
		if errors.Is(err, models.ErrRoleRestricted) {
			return SessionResource{}, errs.InvalidCredentials()
		}
		return SessionResource{}, err
	}

	return toSessionResource(sess, user), nil
}

// GetSession 获取当前有效会话的管理员资料。
func (s *Service) GetSession(ctx context.Context, userID string) (GetSessionResource, error) {
	user, err := s.guard.UserOrFail(ctx)
	if err != nil {
		var loadErr error
		user, loadErr = s.loadAdmin(ctx, userID)
		if loadErr != nil {
			return GetSessionResource{}, loadErr
		}
	}

	return GetSessionResource{User: toAdminUser(user)}, nil
}

// loadAdmin 加载管理员实体并执行管理员守卫策略（角色准入 + 账号状态）。
func (s *Service) loadAdmin(ctx context.Context, userID string) (*models.User, error) {
	if s.guard == nil || s.guard.Provider() == nil {
		return nil, errs.Internal(errors.New("adminauth: guard provider is not configured"))
	}

	u, err := s.guard.Provider().FindByID(ctx, userID)
	if err != nil {
		return nil, errs.From(err, errs.TokenInvalid())
	}
	if err := s.guard.Policy().ValidateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Refresh 刷新管理员双令牌，实现令牌轮换。
func (s *Service) Refresh(ctx context.Context, body RefreshTokenBody) (SessionResource, error) {
	user, sess, err := s.guard.Refresh(ctx, body.RefreshToken)
	if err != nil {
		return SessionResource{}, err
	}

	return toSessionResource(sess, user), nil
}

// SignOut 登出并作废当前管理员访问令牌与可选的刷新令牌。
func (s *Service) SignOut(ctx context.Context, body SignOutBody) error {
	if body.RefreshToken != nil {
		return s.guard.Logout(ctx, *body.RefreshToken)
	}
	return s.guard.Logout(ctx)
}

func toSessionResource(sess authentication.Session, user *models.User) SessionResource {
	return SessionResource{
		Token:        sess.Token,
		RefreshToken: sess.RefreshToken,
		ExpiresIn:    sess.ExpiresIn,
		User:         toAdminUser(user),
	}
}

// ChangePassword 修改当前管理员登录密码。
func (s *Service) ChangePassword(ctx context.Context, userID string, body ChangePasswordBody) error {
	if err := validator.PasswordStrength("newPassword", body.NewPassword); err != nil {
		return err
	}

	if _, err := s.loadAdmin(ctx, userID); err != nil {
		return err
	}

	if err := s.credentials.ChangePassword(ctx, userID, body.CurrentPassword, body.NewPassword); err != nil {
		// 本地凭据缺失统一按"当前口令不正确"处理，避免泄漏账号形态。
		if errors.Is(err, authentication.ErrPasswordCredentialMissing) {
			return errs.PasswordMismatch()
		}
		return err
	}
	return nil
}
