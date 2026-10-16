// Package realtime 包含 Centrifugo 实时通信辅助工厂与错误定义。
package realtime

import (
	"bfiber/internal/config"
)

// RegisterRealtimeGateway 实例化并注册实时通信网关。
func RegisterRealtimeGateway(cfg *config.Config, reg *Registry) (Gateway, error) {
	if cfg == nil {
		return NewManager(nil, WithRegistry(reg)), nil
	}
	return NewManager(&cfg.Realtime, WithRegistry(reg)), nil
}
