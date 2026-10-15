package bootstrap

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// serverURL 构造服务对外访问基础 URL，优先采用应用配置中的 APP_URL。
func (a *Application) serverURL() string {
	if a.cfg.App.URL != "" {
		return strings.TrimRight(a.cfg.App.URL, "/")
	}
	return fmt.Sprintf("http://localhost:%d", a.cfg.App.Port)
}

// Serve 阻塞式启动 HTTP 服务与后台组件，收到 ctx 取消信号后执行优雅停机。
func (a *Application) Serve(ctx context.Context) error {
	srvCfg := a.cfg.App.Server
	addr := fmt.Sprintf(":%d", a.cfg.App.Port)

	// 注册生命周期钩子：通过 Fiber v3 原生 OnListen 获取真实监听元数据，通过 OnPostShutdown 触发资源回收。
	a.registerLifecycleHooks()

	errCh := make(chan error, 2)
	go func() {
		listenCfg := fiber.ListenConfig{
			DisableStartupMessage: true,
			EnablePrefork:         srvCfg.Prefork,
		}
		if err := a.engine.Listen(addr, listenCfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP server exited unexpectedly: %w", err)
		}
	}()

	if a.eventbus != nil {
		go func() {
			if err := a.eventbus.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Error("event bus router stopped unexpectedly", zap.Error(err))
				select {
				case errCh <- fmt.Errorf("event bus exited unexpectedly: %w", err):
				default:
				}
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.logger.Info("shutdown signal received, initiating graceful shutdown")
	}

	shutdownTimeout := cmp.Or(srvCfg.ShutdownTimeout, 3*time.Second)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := a.engine.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	return nil
}

// registerLifecycleHooks 注册 Fiber 引擎生命周期钩子，实现监听就绪与停机清理的统一编排。
func (a *Application) registerLifecycleHooks() {
	a.engine.Hooks().OnListen(func(ld fiber.ListenData) error {
		base := a.serverURL()
		listenAddr := fmt.Sprintf("%s:%s", ld.Host, ld.Port)
		if ld.Host == "" {
			listenAddr = ":" + ld.Port
		}
		fields := []zap.Field{
			zap.String("addr", listenAddr),
			zap.String("url", base),
			zap.String("env", a.cfg.App.Env),
			zap.String("storage", a.cfg.Storage.Driver),
			zap.Int("pid", ld.PID),
			zap.Bool("tls", ld.TLS),
		}
		if a.cfg.App.EnableDocs {
			fields = append(fields, zap.String("docs", base+docsPagePath))
		}
		a.logger.Info("HTTP server started and listening", fields...)
		return nil
	})
}
