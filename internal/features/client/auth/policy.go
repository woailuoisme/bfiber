package auth

import (
	"context"

	"bfiber/internal/config"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
)

// AccessPolicy 构造普通客户端用户的准入策略。
//
// 基底是 models.ClientPolicy（账号状态：封禁与未激活），再按配置叠加邮箱验证要求。
// 策略不再分裂在装配点与切片两处：装配点只负责注入，策略本身与其所属切片同处。
// 返回值由 Guard 在 Login / Refresh / UserOrFail 内统一执行。
func AccessPolicy(cfg *config.Config) authentication.Policy[*models.User] {
	base := models.ClientPolicy{}
	requireVerified := cfg != nil && cfg.Auth.RequireEmailVerification

	return authentication.PolicyFunc[*models.User](func(ctx context.Context, user *models.User) error {
		if err := base.ValidateUser(ctx, user); err != nil {
			return err
		}
		if requireVerified && !user.EmailVerified {
			return errs.New(errs.CodeAuthEmailNotVerified, "The email address has not been verified")
		}
		return nil
	})
}
