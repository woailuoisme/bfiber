package config

import (
	"strings"
)

// SentryConfig 描述 Sentry / GlitchTip 异常监控与性能追踪配置。
type SentryConfig struct {
	Enabled          *bool   `env:"SENTRY_ENABLED" envDefault:"false"`
	DSN              string  `env:"SENTRY_DSN"`
	GlitchTipDSN     string  `env:"GLITCHTIP_DSN"`
	Environment      string  `env:"SENTRY_ENVIRONMENT"`
	Release          string  `env:"SENTRY_RELEASE"`
	TracesSampleRate float64 `env:"SENTRY_TRACES_SAMPLE_RATE" envDefault:"0.01"`
	Debug            bool    `env:"SENTRY_DEBUG" envDefault:"false"`
}

// EffectiveDSN 获取有效 DSN（优先 SENTRY_DSN，回退 GLITCHTIP_DSN）。
func (s *SentryConfig) EffectiveDSN() string {
	if s == nil {
		return ""
	}
	if dsn := strings.TrimSpace(s.DSN); dsn != "" {
		return dsn
	}
	return strings.TrimSpace(s.GlitchTipDSN)
}

// IsEnabled 判断是否激活 Sentry / GlitchTip 监控。
func (s *SentryConfig) IsEnabled() bool {
	if s == nil || (s.Enabled != nil && !*s.Enabled) {
		return false
	}
	return s.EffectiveDSN() != ""
}

// validateSentry 校验 Sentry 配置项合法性。
func (c *Config) validateSentry() error {
	return nil
}
