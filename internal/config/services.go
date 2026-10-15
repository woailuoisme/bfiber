package config

// GeoIPConfig 描述基于 ip2region 的离线 IP 归属地库配置（对齐 Laravel config/services.php）。
type GeoIPConfig struct {
	Enabled bool   `env:"GEOIP_ENABLED" envDefault:"false"`
	DBPath  string `env:"GEOIP_DB_PATH" envDefault:"./storage/app/geoip/ip2region_v4.xdb"`
}

// ExcelConfig 描述 Excel/CSV 导出导入管线临时存储与缓冲配置（对齐 Laravel config/services.php）。
type ExcelConfig struct {
	TempPath string `env:"EXCEL_TEMP_PATH" envDefault:".cache/excel"`
}

// PDFConfig 描述 PDF 生成服务配置（对齐 Laravel config/services.php）。
type PDFConfig struct {
	FontPath   string  `env:"PDF_FONT_PATH" envDefault:"./storage/fonts/SourceHanSans.ttf"`
	FontFamily string  `env:"PDF_FONT_FAMILY" envDefault:"SourceHanSans"`
	PageSize   string  `env:"PDF_PAGE_SIZE" envDefault:"A4"`
	Margin     float64 `env:"PDF_MARGIN" envDefault:"10.0"`
}

// validateServices 检查第三方集成与扩展服务配置。
func (c *Config) validateServices() error {
	return nil
}
