// Package influxdb 提供基于 InfluxDB 3 (Apache Arrow Flight) 的时序数据库高并发客户端抽象。
package influxdb

import (
	"errors"
	"strings"
	"time"

	"bfiber/internal/config"
)

var (
	// ErrInvalidConfig 表示传入的 InfluxDB 配置无效或缺少必填项。
	ErrInvalidConfig = errors.New("influxdb: invalid configuration")

	// ErrClientClosed 表示客户端已关闭。
	ErrClientClosed = errors.New("influxdb: client is closed")

	// ErrClientDisabled 表示 InfluxDB 组件未启用。
	ErrClientDisabled = errors.New("influxdb: client is disabled")
)

// Config 定义 InfluxDB 3 客户端的运行参数。
type Config struct {
	Enabled               bool
	Host                  string
	Token                 string
	Database              string
	Organization          string
	WriteTimeout          time.Duration
	QueryTimeout          time.Duration
	IdleConnectionTimeout time.Duration
	MaxIdleConnections    int
	BatchSize             int
	FlushInterval         time.Duration
	BatchMaxBuffer        int
}

// FromAppConfig 将全局配置转换为 InfluxDB 平台层专用配置。
func FromAppConfig(cfg config.InfluxDBConfig) Config {
	return Config{
		Enabled:               cfg.Enabled,
		Host:                  cfg.Host,
		Token:                 cfg.Token,
		Database:              cfg.Database,
		Organization:          cfg.Organization,
		WriteTimeout:          cfg.WriteTimeout,
		QueryTimeout:          cfg.QueryTimeout,
		IdleConnectionTimeout: cfg.IdleConnectionTimeout,
		MaxIdleConnections:    cfg.MaxIdleConnections,
		BatchSize:             cfg.BatchSize,
		FlushInterval:         cfg.FlushInterval,
		BatchMaxBuffer:        cfg.BatchMaxBuffer,
	}
}

// Validate 校验配置合法性并补齐默认缺省值。
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("influxdb: host cannot be empty")
	}
	if strings.TrimSpace(c.Database) == "" {
		return errors.New("influxdb: database cannot be empty")
	}

	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = 30 * time.Second
	}
	if c.IdleConnectionTimeout <= 0 {
		c.IdleConnectionTimeout = 90 * time.Second
	}
	if c.MaxIdleConnections <= 0 {
		c.MaxIdleConnections = 100
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 1000
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 1 * time.Second
	}
	if c.BatchMaxBuffer <= 0 {
		c.BatchMaxBuffer = 10000
	}
	return nil
}
