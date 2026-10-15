package config

import (
	"cmp"
	"strings"
)

const (
	defaultTemporalHostPort    = "127.0.0.1:7233"
	defaultTemporalNamespace   = "default"
	defaultTemporalTaskQueue   = "bfiber-tasks"
	defaultTemporalConcurrency = 10
)

// TemporalConfig 描述 Temporal 分布式工作流客户端与 Worker 协程参数。
type TemporalConfig struct {
	Enabled             bool   `env:"TEMPORAL_ENABLED" envDefault:"false"`
	HostPort            string `env:"TEMPORAL_HOST_PORT" envDefault:"127.0.0.1:7233"`
	Namespace           string `env:"TEMPORAL_NAMESPACE" envDefault:"default"`
	TaskQueue           string `env:"TEMPORAL_TASK_QUEUE" envDefault:"bfiber-tasks"`
	ActivityConcurrency int    `env:"TEMPORAL_ACTIVITY_CONCURRENCY" envDefault:"10"`
	WorkflowConcurrency int    `env:"TEMPORAL_WORKFLOW_CONCURRENCY" envDefault:"10"`
}

// validateTemporal 校验 Temporal 参数，为未配置字段赋予默认值。
func (c *Config) validateTemporal() error {
	c.Temporal.HostPort = cmp.Or(strings.TrimSpace(c.Temporal.HostPort), defaultTemporalHostPort)
	c.Temporal.Namespace = cmp.Or(strings.TrimSpace(c.Temporal.Namespace), defaultTemporalNamespace)
	c.Temporal.TaskQueue = cmp.Or(strings.TrimSpace(c.Temporal.TaskQueue), defaultTemporalTaskQueue)
	if c.Temporal.ActivityConcurrency <= 0 {
		c.Temporal.ActivityConcurrency = defaultTemporalConcurrency
	}
	if c.Temporal.WorkflowConcurrency <= 0 {
		c.Temporal.WorkflowConcurrency = defaultTemporalConcurrency
	}
	return nil
}
