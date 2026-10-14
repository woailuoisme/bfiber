// Package bootstrap 负责应用依赖装配与生命周期管理。
package bootstrap

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/eventbus"
	"bfiber/internal/platform/mailer"
	"bfiber/internal/platform/notification"
	"bfiber/internal/platform/payment"
	"bfiber/internal/platform/pdf"
	"bfiber/internal/platform/queue"
	"bfiber/internal/platform/realtime"
	"bfiber/internal/platform/search"
	platformsentry "bfiber/internal/platform/sentry"
	"bfiber/internal/platform/storage"
	"bfiber/internal/platform/telemetry"
	"bfiber/internal/platform/wechat"
	"bfiber/internal/shared/media"
)

// Application 聚合装配完成的系统依赖与 HTTP 引擎。
type Application struct {
	cfg          *config.Config
	logger       *zap.Logger
	db           *bun.DB
	cache        *cache.Client
	locker       cache.Locker
	mailer       mailer.Mailer
	notification *notification.Notifier
	storage      storage.Disk
	media        *media.Manager
	pdf          *pdf.Service
	wechat       *wechat.Manager
	payment      *payment.Manager
	realtime     realtime.Manager
	search       search.Client
	eventbus     *eventbus.Manager
	queue        *queue.Client
	temporal     client.Client
	engine       *fiber.App
}

// Engine 暴露已装配的路由引擎，供集成测试直接驱动请求。
func (a *Application) Engine() *fiber.App { return a.engine }

// App 暴露已装配的 Fiber 实例。
func (a *Application) App() *fiber.App { return a.engine }

// DB 暴露数据库连接句柄。
func (a *Application) DB() *bun.DB { return a.db }

// WeChat 暴露已装配的微信多应用管理器。
func (a *Application) WeChat() *wechat.Manager { return a.wechat }

// Locker 暴露系统级分布式互斥锁管理器（支持看门狗自动续期与原子释放）。
func (a *Application) Locker() cache.Locker { return a.locker }

// EventBus 暴露已装配的领域事件总线管理器。
func (a *Application) EventBus() *eventbus.Manager { return a.eventbus }

// Queue 暴露已装配的异步队列客户端。
func (a *Application) Queue() *queue.Client { return a.queue }

// Temporal 暴露已装配的 Temporal 分布式工作流客户端（未启用时为 nil）。
func (a *Application) Temporal() client.Client { return a.temporal }

// Close 释放全部外部资源与客户端连接。
func (a *Application) Close() {
	if a.temporal != nil {
		a.temporal.Close()
	}
	if a.eventbus != nil {
		a.closeResource("event bus manager", a.eventbus.Close)
	}
	if a.queue != nil {
		a.closeResource("queue client", a.queue.Close)
	}
	if a.notification != nil {
		a.closeResource("notification worker pool", a.notification.Close)
	}
	if a.mailer != nil {
		a.closeResource("SMTP connection", a.mailer.Close)
	}
	if a.locker != nil {
		a.closeResource("distributed locker", a.locker.Close)
	}
	if a.cache != nil {
		a.closeResource("Redis connection", a.cache.Close)
	}
	if a.db != nil {
		a.closeResource("database connection", a.db.Close)
	}
	if err := telemetry.Shutdown(context.Background()); err != nil {
		a.logger.Warn("failed to shutdown telemetry", zap.Error(err))
	}
	platformsentry.Flush(5 * time.Second)
}

func (a *Application) closeResource(name string, closeFn func() error) {
	if err := closeFn(); err != nil {
		a.logger.Warn("failed to close "+name, zap.Error(err))
	}
}
