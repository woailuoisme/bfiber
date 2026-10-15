// Package cache 封装 Redis/Valkey 客户端与 Fiber Storage 适配。
package cache

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/storage/memory/v2"
	rueidisStorage "github.com/gofiber/storage/rueidis"
	"go.uber.org/zap"
)

// Storage 返回集成 Fiber 官方 github.com/gofiber/storage/rueidis 的原生存储实例。
func (c *Client) Storage() fiber.Storage {
	if c == nil || c.rdb == nil {
		return nil
	}
	return rueidisStorage.NewFromConnection(c.rdb)
}

// NewMemoryStorage 返回基于 Fiber 官方 memory/v2 驱动的原生内存存储实例。
func NewMemoryStorage() fiber.Storage {
	return memory.New()
}

// AsFiberStorage 将任意存储对象（fiber.Storage, cache.Store, *Client 等）统一适配为满足 Fiber 官方中间件生态的 fiber.Storage。
// 若传入 nil 则返回 nil；已是 fiber.Storage 则原样返回；若为 Redis Client 则直连 rueidis 官方存储驱动；其余 Store 回退至官方 memory 驱动。
func AsFiberStorage(store any) fiber.Storage {
	if store == nil {
		return nil
	}
	switch s := store.(type) {
	case fiber.Storage:
		return s
	case *Client:
		return s.Storage()
	case Store:
		return NewMemoryStorage()
	default:
		return nil
	}
}

// FailOpen 将任意 fiber.Storage 包装为具备故障降级（Fail-Open）弹性的存储适配器。
// 当底层 Redis 出现网络抖动或集群故障抛出异常时，记录告警日志并自动降级，避免阻塞上层业务流。
func FailOpen(storage fiber.Storage, logger *zap.Logger) fiber.Storage {
	if storage == nil {
		return nil
	}
	return &failOpenStorage{
		Storage: storage,
		logger:  logger,
	}
}

// failOpenStorage 为 Fiber 存储提供故障自动降级（Fail-Open）防线，避免缓存故障级联引发 500 异常。
type failOpenStorage struct {
	fiber.Storage
	logger      *zap.Logger
	lastLogTime atomic.Int64 // 秒级时间戳，用于错误日志限频（5 秒最多输出一次），杜绝高并发下的日志风暴
}

var _ fiber.Storage = (*failOpenStorage)(nil)

func (s *failOpenStorage) logErr(op string, err error) {
	if err == nil || s.logger == nil {
		return
	}
	now := time.Now().Unix()
	last := s.lastLogTime.Load()
	if now-last >= 5 && s.lastLogTime.CompareAndSwap(last, now) {
		s.logger.Warn("cache storage error, falling back to fail-open",
			zap.String("op", op),
			zap.Error(err),
		)
	}
}

func (s *failOpenStorage) Get(key string) ([]byte, error) {
	if s.Storage == nil {
		return nil, nil
	}
	val, err := s.Storage.Get(key)
	if err != nil {
		s.logErr("get", err)
	}
	return val, nil
}

func (s *failOpenStorage) GetWithContext(ctx context.Context, key string) ([]byte, error) {
	if s.Storage == nil {
		return nil, nil
	}
	val, err := s.Storage.GetWithContext(ctx, key)
	if err != nil {
		s.logErr("get_with_context", err)
	}
	return val, nil
}

func (s *failOpenStorage) Set(key string, val []byte, exp time.Duration) error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.Set(key, val, exp); err != nil {
		s.logErr("set", err)
	}
	return nil
}

func (s *failOpenStorage) SetWithContext(ctx context.Context, key string, val []byte, exp time.Duration) error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.SetWithContext(ctx, key, val, exp); err != nil {
		s.logErr("set_with_context", err)
	}
	return nil
}

func (s *failOpenStorage) Delete(key string) error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.Delete(key); err != nil {
		s.logErr("delete", err)
	}
	return nil
}

func (s *failOpenStorage) DeleteWithContext(ctx context.Context, key string) error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.DeleteWithContext(ctx, key); err != nil {
		s.logErr("delete_with_context", err)
	}
	return nil
}

func (s *failOpenStorage) Reset() error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.Reset(); err != nil {
		s.logErr("reset", err)
	}
	return nil
}

func (s *failOpenStorage) ResetWithContext(ctx context.Context) error {
	if s.Storage == nil {
		return nil
	}
	if err := s.Storage.ResetWithContext(ctx); err != nil {
		s.logErr("reset_with_context", err)
	}
	return nil
}

func (s *failOpenStorage) Close() error {
	if s.Storage == nil {
		return nil
	}
	return s.Storage.Close()
}
