package media_test

import (
	"errors"
	"image/color"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/platform/media"
	"bfiber/internal/platform/storage"
)

// TestManager_AttachManyClampsLongName 覆盖超长客户端文件名：必须截断到列宽上限，而不是落库失败。
func TestManager_AttachManyClampsLongName(t *testing.T) {
	mgr := media.New(config.MediaConfig{MaxFileSize: 10 * 1024 * 1024}, config.StorageConfig{}, newTestDisk(t), nil)

	// 200 个多字节字符 = 600 字节，远超 name 列的 255 字节。
	longName := strings.Repeat("长", 200) + ".png"

	records, err := mgr.AttachMany(t.Context(), media.Model("user", "usr_long"), media.Collection("gallery"), []media.Source{
		stubSource{name: longName, content: solidPNG(t, 2, 2, color.White)},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.LessOrEqual(t, len(records[0].Name), 255, "名称必须截断到列宽上限")
	assert.True(t, utf8.ValidString(records[0].Name), "截断必须落在 UTF-8 边界上")
}

// TestPipeline_UploadMultiple 覆盖批量上传的空输入、单批上限、逐条返回与中途失败回滚。
func TestPipeline_UploadMultiple(t *testing.T) {
	root := t.TempDir()
	disk, err := storage.NewLocalDisk(root, "https://cdn.example.com/uploads")
	require.NoError(t, err)

	pipeline := media.NewPipeline(disk, media.WithValidator(media.NewValidator()))

	t.Run("空输入被拒绝", func(t *testing.T) {
		infos, err := pipeline.UploadMultiple(t.Context(), nil, "gallery")
		require.Error(t, err)
		assert.Nil(t, infos)
	})

	t.Run("超过单批上限被拒绝", func(t *testing.T) {
		many := make([]media.Source, 0, 21)
		for range 21 {
			many = append(many, stubSource{name: "a.png", content: solidPNG(t, 1, 1, color.White)})
		}

		infos, err := pipeline.UploadMultiple(t.Context(), many, "gallery")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many files")
		assert.Nil(t, infos)
	})

	t.Run("恰好达到上限时允许", func(t *testing.T) {
		// 与上面的 21 个用例配对，钉住 defaultMaxBatchFiles 的边界（20 通过、21 拒绝）。
		exact := make([]media.Source, 0, 20)
		for range 20 {
			exact = append(exact, stubSource{name: "a.png", content: solidPNG(t, 1, 1, color.White)})
		}

		infos, err := pipeline.UploadMultiple(t.Context(), exact, "gallery")
		require.NoError(t, err)
		assert.Len(t, infos, 20)
	})

	t.Run("成功后逐条返回元数据", func(t *testing.T) {
		infos, err := pipeline.UploadMultiple(t.Context(), []media.Source{
			stubSource{name: "a.png", content: solidPNG(t, 2, 2, color.White)},
			stubSource{name: "b.png", content: solidPNG(t, 3, 3, color.White)},
		}, "gallery")
		require.NoError(t, err)
		require.Len(t, infos, 2)
		assert.NotEqual(t, infos[0].Path, infos[1].Path)
		assert.Equal(t, "a.png", infos[0].OriginalName)
		assert.Equal(t, "b.png", infos[1].OriginalName)
	})

	t.Run("回滚不删除先前上传的相同内容对象", func(t *testing.T) {
		// 内容哈希命名下，相同内容必然命中同一路径：回滚只能删本次新建的对象，
		// 否则会把先前成功上传留下的文件一起删掉，令已有记录指向不存在的文件。
		content := solidPNG(t, 6, 6, color.Black)

		first, err := pipeline.Upload(t.Context(), stubSource{name: "first.png", content: content}, "gallery")
		require.NoError(t, err)
		require.True(t, mustExist(t, disk, first.Path), "首次写入应真的落盘")

		before := len(listFiles(t, root))

		_, err = pipeline.UploadMultiple(t.Context(), []media.Source{
			stubSource{name: "duplicate.png", content: content},
			stubSource{name: "broken.png", openErr: errors.New("read failed")},
		}, "gallery")
		require.Error(t, err)

		assert.Len(t, listFiles(t, root), before, "复用既有对象时回滚不得删除它")
		assert.True(t, mustExist(t, disk, first.Path), "先前上传的文件必须仍然存在")
	})

	t.Run("重复上传相同内容复用同一对象", func(t *testing.T) {
		content := solidPNG(t, 7, 7, color.Black)

		first, err := pipeline.Upload(t.Context(), stubSource{name: "a.png", content: content}, "gallery")
		require.NoError(t, err)

		second, err := pipeline.Upload(t.Context(), stubSource{name: "b.png", content: content}, "gallery")
		require.NoError(t, err)

		// 「复用而非新建」的外部可观测后果：路径相同，且失败回滚不会删掉它（见上一个子用例）。
		assert.Equal(t, first.Path, second.Path, "相同内容应复用同一物理对象")
		assert.True(t, mustExist(t, disk, second.Path))
	})

	t.Run("中途失败回滚已上传文件", func(t *testing.T) {
		before := len(listFiles(t, root))

		// 内容必须与其它子用例不同：存储按内容哈希命名，内容相同会命中同一路径，
		// 回滚于是会删掉先前上传留下的同名文件，让计数断言失真。
		infos, err := pipeline.UploadMultiple(t.Context(), []media.Source{
			stubSource{name: "ok.png", content: solidPNG(t, 5, 5, color.Black)},
			stubSource{name: "broken.png", openErr: errors.New("read failed")},
		}, "gallery")
		require.Error(t, err)
		assert.Nil(t, infos)
		assert.Len(t, listFiles(t, root), before, "失败批次不得在存储上留下任何文件")
	})
}

// TestManager_AttachManyGuards 覆盖批量关联的入参守卫，以及未装配数据库时的编号行为。
func TestManager_AttachManyGuards(t *testing.T) {
	mgr := media.New(config.MediaConfig{MaxFileSize: 10 * 1024 * 1024}, config.StorageConfig{}, newTestDisk(t), nil)
	target := media.Model("user", "usr_many")
	gallery := media.Collection("gallery")

	t.Run("空输入被拒绝", func(t *testing.T) {
		_, err := mgr.AttachMany(t.Context(), target, gallery, nil)
		require.Error(t, err)
	})

	t.Run("非法宿主被拒绝", func(t *testing.T) {
		_, err := mgr.AttachMany(t.Context(), nil, gallery, []media.Source{
			stubSource{name: "a.png", content: solidPNG(t, 2, 2, color.White)},
		})
		require.Error(t, err)
	})

	t.Run("显式名称的首尾空白被裁剪", func(t *testing.T) {
		records, err := mgr.AttachMany(t.Context(), target, gallery, []media.Source{
			stubSource{name: "trim.png", content: solidPNG(t, 8, 8, color.White)},
		}, media.WithName("  旅行照片  "))
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "旅行照片", records[0].Name, "与 Update 侧保持同一套归一化")
	})

	t.Run("纯空白名称回落为原文件名", func(t *testing.T) {
		records, err := mgr.AttachMany(t.Context(), target, gallery, []media.Source{
			stubSource{name: "blank.png", content: solidPNG(t, 9, 9, color.White)},
		}, media.WithName("   "))
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "blank.png", records[0].Name, "空白名称不等于有效名称，应回落到上传文件名")
	})

	t.Run("未装配数据库时按序编号并返回记录", func(t *testing.T) {
		// 两条源内容不同：存储按内容哈希命名，相同内容会落到同一物理路径。
		records, err := mgr.AttachMany(t.Context(), target, gallery, []media.Source{
			stubSource{name: "a.png", content: solidPNG(t, 2, 2, color.White)},
			stubSource{name: "b.png", content: solidPNG(t, 3, 3, color.White)},
		})
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.Equal(t, "a.png", records[0].Name)
		assert.Equal(t, "b.png", records[1].Name)
		require.NotNil(t, records[0].OrderColumn)
		require.NotNil(t, records[1].OrderColumn)
		assert.Equal(t, 0, *records[0].OrderColumn)
		assert.Equal(t, 1, *records[1].OrderColumn)
	})

	t.Run("显式起始编号生效", func(t *testing.T) {
		records, err := mgr.AttachMany(t.Context(), target, gallery, []media.Source{
			stubSource{name: "c.png", content: solidPNG(t, 4, 4, color.White)},
			stubSource{name: "d.png", content: solidPNG(t, 6, 6, color.White)},
		}, media.WithOrderColumn(5))
		require.NoError(t, err)
		require.Len(t, records, 2)
		require.NotNil(t, records[0].OrderColumn)
		require.NotNil(t, records[1].OrderColumn)
		assert.Equal(t, 5, *records[0].OrderColumn)
		assert.Equal(t, 6, *records[1].OrderColumn)
	})

	t.Run("中途失败时不产生记录且不留文件", func(t *testing.T) {
		records, err := mgr.AttachMany(t.Context(), target, gallery, []media.Source{
			stubSource{name: "ok.png", content: solidPNG(t, 7, 7, color.White)},
			stubSource{name: "broken.png", openErr: errors.New("read failed")},
		})
		require.Error(t, err)
		assert.Nil(t, records)
	})
}
