// Package media_test 针对媒体实体管理门面 Manager 进行契约测试。
package media_test

import (
	"image/color"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/media"
)

func TestManager_ContractsAndURLDerivation(t *testing.T) {
	disk := newTestDisk(t)

	mediaCfg := config.MediaConfig{
		MaxFileSize: 10 * 1024 * 1024,
		Imgproxy: config.ImgproxyConfig{
			BaseURL:  "https://img.example.com",
			Insecure: true,
		},
	}

	// 传入 nil DB 进行上层无状态与契约测试
	mgr := media.New(mediaCfg, config.StorageConfig{}, disk, nil)
	require.NotNil(t, mgr)

	t.Run("Attachable 契约验证", func(t *testing.T) {
		attachable := media.GenericAttachable{
			ModelType: "user",
			ModelID:   "usr_abc123",
		}
		assert.Equal(t, "user", attachable.GetModelType())
		assert.Equal(t, "usr_abc123", attachable.GetModelID())

		userModel := &models.User{
			ID: "usr_999",
		}
		assert.Equal(t, "user", userModel.GetModelType())
		assert.Equal(t, "usr_999", userModel.GetModelID())
	})

	t.Run("URL / Thumbnail / Avatar 动态派生逻辑", func(t *testing.T) {
		item := &models.Media{
			Disk:     "local",
			FileName: "avatars/2026/09/28/sample.png",
			MimeType: "image/png",
		}

		// 1. 原图 URL 获取
		originalURL := mgr.URL(t.Context(), item)
		assert.Equal(t, "https://cdn.example.com/uploads/avatars/2026/09/28/sample.png", originalURL)

		// 2. 头像通过 imgproxy 生成带重心定位的 URL
		avatarURL := mgr.Avatar(t.Context(), item, 80)
		assert.True(t, strings.HasPrefix(avatarURL, "https://img.example.com/insecure/"))
		assert.Contains(t, avatarURL, "/rs:fill:80:80:")
		assert.Contains(t, avatarURL, "/g:fp:")

		// 3. 空模型保护
		assert.Empty(t, mgr.URL(t.Context(), nil))
		assert.Empty(t, mgr.Avatar(t.Context(), nil, 100))
		assert.Empty(t, mgr.Thumbnail(t.Context(), nil))
		assert.Empty(t, mgr.Optimized(t.Context(), nil))

		// 4. 三档派生：缩略图 / 默认参数重编码 / 原图
		thumbURL := mgr.Thumbnail(t.Context(), item)
		assert.Contains(t, thumbURL, "/rs:fit:300:300:", "缩略图应等比缩放填充外接盒")
		assert.Contains(t, thumbURL, "/q:85/")
		assert.True(t, strings.HasSuffix(thumbURL, ".webp"))

		optimizedURL := mgr.Optimized(t.Context(), item)
		assert.NotContains(t, optimizedURL, "/rs:", "重编码不应改变尺寸")
		assert.Contains(t, optimizedURL, "/q:85/")
		assert.True(t, strings.HasSuffix(optimizedURL, ".webp"))

		// 5. 非位图不经过图像服务，直接回落原始直链
		assert.Equal(t,
			"https://cdn.example.com/uploads/docs/report.pdf",
			mgr.Optimized(t.Context(), &models.Media{Disk: "local", FileName: "docs/report.pdf", MimeType: "application/pdf"}),
		)
	})
}

func TestManager_AttachPersistsOriginalOnly(t *testing.T) {
	disk := newTestDisk(t)
	mgr := media.New(config.MediaConfig{MaxFileSize: 10 * 1024 * 1024}, config.StorageConfig{}, disk, nil)

	item, err := mgr.Attach(
		t.Context(),
		media.Model("user", "usr_1"),
		media.CollectionAvatar,
		solidPNG(t, 16, 16, color.RGBA{B: 0xFF, A: 0xFF}),
	)
	require.NoError(t, err)

	// 派生由 imgproxy 实时完成，附件仅落地原图。
	assert.Empty(t, item.GeneratedConversions)
	assert.NotEmpty(t, item.FileName)
	assert.NotEmpty(t, mgr.URL(t.Context(), item))
}
