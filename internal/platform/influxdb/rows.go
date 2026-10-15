package influxdb

import (
	"context"
	"io"
	"sync"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	"github.com/apache/arrow-go/v18/arrow/flight"
)

var _ io.Closer = (*Rows)(nil)

// Rows 封装 InfluxDB 3 Flight SQL 查询结果游标与流式上下文生命周期。
// 必须在迭代完毕或提前退出时调用 Close() 释放底层 gRPC 传输流与上下文资源。
type Rows struct {
	iter   *influxdb3.QueryIterator
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
}

// newRows 创建一个安全持有生命周期的 Rows 游标实例。
func newRows(iter *influxdb3.QueryIterator, cancel context.CancelFunc) *Rows {
	return &Rows{
		iter:   iter,
		cancel: cancel,
	}
}

// NewRowsForTest 暴露给测试套件构造游标实例。
func NewRowsForTest(iter *influxdb3.QueryIterator, cancel context.CancelFunc) *Rows {
	return newRows(iter, cancel)
}

// Next 推进游标至下一行。当流结束或发生错误时返回 false 并自动取消上下文。
func (r *Rows) Next() bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	r.mu.Unlock()

	if r.iter == nil {
		_ = r.Close()
		return false
	}

	hasMore := r.iter.Next()
	if !hasMore {
		_ = r.Close()
	}
	return hasMore
}

// Value 返回当前行数据字典映射。
func (r *Rows) Value() map[string]any {
	if r.iter == nil {
		return nil
	}
	return r.iter.Value()
}

// AsPoints 将当前行解析为 InfluxDB 3 PointValues 结构。
func (r *Rows) AsPoints() *influxdb3.PointValues {
	if r.iter == nil {
		return nil
	}
	return r.iter.AsPoints()
}

// Done 检查当前结果集是否已完全读尽。
func (r *Rows) Done() bool {
	if r.iter == nil {
		return true
	}
	return r.iter.Done()
}

// Err 返回游标在迭代过程中捕获的首个底层错误。
func (r *Rows) Err() error {
	if r.iter == nil {
		return nil
	}
	return r.iter.Err()
}

// Index 返回游标当前行的索引。
func (r *Rows) Index() any {
	if r.iter == nil {
		return nil
	}
	return r.iter.Index()
}

// Raw 返回底层 Apache Arrow Flight 记录流读取器。
func (r *Rows) Raw() *flight.Reader {
	if r.iter == nil {
		return nil
	}
	return r.iter.Raw()
}

// Close 幂等地关闭游标、中断未读尽的 gRPC 数据流并释放上下文。
func (r *Rows) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}

	if r.iter != nil {
		return r.iter.Err()
	}
	return nil
}
