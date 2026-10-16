// Package bootstrap 负责应用依赖装配与生命周期管理。
package bootstrap

import (
	"sync"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/platform/eventbus"
)

// Application 聚合装配完成后仍需在运行期被消费的句柄。
//
// 其余子系统的生命周期由装配期登记的 teardown 统一承担：句柄只要已被
// feature 服务持有或已登记释放回调，此处就不再保留第二份引用。
type Application struct {
	closeOnce sync.Once
	teardown  *teardown
	cfg       *config.Config
	logger    *zap.Logger
	// eventbus 需在 Run 中驱动其路由，因此必须由 Application 持有。
	eventbus *eventbus.Manager
	engine   *fiber.App
}

// Engine 暴露已装配的路由引擎，供集成测试直接驱动请求。
func (a *Application) Engine() *fiber.App { return a.engine }

// Close 释放全部外部资源与客户端连接，通过 sync.Once 保证多次调用幂等安全。
// 释放清单由装配期登记的 teardown 单一维护，此处不再重复列举资源。
func (a *Application) Close() {
	a.closeOnce.Do(func() {
		if a.teardown != nil {
			a.teardown.close()
		}
	})
}
