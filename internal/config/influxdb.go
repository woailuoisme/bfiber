package config

import (
	"cmp"
	"errors"
	"strings"
	"time"
)

const (
	defaultInfluxDBHost            = "http://127.0.0.1:8086"
	defaultInfluxDBWriteTimeout    = 10 * time.Second
	defaultInfluxDBQueryTimeout    = 30 * time.Second
	defaultInfluxDBIdleConnTimeout = 90 * time.Second
	defaultInfluxDBMaxIdleConns    = 100
	defaultInfluxDBBatchSize       = 1000
	defaultInfluxDBFlushInterval   = 1 * time.Second
	defaultInfluxDBBatchMaxBuffer  = 10000
)

// InfluxDBConfig 描述 InfluxDB 3 时序数据库连接与批处理参数。
type InfluxDBConfig struct {
	Enabled               bool          `env:"INFLUXDB_ENABLED" envDefault:"false"`
	Host                  string        `env:"INFLUXDB_HOST" envDefault:"http://127.0.0.1:8086"`
	Token                 string        `env:"INFLUXDB_TOKEN"`
	Database              string        `env:"INFLUXDB_DATABASE"`
	Organization          string        `env:"INFLUXDB_ORG"`
	WriteTimeout          time.Duration `env:"INFLUXDB_WRITE_TIMEOUT" envDefault:"10s"`
	QueryTimeout          time.Duration `env:"INFLUXDB_QUERY_TIMEOUT" envDefault:"30s"`
	IdleConnectionTimeout time.Duration `env:"INFLUXDB_IDLE_CONN_TIMEOUT" envDefault:"90s"`
	MaxIdleConnections    int           `env:"INFLUXDB_MAX_IDLE_CONNS" envDefault:"100"`
	BatchSize             int           `env:"INFLUXDB_BATCH_SIZE" envDefault:"1000"`
	FlushInterval         time.Duration `env:"INFLUXDB_FLUSH_INTERVAL" envDefault:"1s"`
	BatchMaxBuffer        int           `env:"INFLUXDB_BATCH_MAX_BUFFER" envDefault:"10000"`
}

// validateInfluxDB 校验 InfluxDB 3 参数，为未配置字段赋予默认值。
func (c *Config) validateInfluxDB() error {
	c.InfluxDB.Host = cmp.Or(strings.TrimSpace(c.InfluxDB.Host), defaultInfluxDBHost)
	if c.InfluxDB.WriteTimeout <= 0 {
		c.InfluxDB.WriteTimeout = defaultInfluxDBWriteTimeout
	}
	if c.InfluxDB.QueryTimeout <= 0 {
		c.InfluxDB.QueryTimeout = defaultInfluxDBQueryTimeout
	}
	if c.InfluxDB.IdleConnectionTimeout <= 0 {
		c.InfluxDB.IdleConnectionTimeout = defaultInfluxDBIdleConnTimeout
	}
	if c.InfluxDB.MaxIdleConnections <= 0 {
		c.InfluxDB.MaxIdleConnections = defaultInfluxDBMaxIdleConns
	}
	if c.InfluxDB.BatchSize <= 0 {
		c.InfluxDB.BatchSize = defaultInfluxDBBatchSize
	}
	if c.InfluxDB.FlushInterval <= 0 {
		c.InfluxDB.FlushInterval = defaultInfluxDBFlushInterval
	}
	if c.InfluxDB.BatchMaxBuffer <= 0 {
		c.InfluxDB.BatchMaxBuffer = defaultInfluxDBBatchMaxBuffer
	}

	if c.InfluxDB.Enabled {
		if strings.TrimSpace(c.InfluxDB.Host) == "" {
			return errors.New("config: INFLUXDB_HOST is required when InfluxDB is enabled")
		}
		if strings.TrimSpace(c.InfluxDB.Database) == "" {
			return errors.New("config: INFLUXDB_DATABASE is required when InfluxDB is enabled")
		}
	}
	return nil
}
