// Package media_test 针对无状态媒体上传管道 PipelineImpl 进行契约测试。
package media_test

import (
	"bytes"
	"image/color"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/platform/media"
	"bfiber/internal/platform/storage"
)

func TestPipeline_RoundTrip(t *testing.T) {
	ctx := t.Context()
	rootDir := t.TempDir()
	baseURL := "https://cdn.example.com/uploads"

	disk, err := storage.NewLocalDisk(rootDir, baseURL)
	require.NoError(t, err)

	pipeline := media.NewPipeline(
		disk,
		media.WithUUIDNaming(),
		media.WithCategoryDatePath("uploads"),
	)

	validPNG := solidPNG(t, 1, 1, color.RGBA{A: 0xFF})

	t.Run("UploadBytes 上传成功并生成规范元数据", func(t *testing.T) {
		info, err := pipeline.UploadBytes(ctx, validPNG, "test_logo.png", "logos")
		require.NoError(t, err)
		require.NotNil(t, info)

		assert.Equal(t, "test_logo.png", info.OriginalName)
		assert.Equal(t, int64(len(validPNG)), info.Size)
		assert.Equal(t, "image/png", info.MimeType)
		assert.True(t, strings.HasSuffix(info.FileName, ".png"))
		assert.NotEmpty(t, info.Hash)
		assert.True(t, strings.HasPrefix(info.Path, "uploads/logos/"))
		assert.True(t, strings.HasPrefix(info.URL, baseURL))

		// 检查底层文件是否真的存在
		exists, err := disk.Exists(ctx, info.Path)
		require.NoError(t, err)
		assert.True(t, exists)

		// 测试 Pipeline.URL 方法
		fileURL := pipeline.URL(info.Path)
		assert.Equal(t, info.URL, fileURL)

		// 测试删除
		require.NoError(t, pipeline.Delete(ctx, info.Path))
		existsAfterDel, err := disk.Exists(ctx, info.Path)
		require.NoError(t, err)
		assert.False(t, existsAfterDel)
	})

	t.Run("UploadReader 流式上传验证", func(t *testing.T) {
		reader := bytes.NewReader(validPNG)
		info, err := pipeline.UploadReader(ctx, reader, "stream.png", int64(len(validPNG)), "image/png", "streams")
		require.NoError(t, err)
		require.NotNil(t, info)

		assert.Equal(t, "stream.png", info.OriginalName)
		assert.Equal(t, int64(len(validPNG)), info.Size)

		// 验证后清理
		require.NoError(t, pipeline.Delete(ctx, info.Path))
	})

	t.Run("安全拦截高危文件上传", func(t *testing.T) {
		maliciousPHP := []byte("<?php phpinfo(); ?>")
		_, err := pipeline.UploadBytes(ctx, maliciousPHP, "evil.php", "hacks")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forbidden")
	})
}
