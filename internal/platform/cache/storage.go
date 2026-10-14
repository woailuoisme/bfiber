// Package cache 封装 Redis/Valkey 客户端。
package cache

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/rueidis"
)

// fiberStorage 将 Client 适配为 Fiber v3 官方的 fiber.Storage 接口。
type fiberStorage struct {
	client *Client
	prefix string
}

var _ fiber.Storage = (*fiberStorage)(nil)

func normalizePrefix(prefix []string) string {
	if len(prefix) > 0 {
		if p := strings.TrimSpace(prefix[0]); p != "" {
			return p
		}
	}
	return "storage"
}

// Storage 返回满足 Fiber v3 官方中间件生态契约的 fiber.Storage 存储实例。
func (c *Client) Storage(prefix ...string) fiber.Storage {
	return &fiberStorage{
		client: c,
		prefix: normalizePrefix(prefix),
	}
}

func (s *fiberStorage) fullKey(k string) string {
	return Key(s.prefix, k)
}

// GetWithContext 根据指定键与请求上下文读取二进制内容；键不存在时返回 nil, nil。
func (s *fiberStorage) GetWithContext(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil || key == "" {
		return nil, nil
	}
	res := s.client.do(ctx, s.client.rdb.B().Get().Key(s.fullKey(key)).Build())
	val, err := res.AsBytes()
	if err == nil {
		return val, nil
	}
	if errors.Is(err, rueidis.Nil) {
		return nil, nil
	}
	return nil, err
}

// Get 根据指定键读取二进制内容；键不存在时返回 nil, nil。
func (s *fiberStorage) Get(key string) ([]byte, error) {
	return s.GetWithContext(context.Background(), key)
}

// SetWithContext 将指定键值写入存储，exp 为 0 时表示不过期。
func (s *fiberStorage) SetWithContext(ctx context.Context, key string, val []byte, exp time.Duration) error {
	if s == nil || s.client == nil || len(key) == 0 || len(val) == 0 {
		return nil
	}
	cmd := s.client.rdb.B().Set().Key(s.fullKey(key)).Value(rueidis.BinaryString(val))
	if exp > 0 {
		cmd.Ex(exp)
	}
	return s.client.do(ctx, cmd.Build()).Error()
}

// Set 将指定键值写入存储，exp 为 0 时表示不过期。
func (s *fiberStorage) Set(key string, val []byte, exp time.Duration) error {
	return s.SetWithContext(context.Background(), key, val, exp)
}

// DeleteWithContext 删除指定键；键不存在时不报错。
func (s *fiberStorage) DeleteWithContext(ctx context.Context, key string) error {
	if s == nil || s.client == nil || key == "" {
		return nil
	}
	err := s.client.do(ctx, s.client.rdb.B().Del().Key(s.fullKey(key)).Build()).Error()
	if errors.Is(err, rueidis.Nil) {
		return nil
	}
	return err
}

// Delete 删除指定键；键不存在时不报错。
func (s *fiberStorage) Delete(key string) error {
	return s.DeleteWithContext(context.Background(), key)
}

// ResetWithContext 重置存储并批量清除属于当前前缀空间的所有键。
func (s *fiberStorage) ResetWithContext(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	pattern := Key(s.prefix, "*")
	var cursor uint64
	for {
		res, err := s.client.rdb.Do(ctx, s.client.rdb.B().Scan().Cursor(cursor).Match(pattern).Count(100).Build()).AsScanEntry()
		if err != nil {
			return err
		}
		if len(res.Elements) > 0 {
			if delErr := s.client.do(ctx, s.client.rdb.B().Del().Key(res.Elements...).Build()).Error(); delErr != nil {
				return delErr
			}
		}
		cursor = res.Cursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// Reset 重置存储并批量清除属于当前前缀空间的所有键。
func (s *fiberStorage) Reset() error {
	return s.ResetWithContext(context.Background())
}

// Close 满足 fiber.Storage 接口契约，底层连接生命周期由应用程序统一管控。
func (s *fiberStorage) Close() error {
	return nil
}

// AsFiberStorage 将任意 Store 适配为满足 Fiber 官方中间件生态的 fiber.Storage。
func AsFiberStorage(s Store, prefix ...string) fiber.Storage {
	if s == nil {
		return nil
	}
	if client, ok := s.(*Client); ok {
		return client.Storage(prefix...)
	}
	return &genericFiberStorage{
		store:  s,
		prefix: normalizePrefix(prefix),
	}
}

type genericFiberStorage struct {
	store  Store
	prefix string
}

var _ fiber.Storage = (*genericFiberStorage)(nil)

func (s *genericFiberStorage) fullKey(k string) string {
	return Key(s.prefix, k)
}

func (s *genericFiberStorage) GetWithContext(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.store == nil || key == "" {
		return nil, nil
	}
	v, err := s.store.Get(ctx, s.fullKey(key))
	if err == nil {
		return []byte(v), nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return nil, err
}

func (s *genericFiberStorage) Get(key string) ([]byte, error) {
	return s.GetWithContext(context.Background(), key)
}

func (s *genericFiberStorage) SetWithContext(ctx context.Context, key string, val []byte, exp time.Duration) error {
	if s == nil || s.store == nil || key == "" || len(val) == 0 {
		return nil
	}
	ttl := exp
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return s.store.Set(ctx, s.fullKey(key), string(val), ttl)
}

func (s *genericFiberStorage) Set(key string, val []byte, exp time.Duration) error {
	return s.SetWithContext(context.Background(), key, val, exp)
}

func (s *genericFiberStorage) DeleteWithContext(ctx context.Context, key string) error {
	if s == nil || s.store == nil || key == "" {
		return nil
	}
	_, err := s.store.Delete(ctx, s.fullKey(key))
	return err
}

func (s *genericFiberStorage) Delete(key string) error {
	return s.DeleteWithContext(context.Background(), key)
}

func (s *genericFiberStorage) ResetWithContext(_ context.Context) error {
	return nil
}

func (s *genericFiberStorage) Reset() error {
	return nil
}

func (s *genericFiberStorage) Close() error {
	return nil
}
