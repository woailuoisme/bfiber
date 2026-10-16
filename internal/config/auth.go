package config

import (
	"errors"
	"time"
)

// AuthConfig 描述认证策略开关与一次性凭据寿命（对齐 Laravel config/auth.php）。
type AuthConfig struct {
	RequireEmailVerification bool          `env:"REQUIRE_EMAIL_VERIFICATION" envDefault:"false"`
	OTPTTL                   time.Duration `env:"OTP_TTL" envDefault:"10m"`
	OTPMaxAttempts           int           `env:"OTP_MAX_ATTEMPTS" envDefault:"5"`
	// OTPSendCooldown 同一邮箱两次签发一次性凭据之间的最小间隔；<=0 表示关闭冷却。
	OTPSendCooldown time.Duration `env:"OTP_SEND_COOLDOWN" envDefault:"60s"`
	// OTPSendMaxPerWindow 同一邮箱在一个发送窗口内允许的签发次数上限；<=0 表示关闭窗口限流。
	OTPSendMaxPerWindow int           `env:"OTP_SEND_MAX_PER_WINDOW" envDefault:"5"`
	PasswordResetTTL    time.Duration `env:"PASSWORD_RESET_TTL" envDefault:"30m"`
}

// validateAuth 检查认证相关寿命参数与限制次数合法性。
func (c *Config) validateAuth() error {
	a := c.Auth
	if a.OTPTTL <= 0 {
		return errors.New("config: OTP_TTL must be positive")
	}
	if a.OTPMaxAttempts <= 0 {
		return errors.New("config: OTP_MAX_ATTEMPTS must be positive")
	}
	if a.PasswordResetTTL <= 0 {
		return errors.New("config: PASSWORD_RESET_TTL must be positive")
	}
	return nil
}
