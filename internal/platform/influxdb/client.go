package influxdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	"go.uber.org/zap"
)

// Client 封装 InfluxDB 3 官方客户端，提供指标写入、Flight SQL 流式查询与批处理缓冲基座。
type Client struct {
	db         *influxdb3.Client
	config     Config
	httpClient *http.Client
	log        *zap.Logger
	batch      *BatchWriter

	mu     sync.RWMutex
	closed bool
}

// NewClient 构造并初始化 InfluxDB 3 客户端及其后台批量处理协程。
func NewClient(cfg Config, log *zap.Logger) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	db, err := influxdb3.New(influxdb3.ClientConfig{
		Host:                  cfg.Host,
		Token:                 cfg.Token,
		Database:              cfg.Database,
		Organization:          cfg.Organization,
		WriteTimeout:          cfg.WriteTimeout,
		QueryTimeout:          cfg.QueryTimeout,
		IdleConnectionTimeout: cfg.IdleConnectionTimeout,
		MaxIdleConnections:    cfg.MaxIdleConnections,
	})
	if err != nil {
		return nil, fmt.Errorf("influxdb: failed to initialize client: %w", err)
	}

	httpClient := &http.Client{
		Timeout: cfg.WriteTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.MaxIdleConnections,
			MaxIdleConnsPerHost: cfg.MaxIdleConnections,
			IdleConnTimeout:     cfg.IdleConnectionTimeout,
		},
	}

	client := &Client{
		db:         db,
		config:     cfg,
		httpClient: httpClient,
		log:        log,
	}

	client.batch = NewBatchWriter(client)
	return client, nil
}

// Write 同步写入 Line Protocol 格式字节流。
func (c *Client) Write(ctx context.Context, lineProtocol []byte, opts ...influxdb3.WriteOption) error {
	if len(lineProtocol) == 0 {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return ErrClientClosed
	}

	if err := c.db.Write(ctx, lineProtocol, opts...); err != nil {
		return fmt.Errorf("influxdb: write failed: %w", err)
	}
	return nil
}

// WritePoints 同步批量写入一组结构化 Point。
func (c *Client) WritePoints(ctx context.Context, points []*influxdb3.Point, opts ...influxdb3.WriteOption) error {
	if len(points) == 0 {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return ErrClientClosed
	}

	if err := c.db.WritePoints(ctx, points, opts...); err != nil {
		return fmt.Errorf("influxdb: write points failed: %w", err)
	}
	return nil
}

// Query 执行 Flight SQL 查询，返回受保护的流式游标 Rows。
// 必须在消费完毕或异常退出时显式调用 Rows.Close()。
func (c *Client) Query(ctx context.Context, sql string, opts ...influxdb3.QueryOption) (*Rows, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrClientClosed
	}

	queryCtx, cancel := context.WithTimeout(ctx, c.config.QueryTimeout)
	iter, err := c.db.Query(queryCtx, sql, opts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("influxdb: query failed: %w", err)
	}

	return newRows(iter, cancel), nil
}

// QueryEach 执行 Flight SQL 查询并通过回调闭包逐行处理数据，自动闭环管理游标流生命周期。
func (c *Client) QueryEach(ctx context.Context, sql string, fn func(row map[string]any) error, opts ...influxdb3.QueryOption) error {
	rows, err := c.Query(ctx, sql, opts...)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		if err := fn(rows.Value()); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Batch 获取挂载在客户端上的默认异步批量写入器。
func (c *Client) Batch() *BatchWriter {
	return c.batch
}

// Ping 执行 InfluxDB 服务健康检查与连通性探针，契合 ready.Pinger 接口。
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errors.New("influxdb: client is nil")
	}

	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return ErrClientClosed
	}

	pingURL := strings.TrimRight(c.config.Host, "/") + "/ping"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		return fmt.Errorf("influxdb: create ping request failed: %w", err)
	}

	if c.config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("influxdb: ping request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("influxdb: ping returned unexpected status code %d", resp.StatusCode)
	}
	return nil
}

// Driver 报告时序数据库底层驱动标识。
func (c *Client) Driver() string {
	return "influxdb3"
}

// Enabled 报告客户端当前是否处于激活启用状态。
func (c *Client) Enabled() bool {
	return c != nil && c.config.Enabled
}

// Raw 返回底层官方原生 *influxdb3.Client 实例以备高阶扩展。
func (c *Client) Raw() *influxdb3.Client {
	return c.db
}

// Close 依次关闭批量写入器（排空刷盘）与底层 InfluxDB 连接池。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	var firstErr error
	if c.batch != nil {
		if err := c.batch.Close(); err != nil {
			firstErr = err
		}
	}

	if c.db != nil {
		if err := c.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
