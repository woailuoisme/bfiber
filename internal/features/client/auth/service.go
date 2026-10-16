// Package auth 实现注册、登录、登出、令牌续签与密码重置等认证用例。
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/database"
	"bfiber/internal/database/models"
	platformauth "bfiber/internal/platform/authentication"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/eventbus"
	"bfiber/internal/platform/hasher"
	"bfiber/internal/platform/i18n"
	"bfiber/internal/platform/logger"
	"bfiber/internal/platform/mailer"
	"bfiber/internal/platform/notification"
	"bfiber/internal/platform/notification/contract"
	"bfiber/internal/platform/validator"
	"bfiber/internal/shared/events"
)

// NotificationSender 定义外发通知的高层抽象契约。
type NotificationSender interface {
	Send(ctx context.Context, notifiables any, notif contract.Notification, opts ...notification.SendOption) error
}

// Deps 是认证服务所需的依赖，由 main 装配后注入。
// 守卫由装配点单点构造（含准入策略），服务内不再自建，故不需要 TokenIssuer。
type Deps struct {
	DB         bun.IDB
	Guard      *platformauth.Guard[*models.User]
	Hasher     *hasher.Hasher
	Cache      cache.Store
	Mailer     mailer.Mailer
	Notifier   NotificationSender
	Translator *i18n.Translator
	Config     *config.Config
	Publisher  eventbus.Publisher
}

// Service 实现认证用例。
type Service struct {
	db          bun.IDB
	guard       *platformauth.Guard[*models.User]
	credentials *platformauth.CredentialManager[*models.User]
	hasher      *hasher.Hasher
	mailer      mailer.Mailer
	notifier    NotificationSender
	translator  *i18n.Translator
	cfg         *config.Config
	verifier    *verifier
	publisher   eventbus.Publisher
}

// NewService 构造认证服务。
// 守卫由装配点单点构造并注入（准入策略见 policy.go），服务内不再自建第二份守卫。
func NewService(deps Deps) *Service {
	return &Service{
		db:          deps.DB,
		guard:       deps.Guard,
		credentials: platformauth.NewCredentialManager(deps.Guard, deps.Hasher),
		hasher:      deps.Hasher,
		mailer:      deps.Mailer,
		notifier:    deps.Notifier,
		translator:  deps.Translator,
		cfg:         deps.Config,
		verifier:    newVerifier(deps.DB, deps.Cache, deps.Config),
		publisher:   deps.Publisher,
	}
}

// Guard 返回底层 Guard 实例。
func (s *Service) Guard() *platformauth.Guard[*models.User] { return s.guard }

// SignUpEmail 注册邮箱密码账号，并按 Q15 的约定直接完成登录。
func (s *Service) SignUpEmail(ctx context.Context, req SignUpEmailBody) (SessionResource, error) {
	if err := validator.PasswordStrength("password", req.Password); err != nil {
		return SessionResource{}, err
	}

	user := &models.User{
		Name:   strings.TrimSpace(req.Name),
		Email:  models.NormaliseEmail(req.Email),
		Image:  req.Image,
		Role:   models.RoleUser,
		Status: models.StatusActive,
	}
	assignUsername(user, req.Username)

	hash, err := s.hasher.Make(req.Password)
	if err != nil {
		return SessionResource{}, errs.Internal(err)
	}

	evt := &events.UserRegistered{
		UserID:       user.ID,
		Email:        user.Email,
		Name:         user.Name,
		RegisteredAt: time.Now(),
	}
	if err := database.RunInTx(ctx, s.db, func(txCtx context.Context) error {
		if err := models.CreateUserWithCredential(txCtx, s.db, user, hash); err != nil {
			return models.MapUserUniqueConflict(err)
		}
		return nil
	}); err != nil {
		return SessionResource{}, err
	}
	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, evt)
	}

	session, err := s.issueSession(ctx, user)
	if err != nil {
		return SessionResource{}, err
	}
	s.recordLogin(ctx, user.ID, models.ProviderCredential)

	return session, nil
}

// SignInEmail 邮箱密码登录。
func (s *Service) SignInEmail(ctx context.Context, req SignInEmailBody) (SessionResource, error) {
	user, err := models.FindUserByEmail(ctx, s.db, req.Email)
	if err != nil {
		return SessionResource{}, s.translateLookupFailure(ctx, err, req.Email)
	}
	return s.signInWithCredentials(ctx, user, req.Password)
}

// SignInUsername 用户名密码登录。
func (s *Service) SignInUsername(ctx context.Context, req SignInUsernameBody) (SessionResource, error) {
	user, err := models.FindUserByUsername(ctx, s.db, req.Username)
	if err != nil {
		return SessionResource{}, s.translateLookupFailure(ctx, err, req.Username)
	}
	return s.signInWithCredentials(ctx, user, req.Password)
}

// SignOut 作废当前访问令牌；同时传入刷新令牌时一并作废，否则续签通道仍然敞开。
func (s *Service) SignOut(ctx context.Context, req SignOutBody) error {
	if req.RefreshToken != nil {
		return s.guard.Logout(ctx, *req.RefreshToken)
	}
	return s.guard.Logout(ctx)
}

// GetSession 返回当前用户。纯只读、不续签，令牌过期即 401（Q13）。
func (s *Service) GetSession(ctx context.Context, userID string) (GetSessionResource, error) {
	user, err := s.guard.UserOrFail(ctx)
	if err != nil {
		var loadErr error
		user, loadErr = s.loadUser(ctx, userID)
		if loadErr != nil {
			return GetSessionResource{}, loadErr
		}
	}
	return GetSessionResource{User: projectUser(user)}, nil
}

// loadUser 兜底从数据库加载用户实体并按守卫策略校验。
// 必须走策略而非直接返回实体：guard.UserOrFail 失败后由本函数接管，若跳过策略，
// 该回退路径就会绕过准入判定（封禁、未激活与未验证邮箱）。
func (s *Service) loadUser(ctx context.Context, userID string) (*models.User, error) {
	u, err := models.FindUserByID(ctx, s.db, userID)
	if err != nil {
		return nil, errs.From(err, errs.TokenInvalid())
	}
	if err := s.guard.Policy().ValidateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Refresh 用刷新令牌换取新的一对令牌，旧的刷新令牌立即失效（轮换）。
func (s *Service) Refresh(ctx context.Context, req RefreshTokenBody) (SessionResource, error) {
	// guard.Refresh 内部已执行准入策略，无需在此重复校验账号状态。
	user, sess, err := s.guard.Refresh(ctx, req.RefreshToken)
	if err != nil {
		return SessionResource{}, err
	}

	return toSessionResource(sess, user), nil
}

// ChangePassword 校验当前口令后写入新口令。
func (s *Service) ChangePassword(ctx context.Context, userID string, req ChangePasswordBody) error {
	if err := validator.PasswordStrength("newPassword", req.NewPassword); err != nil {
		return err
	}

	if err := s.credentials.ChangePassword(ctx, userID, req.CurrentPassword, req.NewPassword); err != nil {
		// 纯第三方登录账号没有本地密码凭据，明确提示而不是伪装成口令错误。
		if errors.Is(err, platformauth.ErrPasswordCredentialMissing) {
			return noCredentialError()
		}
		return err
	}
	return nil
}

// ForgetPassword 发送携带一次性令牌的重置链接。
func (s *Service) ForgetPassword(ctx context.Context, language string, req ForgetPasswordBody) (StatusResource, error) {
	return s.sendResetCredential(ctx, language, purposeResetLink, req.Email)
}

// RequestPasswordReset 发送 6 位重置验证码。
func (s *Service) RequestPasswordReset(ctx context.Context, language string, req RequestPasswordResetBody) (StatusResource, error) {
	return s.sendResetCredential(ctx, language, purposeResetOTP, req.Email)
}

// ResetPassword 处理链接令牌与验证码两条互斥的重置路径。
func (s *Service) ResetPassword(ctx context.Context, req ResetPasswordBody) (StatusResource, error) {
	if err := validator.PasswordStrength("newPassword", req.NewPassword); err != nil {
		return StatusResource{}, err
	}

	user, err := s.resolveResetTarget(ctx, req)
	if err != nil {
		return StatusResource{}, err
	}

	hash, err := s.hasher.Make(req.NewPassword)
	if err != nil {
		return StatusResource{}, errs.Internal(err)
	}

	evt := &events.PasswordChanged{
		UserID:    user.ID,
		ChangedAt: time.Now(),
	}
	if err := database.RunInTx(ctx, s.db, func(txCtx context.Context) error {
		if err := models.UpdatePassword(txCtx, s.db, user.ID, hash); err != nil {
			return errs.From(err, noCredentialError())
		}
		return nil
	}); err != nil {
		return StatusResource{}, err
	}
	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, evt)
	}

	// 重置密码成功后作废该用户在此之前签发的所有会话令牌
	_ = s.guard.InvalidateAll(ctx, user.ID)

	return StatusResource{Status: true}, nil
}

// SendVerificationOTP 发送邮箱验证码。
func (s *Service) SendVerificationOTP(ctx context.Context, language string, req SendVerificationOTPBody) (StatusResource, error) {
	user, err := models.FindUserByEmail(ctx, s.db, req.Email)
	if err != nil {
		if errs.IsNoRows(err) {
			// 静默成功：不向未认证调用方暴露邮箱是否已注册。
			logger.FromContext(ctx).Info("verification requested for non-existent email, ignored")
			return StatusResource{Status: true}, nil
		}
		return StatusResource{}, errs.Internal(err)
	}
	if user.EmailVerified {
		return StatusResource{Status: true}, nil
	}

	ttl := s.cfg.Auth.OTPTTL
	// 签发失败可能是领域拒绝（如发码冷却/窗口超限的 429），必须原样透出；
	// 一律包成 500 会把已声明的错误码整体掩盖掉。verifier 内部的故障本身已是 500 语义。
	code, err := s.verifier.issueCode(ctx, purposeEmailVerify, user.Email, ttl)
	if err != nil {
		return StatusResource{}, err
	}
	notif := NewSignupVerificationNotification(user.Name, code)
	notif.ExpiresIn = ttl
	if err := s.notifyUser(ctx, language, user, notif, mailKeyVerifyEmail, code, ttl); err != nil {
		return StatusResource{}, errs.Internal(err)
	}
	return StatusResource{Status: true}, nil
}

// VerifyEmail 核销验证码并置位邮箱验证状态。
func (s *Service) VerifyEmail(ctx context.Context, req VerifyEmailBody) (StatusResource, error) {
	email := models.NormaliseEmail(req.Email)
	if err := s.verifier.consume(ctx, purposeEmailVerify, email, strings.TrimSpace(req.OTP)); err != nil {
		return StatusResource{}, err
	}

	user, err := models.FindUserByEmail(ctx, s.db, email)
	if err != nil {
		return StatusResource{}, errs.From(err, errNoAccountForEmail)
	}
	if err := user.MarkEmailVerified(ctx, s.db); err != nil {
		return StatusResource{}, errs.Internal(err)
	}
	return StatusResource{Status: true}, nil
}

// 邮件模板键，与 lang/<语言>/mail.toml 中的键前缀一一对应。
const (
	mailKeyVerifyEmail = "verify_email"
	mailKeyResetLink   = "reset_link"
	mailKeyResetOTP    = "reset_otp"
)

// signInWithCredentials 校验本地密码凭据并签发令牌。
func (s *Service) signInWithCredentials(ctx context.Context, user *models.User, password string) (SessionResource, error) {
	if err := s.credentials.VerifyPassword(ctx, user.ID, password); err != nil {
		return SessionResource{}, err
	}

	session, err := s.issueSession(ctx, user)
	if err != nil {
		return SessionResource{}, err
	}
	s.recordLogin(ctx, user.ID, models.ProviderCredential)
	return session, nil
}

// issueSession 签发双令牌并组装响应体，不写库。
// guard.Login 内部已执行准入策略，账号状态不再由本函数判定。
func (s *Service) issueSession(ctx context.Context, user *models.User) (SessionResource, error) {
	sess, err := s.guard.Login(ctx, user)
	if err != nil {
		return SessionResource{}, err
	}

	return toSessionResource(sess, user), nil
}

func toSessionResource(sess platformauth.Session, user *models.User) SessionResource {
	return SessionResource{
		Token:        sess.Token,
		RefreshToken: sess.RefreshToken,
		ExpiresIn:    sess.ExpiresIn,
		User:         projectUser(user),
	}
}

// guardAccountState 已并入守卫准入策略（见 policy.go）：
// 账号状态与邮箱验证由 Guard 在 Login/Refresh/UserOrFail 内统一执行，此处不再重复判定。

// translateLookupFailure 把"账号不存在"统一翻译为凭证错误，避免泄漏账号是否存在。
func (s *Service) translateLookupFailure(ctx context.Context, err error, identifier string) error {
	if errs.IsNoRows(err) {
		s.equaliseLookupTiming(identifier)
		logger.FromContext(ctx).Info("login failed: account not found", zap.String("identifier", identifier))
		return errs.InvalidCredentials()
	}
	return errs.Internal(err)
}

// dummyPasswordHash 是时序对齐用的固定哈希载荷，进程内只生成一次。
var dummyPasswordHash = sync.OnceValue(func() string {
	encoded, err := hasher.New().Make("bfiber-login-timing-equaliser")
	if err != nil {
		return ""
	}
	return encoded
})

// equaliseLookupTiming 在账号不存在时补做一次等价的口令校验。
// "密码错误"路径会执行 Argon2 校验而本路径不会，响应时间差足以枚举账号是否存在。
func (s *Service) equaliseLookupTiming(identifier string) {
	if s.hasher == nil {
		return
	}
	if encoded := dummyPasswordHash(); encoded != "" {
		_, _ = s.hasher.Check(encoded, identifier)
	}
}

// recordLogin 记录最近登录信息；失败不影响登录本身。
func (s *Service) recordLogin(ctx context.Context, userID, method string) {
	if err := models.RecordLogin(ctx, s.db, userID, method); err != nil {
		logger.FromContext(ctx).Warn("failed to record last login", zap.Error(err), zap.String("user_id", userID))
	}
}

// sendResetCredential 签发重置凭据并投递邮件。邮箱不存在时静默返回成功。
func (s *Service) sendResetCredential(ctx context.Context, language, purpose, email string) (StatusResource, error) {
	user, err := models.FindUserByEmail(ctx, s.db, email)
	if err != nil {
		if errs.IsNoRows(err) {
			logger.FromContext(ctx).Info("password reset requested for non-existent email, ignored")
			return StatusResource{Status: true}, nil
		}
		return StatusResource{}, errs.Internal(err)
	}

	// 同上：重置凭据的签发同样可能被发码限流拒绝，错误语义不得被 Internal 覆盖。
	code, mailKey, ttl, err := s.issueResetCredential(ctx, purpose, user.Email)
	if err != nil {
		return StatusResource{}, err
	}
	notif := NewPasswordResetNotification(user.Name, code)
	notif.ExpiresIn = ttl
	if err := s.notifyUser(ctx, language, user, notif, mailKey, code, ttl); err != nil {
		return StatusResource{}, errs.Internal(err)
	}
	return StatusResource{Status: true}, nil
}

// issueResetCredential 按用途签发重置凭据，返回明文、邮件模板键与寿命。
func (s *Service) issueResetCredential(ctx context.Context, purpose, email string) (code, mailKey string, ttl time.Duration, err error) {
	switch purpose {
	case purposeResetLink:
		ttl = s.cfg.Auth.PasswordResetTTL
		code, err = s.verifier.issueToken(ctx, purpose, email, ttl)
		mailKey = mailKeyResetLink
	case purposeResetOTP:
		ttl = s.cfg.Auth.OTPTTL
		code, err = s.verifier.issueCode(ctx, purpose, email, ttl)
		mailKey = mailKeyResetOTP
	default:
		err = errs.Internal(fmt.Errorf("unknown reset purpose %q", purpose))
	}
	return code, mailKey, ttl, err
}

// resolveResetTarget 核销凭据并定位待重置的账号。
func (s *Service) resolveResetTarget(ctx context.Context, req ResetPasswordBody) (*models.User, error) {
	email := models.NormaliseEmail(req.Email)

	purpose, credential := purposeResetOTP, ""
	switch {
	case req.Token != nil && strings.TrimSpace(*req.Token) != "":
		purpose, credential = purposeResetLink, strings.TrimSpace(*req.Token)
	case req.OTP != nil && strings.TrimSpace(*req.OTP) != "":
		credential = strings.TrimSpace(*req.OTP)
	default:
		return nil, errs.Validation(map[string][]string{
			"token": {"Either token or OTP code is required"},
		})
	}

	if err := s.verifier.consume(ctx, purpose, email, credential); err != nil {
		return nil, err
	}

	user, err := models.FindUserByEmail(ctx, s.db, email)
	if err != nil {
		return nil, errs.From(err, errNoAccountForEmail)
	}
	return user, nil
}

// sendMail 渲染并投递邮件；重置链接由前端站点根地址拼接而成。
func (s *Service) sendMail(ctx context.Context, language string, user *models.User, mailKey, code string, ttl time.Duration) error {
	lang := s.translator.Language(language)
	vars := map[string]string{
		"Name": user.Name,
		"Code": code,
		"TTL":  strconv.Itoa(int(ttl.Minutes())),
		"Link": s.resetLink(user.Email, code),
	}

	subject, body := s.translator.Mail(lang, mailKey, vars)
	return s.mailer.Send(ctx, mailer.Message{To: user.Email, Subject: subject, Body: body})
}

// resetLink 拼接前端重置页面完整地址。
func (s *Service) resetLink(email, token string) string {
	query := url.Values{"token": {token}, "email": {email}}
	return strings.TrimRight(s.cfg.App.URL, "/") + "/reset-password?" + query.Encode()
}

// assignUsername 写入规范化后的用户名。
func assignUsername(user *models.User, username *string) {
	if username == nil {
		return
	}
	normalised := models.NormaliseUsername(*username)
	user.Username = &normalised
	user.DisplayUsername = &normalised
}

// errNoAccountForEmail 用于凭据核销通过但邮箱无对应账号的场景。
var errNoAccountForEmail = errs.NotFound("No account is associated with this email address")

// noCredentialError 用于本地密码凭据缺失的场景。
func noCredentialError() *errs.Error {
	return errs.NotFound("No password credential is associated with this account")
}

func (s *Service) notifyUser(ctx context.Context, language string, user *models.User, notif contract.Notification, mailKey, code string, ttl time.Duration) error {
	if s.notifier != nil {
		return s.notifier.Send(ctx, user, notif)
	}
	return s.sendMail(ctx, language, user, mailKey, code, ttl)
}
