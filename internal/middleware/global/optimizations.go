package global

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/etag"
	"github.com/gofiber/fiber/v3/middleware/favicon"
)

// Favicon 【全局中间件】基于 Fiber v3 官方 favicon 中间件快速拦截并短路 /favicon.ico 请求。
func (m *Middleware) Favicon() fiber.Handler {
	return favicon.New()
}

// CompressOption 定义透明响应体压缩配置函数。
type CompressOption func(*compress.Config)

// WithCompressLevel 自定义指定透明响应体压缩级别（如 compress.LevelBestSpeed、compress.LevelBestCompression）。
func WithCompressLevel(level compress.Level) CompressOption {
	return func(c *compress.Config) {
		c.Level = level
	}
}

// Compress 【全局中间件】基于 Fiber v3 官方 compress 中间件对 HTTP 响应体执行透明 Gzip/Brotli 压缩。
func (m *Middleware) Compress(opts ...CompressOption) fiber.Handler {
	cfg := compress.Config{
		Level: compress.LevelDefault,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return compress.New(cfg)
}

// ETag 【全局中间件】基于 Fiber v3 官方 etag 中间件自动计算弱 ETag 并支持客户端 304 缓存协商。
func (m *Middleware) ETag() fiber.Handler {
	return etag.New()
}
