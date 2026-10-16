package media_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/media"
)

// TestManager_CRUDGuards 覆盖增删改查入口在无数据库与非法入参时的守卫分支。
// 断言到文案级别：只断言「有错误」的话，把 400 误写成 500 这类回归不会被发现。
// 真实数据库下的全生命周期见 tests/integration/media_crud_test.go。
func TestManager_CRUDGuards(t *testing.T) {
	mgr := media.New(config.MediaConfig{MaxFileSize: 10 * 1024 * 1024}, config.StorageConfig{}, newTestDisk(t), nil)
	target := media.Model("user", "usr_guard")

	t.Run("All 拒绝非法宿主", func(t *testing.T) {
		_, err := mgr.All(t.Context(), nil, media.CollectionAvatar)
		require.ErrorContains(t, err, "invalid attachable target")
	})

	t.Run("First 拒绝空主键宿主", func(t *testing.T) {
		_, err := mgr.First(t.Context(), media.Model("user", ""), media.CollectionAvatar)
		require.ErrorContains(t, err, "invalid attachable target")
	})

	t.Run("All 在无数据库时拒绝", func(t *testing.T) {
		_, err := mgr.All(t.Context(), target, media.CollectionAvatar)
		require.ErrorContains(t, err, "database is not configured")
	})

	t.Run("First 在无数据库时拒绝", func(t *testing.T) {
		_, err := mgr.First(t.Context(), target, media.CollectionAvatar)
		require.ErrorContains(t, err, "database is not configured")
	})

	t.Run("Update 拒绝空记录", func(t *testing.T) {
		_, err := mgr.Update(t.Context(), nil, media.WithName("renamed"))
		require.ErrorContains(t, err, "invalid media record for update")
	})

	t.Run("Update 拒绝零主键记录", func(t *testing.T) {
		_, err := mgr.Update(t.Context(), &models.Media{}, media.WithName("renamed"))
		require.ErrorContains(t, err, "invalid media record for update")
	})

	t.Run("Update 拒绝未提供任何可更新字段", func(t *testing.T) {
		_, err := mgr.Update(t.Context(), &models.Media{ID: 1}, media.WithSingleFile())
		require.ErrorContains(t, err, "no updatable field provided", "WithSingleFile 这类选项不得被静默忽略")
	})

	t.Run("Update 拒绝纯空白名称", func(t *testing.T) {
		_, err := mgr.Update(t.Context(), &models.Media{ID: 1}, media.WithName("   "))
		require.ErrorContains(t, err, "no updatable field provided", "空白名称视为未提供，不得写入空格")
	})

	t.Run("Update 在无数据库时拒绝", func(t *testing.T) {
		_, err := mgr.Update(t.Context(), &models.Media{ID: 1}, media.WithName("renamed"))
		require.ErrorContains(t, err, "database is not configured")
	})

	t.Run("Delete 拒绝空记录", func(t *testing.T) {
		require.ErrorContains(t, mgr.Delete(t.Context(), nil), "invalid media record for delete")
	})

	t.Run("Delete 拒绝零主键记录", func(t *testing.T) {
		require.ErrorContains(t, mgr.Delete(t.Context(), &models.Media{}), "invalid media record for delete")
	})

	t.Run("Delete 在无数据库时拒绝", func(t *testing.T) {
		require.ErrorContains(t, mgr.Delete(t.Context(), &models.Media{ID: 1}), "database is not configured")
	})
}
