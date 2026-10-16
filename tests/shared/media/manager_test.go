// Package media_test 针对媒体实体管理门面 Manager 进行契约测试。
package media_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/storage"
	"bfiber/internal/shared/media"
)

func TestManager_ContractsAndURLDerivation(t *testing.T) {
	rootDir := t.TempDir()
	baseURL := "https://cdn.example.com/uploads"

	disk, err := storage.NewLocalDisk(rootDir, baseURL)
	require.NoError(t, err)

	mediaCfg := config.MediaConfig{
		MaxFileSize: 10 * 1024 * 1024,
		Imgproxy: config.ImgproxyConfig{
			BaseURL:  "https://img.example.com",
			Insecure: true,
		},
	}
	storageCfg := config.StorageConfig{}

	// 传入 nil DB 进行上层无状态与契约测试
	mgr := media.New(mediaCfg, storageCfg, disk, nil)
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
		originalURL := mgr.URL(item)
		assert.Equal(t, "https://cdn.example.com/uploads/avatars/2026/09/28/sample.png", originalURL)

		// 2. 头像通过 imgproxy 生成带重心定位的 URL
		avatarURL := mgr.Avatar(item, 80)
		assert.True(t, strings.HasPrefix(avatarURL, "https://img.example.com/insecure/"))
		assert.Contains(t, avatarURL, "/rs:fill:80:80:")
		assert.Contains(t, avatarURL, "/g:fp:")

		// 3. 空模型保护
		assert.Empty(t, mgr.URL(nil))
		assert.Empty(t, mgr.Avatar(nil, 100))
	})
}
