package influxdb

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	"go.uber.org/zap"
)

// ErrBufferFull 表示批处理器缓冲区已满，无法接收更多点位。
var ErrBufferFull = errors.New("influxdb: batch buffer is full")

// BatchOption 定义 BatchWriter 的初始化配置项。
type BatchOption func(*BatchWriter)

// WithBatchSize 设置单批次写入的最大点位数阈值。
func WithBatchSize(size int) BatchOption {
	return func(bw *BatchWriter) {
		if size > 0 {
			bw.batchSize = size
		}
	}
}

// WithFlushInterval 设置后台定期刷写的时限。
func WithFlushInterval(interval time.Duration) BatchOption {
	return func(bw *BatchWriter) {
		if interval > 0 {
			bw.flushInterval = interval
		}
	}
}

// WithMaxBuffer 设置内存缓冲区容纳的最大积压上限。
func WithMaxBuffer(max int) BatchOption {
	return func(bw *BatchWriter) {
		if max > 0 {
			bw.maxBuffer = max
		}
	}
}

// BatchWriter 提供高并发异步点位收集、内存排队与定时/定容批量聚合刷盘机制。
type BatchWriter struct {
	client        *Client
	batchSize     int
	flushInterval time.Duration
	maxBuffer     int

	mu     sync.Mutex
	points []*influxdb3.Point
	lines  [][]byte

	flushMu   sync.Mutex
	wakeChan  chan struct{}
	doneChan  chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	closed    bool
}

// NewBatchWriter 构造并启动后台批量刷写器实例。
func NewBatchWriter(client *Client, opts ...BatchOption) *BatchWriter {
	bw := &BatchWriter{
		client:        client,
		batchSize:     client.config.BatchSize,
		flushInterval: client.config.FlushInterval,
		maxBuffer:     client.config.BatchMaxBuffer,
		wakeChan:      make(chan struct{}, 1),
		doneChan:      make(chan struct{}),
	}
	for _, opt := range opts {
		opt(bw)
	}

	bw.points = make([]*influxdb3.Point, 0, bw.batchSize)
	bw.lines = make([][]byte, 0, bw.batchSize)

	bw.wg.Add(1)
	go bw.worker()

	return bw
}

// Add 线程安全地追加结构化 Point 到批量缓冲池中。
func (bw *BatchWriter) Add(ctx context.Context, points ...*influxdb3.Point) error {
	if len(points) == 0 {
		return nil
	}

	bw.mu.Lock()
	if bw.closed {
		bw.mu.Unlock()
		return ErrClientClosed
	}
	if len(bw.points)+len(bw.lines)+len(points) > bw.maxBuffer {
		bw.mu.Unlock()
		return ErrBufferFull
	}

	bw.points = append(bw.points, points...)
	shouldWake := len(bw.points) >= bw.batchSize
	bw.mu.Unlock()

	if shouldWake {
		bw.notifyWake()
	}
	return nil
}

// AddBytes 线程安全地追加 Line Protocol 原始字节行到批量缓冲池中。
func (bw *BatchWriter) AddBytes(ctx context.Context, lines ...[]byte) error {
	if len(lines) == 0 {
		return nil
	}

	bw.mu.Lock()
	if bw.closed {
		bw.mu.Unlock()
		return ErrClientClosed
	}
	if len(bw.points)+len(bw.lines)+len(lines) > bw.maxBuffer {
		bw.mu.Unlock()
		return ErrBufferFull
	}

	for _, line := range lines {
		if len(line) > 0 {
			bw.lines = append(bw.lines, line)
		}
	}
	shouldWake := len(bw.lines) >= bw.batchSize
	bw.mu.Unlock()

	if shouldWake {
		bw.notifyWake()
	}
	return nil
}

// notifyWake 触发非阻塞的工作协程唤醒信号。
func (bw *BatchWriter) notifyWake() {
	select {
	case bw.wakeChan <- struct{}{}:
	default:
	}
}

// Flush 立即将当前缓冲区的所有待写点位刷入 InfluxDB。
func (bw *BatchWriter) Flush(ctx context.Context) error {
	return bw.flushInternal(ctx)
}

// Close 优雅停止后台批量刷盘调度并刷新剩余未刷盘数据。
func (bw *BatchWriter) Close() error {
	var err error
	bw.closeOnce.Do(func() {
		bw.mu.Lock()
		bw.closed = true
		bw.mu.Unlock()

		close(bw.doneChan)
		bw.wg.Wait()

		// 终态排空刷写剩余数据
		ctx, cancel := context.WithTimeout(context.Background(), bw.client.config.WriteTimeout)
		defer cancel()
		err = bw.flushInternal(ctx)
	})
	return err
}

// worker 后台定时与定容驱动循环。
func (bw *BatchWriter) worker() {
	defer bw.wg.Done()
	ticker := time.NewTicker(bw.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bw.doneChan:
			return
		case <-ticker.C:
			bw.safeFlush()
		case <-bw.wakeChan:
			bw.safeFlush()
		}
	}
}

// safeFlush 在后台执行保护性刷盘并捕获错误日志。
func (bw *BatchWriter) safeFlush() {
	ctx, cancel := context.WithTimeout(context.Background(), bw.client.config.WriteTimeout)
	defer cancel()

	if err := bw.flushInternal(ctx); err != nil && bw.client.log != nil {
		bw.client.log.Error("failed to flush influxdb batch buffer", zap.Error(err))
	}
}

// flushInternal 提取并执行真正的网络批量写入操作。
func (bw *BatchWriter) flushInternal(ctx context.Context) error {
	bw.flushMu.Lock()
	defer bw.flushMu.Unlock()

	bw.mu.Lock()
	pts := bw.points
	bw.points = make([]*influxdb3.Point, 0, bw.batchSize)
	lns := bw.lines
	bw.lines = make([][]byte, 0, bw.batchSize)
	bw.mu.Unlock()

	if len(pts) == 0 && len(lns) == 0 {
		return nil
	}

	var firstErr error
	if len(pts) > 0 {
		if err := bw.client.WritePoints(ctx, pts); err != nil {
			firstErr = err
		}
	}

	if len(lns) > 0 {
		payload := bytes.Join(lns, []byte("\n"))
		if err := bw.client.Write(ctx, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
