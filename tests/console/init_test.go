package console_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/console"
	"bfiber/internal/console/core"
)

// TestStorageInitOptions_Run_Local 测试 local 存储驱动下 storage:init 自动创建目录并保证幂等。
func TestStorageInitOptions_Run_Local(t *testing.T) {
	tempDir := t.TempDir()
	targetRoot := filepath.Join(tempDir, "storage", "uploads")

	var outBuf bytes.Buffer
	streams := &core.IOStreams{
		Out:    &outBuf,
		ErrOut: &outBuf,
	}

	testCfg := &config.Config{
		Storage: config.StorageConfig{
			Driver: config.StorageDriverLocal,
			Local: config.LocalStorageConfig{
				Root: targetRoot,
			},
		},
	}

	f := &console.TestFactory{
		StreamsValue: streams,
		ConfigValue:  testCfg,
	}

	opts := console.NewStorageInitOptions(f)
	ctx := context.Background()

	// 1. 首次执行：创建目录
	err := opts.Run(ctx)
	require.NoError(t, err)

	info, err := os.Stat(targetRoot)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Contains(t, outBuf.String(), "Local storage directory")

	// 2. 二次执行：天然幂等不报错
	outBuf.Reset()
	err = opts.Run(ctx)
	require.NoError(t, err)
	assert.Contains(t, outBuf.String(), "is ready")
}

// TestStorageInitOptions_Run_S3_MissingConfig 测试 S3 驱动缺少关键配置时的拦截保护。
func TestStorageInitOptions_Run_S3_MissingConfig(t *testing.T) {
	var outBuf bytes.Buffer
	streams := &core.IOStreams{
		Out:    &outBuf,
		ErrOut: &outBuf,
	}

	testCfg := &config.Config{
		Storage: config.StorageConfig{
			Driver: config.StorageDriverS3,
			S3: config.S3StorageConfig{
				Endpoint: "", // 故意缺失
				Bucket:   "",
			},
		},
	}

	f := &console.TestFactory{
		StreamsValue: streams,
		ConfigValue:  testCfg,
	}

	opts := console.NewStorageInitOptions(f)
	ctx := context.Background()

	err := opts.Run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_BUCKET cannot be empty")
}

// TestDBInitOptions_Run_MissingURL 测试 DB_URL 为空时的防御性拦截。
func TestDBInitOptions_Run_MissingURL(t *testing.T) {
	var outBuf bytes.Buffer
	streams := &core.IOStreams{
		Out:    &outBuf,
		ErrOut: &outBuf,
	}

	testCfg := &config.Config{
		Database: config.DatabaseConfig{
			URL: "",
		},
	}

	f := &console.TestFactory{
		StreamsValue: streams,
		ConfigValue:  testCfg,
	}

	opts := console.NewDBInitOptions(f)
	ctx := context.Background()

	err := opts.Run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_URL cannot be empty")
}

// TestAppInitOptions_Run_SkipAll 测试当跳过所有外部 I/O 资源时，app:init 的纯内存控制流平稳运行。
func TestAppInitOptions_Run_SkipAll(t *testing.T) {
	var outBuf bytes.Buffer
	streams := &core.IOStreams{
		Out:    &outBuf,
		ErrOut: &outBuf,
	}

	f := &console.TestFactory{
		StreamsValue: streams,
	}

	opts := console.NewAppInitOptions(f)
	opts.SkipDB = true
	opts.SkipStorage = true
	opts.SkipJWT = true

	ctx := context.Background()
	err := opts.Run(ctx)
	require.NoError(t, err)

	output := outBuf.String()
	assert.Contains(t, output, "Initializing bfiber application environment...")
	assert.Contains(t, output, "Skipping JWT secret initialization")
	assert.Contains(t, output, "Skipping database initialization")
	assert.Contains(t, output, "Skipping storage initialization")
	assert.Contains(t, output, "Application environment initialization finished successfully!")
}
