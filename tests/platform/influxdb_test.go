package platform_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/platform/influxdb"
)

func TestInfluxDB_ConfigValidation(t *testing.T) {
	// 验证未启用时允许空配置
	disabledCfg := influxdb.Config{Enabled: false}
	require.NoError(t, disabledCfg.Validate())

	// 验证启用时缺少 Host 报错
	missingHost := influxdb.Config{
		Enabled:  true,
		Database: "test_db",
	}
	require.Error(t, missingHost.Validate())

	// 验证启用时缺少 Database 报错
	missingDB := influxdb.Config{
		Enabled: true,
		Host:    "http://127.0.0.1:8086",
	}
	require.Error(t, missingDB.Validate())

	// 验证有效配置与默认值回退
	validCfg := influxdb.Config{
		Enabled:  true,
		Host:     "http://127.0.0.1:8086",
		Database: "metrics",
	}
	require.NoError(t, validCfg.Validate())
	require.Equal(t, 10*time.Second, validCfg.WriteTimeout)
	require.Equal(t, 30*time.Second, validCfg.QueryTimeout)
	require.Equal(t, 90*time.Second, validCfg.IdleConnectionTimeout)
	require.Equal(t, 100, validCfg.MaxIdleConnections)
	require.Equal(t, 1000, validCfg.BatchSize)
	require.Equal(t, 1*time.Second, validCfg.FlushInterval)
	require.Equal(t, 10000, validCfg.BatchMaxBuffer)

	// 验证 FromAppConfig 映射一致性
	appCfg := config.InfluxDBConfig{
		Enabled:               true,
		Host:                  "http://localhost:8086",
		Database:              "prod",
		WriteTimeout:          5 * time.Second,
		QueryTimeout:          15 * time.Second,
		IdleConnectionTimeout: 60 * time.Second,
		MaxIdleConnections:    50,
		BatchSize:             500,
		FlushInterval:         2 * time.Second,
		BatchMaxBuffer:        5000,
	}
	mapped := influxdb.FromAppConfig(appCfg)
	require.True(t, mapped.Enabled)
	require.Equal(t, "http://localhost:8086", mapped.Host)
	require.Equal(t, "prod", mapped.Database)
	require.Equal(t, 5*time.Second, mapped.WriteTimeout)
	require.Equal(t, 15*time.Second, mapped.QueryTimeout)
	require.Equal(t, 50, mapped.MaxIdleConnections)
	require.Equal(t, 500, mapped.BatchSize)
	require.Equal(t, 2*time.Second, mapped.FlushInterval)
	require.Equal(t, 5000, mapped.BatchMaxBuffer)
}

func TestInfluxDB_ClientLifecycleAndProbe(t *testing.T) {
	// 构造测试 HTTP Server 模拟 InfluxDB /ping 端点
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.Header().Set("X-Influxdb-Version", "3.0.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := influxdb.Config{
		Enabled:               true,
		Host:                  server.URL,
		Database:              "system_metrics",
		Token:                 "test-token",
		WriteTimeout:          2 * time.Second,
		QueryTimeout:          2 * time.Second,
		IdleConnectionTimeout: 5 * time.Second,
		MaxIdleConnections:    10,
		BatchSize:             10,
		FlushInterval:         100 * time.Millisecond,
		BatchMaxBuffer:        100,
	}

	client, err := influxdb.NewClient(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() {
		_ = client.Close()
	}()

	require.Equal(t, "influxdb3", client.Driver())
	require.True(t, client.Enabled())
	require.NotNil(t, client.Raw())
	require.NotNil(t, client.Batch())

	// 测试 Ping 探针连通性
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx))

	// 验证空输入防卫
	require.NoError(t, client.Write(ctx, nil))
	require.NoError(t, client.WritePoints(ctx, nil))

	// 关闭客户端
	require.NoError(t, client.Close())
	// 幂等关闭
	require.NoError(t, client.Close())

	// 关闭后操作报错
	require.ErrorIs(t, client.Write(ctx, []byte("cpu,host=serverA value=1")), influxdb.ErrClientClosed)
	require.ErrorIs(t, client.WritePoints(ctx, []*influxdb3.Point{influxdb3.NewPointWithMeasurement("cpu")}), influxdb.ErrClientClosed)
	_, err = client.Query(ctx, "SELECT * FROM cpu")
	require.ErrorIs(t, err, influxdb.ErrClientClosed)
	require.ErrorIs(t, client.Ping(ctx), influxdb.ErrClientClosed)
}

func TestInfluxDB_RowsLifecycle(t *testing.T) {
	canceled := false
	cancelFn := func() {
		canceled = true
	}

	// 游标提前 Close 必须触发上下文 Cancel
	rows := influxdb.NewRowsForTest(nil, cancelFn)
	require.False(t, rows.Next())
	require.True(t, canceled)
	require.NoError(t, rows.Close())
	require.True(t, rows.Done())
	require.Nil(t, rows.Value())
	require.Nil(t, rows.AsPoints())
	require.Nil(t, rows.Index())
	require.Nil(t, rows.Raw())
	require.NoError(t, rows.Err())
}

func TestInfluxDB_BatchWriter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := influxdb.Config{
		Enabled:        true,
		Host:           server.URL,
		Token:          "test-token",
		Database:       "batch_test",
		BatchSize:      5,
		FlushInterval:  50 * time.Millisecond,
		BatchMaxBuffer: 10,
	}

	client, err := influxdb.NewClient(cfg, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = client.Close()
	}()

	bw := influxdb.NewBatchWriter(client,
		influxdb.WithBatchSize(10),
		influxdb.WithFlushInterval(time.Minute),
		influxdb.WithMaxBuffer(3),
	)
	defer func() {
		_ = bw.Close()
	}()

	ctx := t.Context()
	p1 := influxdb3.NewPointWithMeasurement("mem")
	p2 := influxdb3.NewPointWithMeasurement("mem")
	p3 := influxdb3.NewPointWithMeasurement("mem")
	p4 := influxdb3.NewPointWithMeasurement("mem")

	// 追加合法点位（填满上限 3 个）
	require.NoError(t, bw.Add(ctx, p1, p2, p3))

	// 缓冲区溢出测试
	require.ErrorIs(t, bw.Add(ctx, p4), influxdb.ErrBufferFull)
	require.ErrorIs(t, bw.AddBytes(ctx, []byte("disk,path=/ usage=80")), influxdb.ErrBufferFull)

	// 手动触发 Flush
	require.NoError(t, bw.Flush(ctx))

	// 清空后允许继续追加
	require.NoError(t, bw.Add(ctx, p1))
	require.NoError(t, bw.AddBytes(ctx, []byte("disk,path=/ usage=80")))

	// 关闭批处理器
	require.NoError(t, bw.Close())

	// 关闭后追加报错
	require.ErrorIs(t, bw.Add(ctx, p1), influxdb.ErrClientClosed)
	require.ErrorIs(t, bw.AddBytes(ctx, []byte("disk,path=/ usage=80")), influxdb.ErrClientClosed)
}

func TestInfluxDB_PingFailures(t *testing.T) {
	// 500 错误服务端探针
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := influxdb.Config{
		Enabled:  true,
		Host:     server.URL,
		Token:    "test-token",
		Database: "system_metrics",
	}

	client, err := influxdb.NewClient(cfg, zap.NewNop())
	require.NoError(t, err)
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	err = client.Ping(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")

	// 空客户端防卫
	var nilClient *influxdb.Client
	require.Error(t, nilClient.Ping(ctx))
}
