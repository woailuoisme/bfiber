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

	errCh := make(chan error, 2)
	go func() {
		base := a.serverURL()
		fields := []zap.Field{
			zap.String("addr", addr),
			zap.String("url", base),
			zap.String("env", a.cfg.App.Env),
			zap.String("storage", a.cfg.Storage.Driver),
		}
		if a.cfg.App.EnableDocs {
			fields = append(fields, zap.String("docs", base+docsPagePath))
		}
		a.logger.Info("HTTP server started and listening", fields...)

		listenCfg := fiber.ListenConfig{
			DisableStartupMessage: true,
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
