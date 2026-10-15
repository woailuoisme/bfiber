// Package config 负责从环境变量装载并校验应用程序的全局配置。
package config

// Config 汇总应用的所有配置模块。
type Config struct {
	App          AppConfig
	Database     DatabaseConfig
	Cache        CacheConfig
	Storage      StorageConfig
	JWT          JWTConfig
	Mail         MailConfig
	I18n         I18nConfig
	Log          LogConfig
	Auth         AuthConfig
	Limiter      LimiterConfig
	CORS         CORSConfig
	Seed         SeedConfig
	Queue        QueueConfig
	Search       SearchConfig
	Realtime     RealtimeConfig
	GeoIP        GeoIPConfig
	Excel        ExcelConfig
	WeChat       WeChatConfig
	Payment      PaymentConfig
	Notification NotificationConfig
	Media        MediaConfig
	PDF          PDFConfig
	EventBus     EventBusConfig
	Sentry       SentryConfig
	Telemetry    TelemetryConfig
	Temporal     TemporalConfig
	InfluxDB     InfluxDBConfig
}

// Validate 依次执行各模块的语义与有效性校验。
func (c *Config) Validate() error {
	validators := []func() error{
		c.validateApp,
		c.validateServer,
		c.validateDatabase,
		c.validateCache,
		c.validateStorage,
		c.validateJWT,
		c.validateAuth,
		c.validateMail,
		c.validateLog,
		c.validateQueue,
		c.validateSearch,
		c.validateRealtime,
		c.validateServices,
		c.validateCORS,
		c.validateLimiter,
		c.validateI18n,
		c.validateWeChat,
		c.validatePayment,
		c.validateNotification,
		c.validateMedia,
		c.validateEventBus,
		c.validateSentry,
		c.validateTelemetry,
		c.validateTemporal,
		c.validateInfluxDB,
	}
	for _, fn := range validators {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}
