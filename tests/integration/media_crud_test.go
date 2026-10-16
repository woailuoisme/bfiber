//go:build integration

package integration_test

import (
	"bytes"
	"errors"
	stdImage "image"
	"image/png"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/platform/media"
	"bfiber/internal/platform/storage"
)

// newMediaManager 用真实数据库与临时本地磁盘装配媒体门面，并一并返回磁盘以便断言物理文件。
func newMediaManager(t *testing.T) (*media.Manager, storage.Disk) {
	t.Helper()

	disk, err := storage.NewLocalDisk(t.TempDir(), "https://cdn.example.com/uploads")
	require.NoError(t, err)

	manager := media.New(
		config.MediaConfig{MaxFileSize: 10 * 1024 * 1024, DefaultCollection: "default"},
		config.StorageConfig{},
		disk,
		openTestDB(t),
	)

	return manager, disk
}

// samplePNG 生成一张 8×8 的合法 PNG 字节，避免引入二进制夹具。
func samplePNG(t *testing.T) []byte {
	t.Helper()

	return samplePNGWithSize(t, 8)
}

// samplePNGWithSize 生成指定边长的合法 PNG：存储按内容哈希命名，
// 同一批次内需要内容互不相同才能落到不同物理路径。
func samplePNGWithSize(t *testing.T, size int) []byte {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, stdImage.NewRGBA(stdImage.Rect(0, 0, size, size))))
	return buf.Bytes()
}

// TestMediaCRUDLifecycle 针对真实数据库验证媒体附件的增、查、改、删全生命周期。
func TestMediaCRUDLifecycle(t *testing.T) {
	manager, disk := newMediaManager(t)
	ctx := t.Context()
	// 每个用例使用独立宿主，避免与其它集成用例互相干扰。
	target := media.Model("user", "it_media_"+uniqueUsername())

	// 增
	created, err := manager.Attach(ctx, target, media.CollectionAvatar, samplePNG(t), media.WithName("初始名称"))
	require.NoError(t, err, "Attach 失败")
	require.NotZero(t, created.ID)
	assert.Equal(t, "初始名称", created.Name)
	assert.Equal(t, string(media.CollectionAvatar), created.CollectionName)

	// 查：All 与 First 命中同一条记录
	all, err := manager.All(ctx, target, media.CollectionAvatar)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, created.ID, all[0].ID)

	first, err := manager.First(ctx, target, media.CollectionAvatar)
	require.NoError(t, err)
	assert.Equal(t, created.ID, first.ID)

	// 查：集合内无记录时返回哨兵错误
	_, err = manager.First(ctx, media.Model("user", "it_absent_"+uniqueUsername()), media.CollectionAvatar)
	require.ErrorIs(t, err, media.ErrMediaNotFound)

	// 改：仅写入显式提供的字段（含 jsonb 列），其余列保持不变
	renamed, err := manager.Update(ctx, first,
		media.WithName("新名称"),
		media.WithOrderColumn(7),
		media.WithCustomProperties(map[string]any{"source": "it"}),
	)
	require.NoError(t, err)
	assert.Equal(t, "新名称", renamed.Name)
	require.NotNil(t, renamed.OrderColumn)
	assert.Equal(t, 7, *renamed.OrderColumn)
	assert.Equal(t, created.FileName, renamed.FileName, "未提供的列不应被改动")
	assert.Equal(t, created.MimeType, renamed.MimeType)

	// 改：真库回读，确认 UPDATE 确实落库（含 jsonb 列的编码往返）
	persisted, err := manager.First(ctx, target, media.CollectionAvatar)
	require.NoError(t, err)
	assert.Equal(t, "新名称", persisted.Name)
	require.NotNil(t, persisted.OrderColumn)
	assert.Equal(t, 7, *persisted.OrderColumn)
	assert.Equal(t, "it", persisted.CustomProperties["source"])
	assert.Equal(t, created.FileName, persisted.FileName, "未提供的列不得被覆盖")

	// 改：未提供任何可更新字段时拒绝
	_, err = manager.Update(ctx, renamed)
	require.Error(t, err)

	// 删：硬删除记录，并在事务提交后清理物理文件
	fileName := renamed.FileName
	stale := *renamed
	require.NoError(t, manager.Delete(ctx, renamed))

	// 改：记录已不存在（陈旧对象）时必须返回哨兵错误，而不是静默成功
	_, err = manager.Update(ctx, &stale, media.WithName("resurrect"))
	require.ErrorIs(t, err, media.ErrMediaNotFound)

	// 删：重复删除按幂等处理
	require.NoError(t, manager.Delete(ctx, renamed), "重复删除应幂等成功")

	_, err = manager.First(ctx, target, media.CollectionAvatar)
	require.ErrorIs(t, err, media.ErrMediaNotFound)

	exists, err := disk.Exists(ctx, fileName)
	require.NoError(t, err)
	assert.False(t, exists, "物理文件应在事务提交后被删除")
}

// TestMediaAllOrdersByColumn 验证 All 的排序契约：按排序号升序、未设排序号的排在最后。
func TestMediaAllOrdersByColumn(t *testing.T) {
	manager, _ := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_order_"+uniqueUsername())
	gallery := media.Collection("gallery")

	// 先建一条未设排序号的记录，再建 order_column=2 与 1，故意乱序插入。
	unordered, err := manager.Attach(ctx, target, gallery, samplePNG(t))
	require.NoError(t, err)

	second, err := manager.Attach(ctx, target, gallery, samplePNG(t), media.WithOrderColumn(2))
	require.NoError(t, err)

	first, err := manager.Attach(ctx, target, gallery, samplePNG(t), media.WithOrderColumn(1))
	require.NoError(t, err)

	all, err := manager.All(ctx, target, gallery)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, []int64{first.ID, second.ID, unordered.ID},
		[]int64{all[0].ID, all[1].ID, all[2].ID},
		"排序应为：排序号升序在前，未设排序号的最后")

	// First 跟随同一排序契约，应命中排序号最小的一条。
	head, err := manager.First(ctx, target, gallery)
	require.NoError(t, err)
	assert.Equal(t, first.ID, head.ID)

	for _, item := range all {
		require.NoError(t, manager.Delete(ctx, item))
	}
}

// TestMediaAttachSingleFileReplacesPrevious 验证单文件集合的替换语义在真实数据库下的记录收敛。
func TestMediaAttachSingleFileReplacesPrevious(t *testing.T) {
	manager, disk := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_single_"+uniqueUsername())

	// 两次上传必须使用不同内容：内容哈希命名下相同内容会让新记录复用旧路径，
	// 此时被替换的文件仍在被新记录引用，按设计不得清理（该场景由
	// TestMediaSingleFileReplaceSameContentKeepsFile 专门覆盖）。
	previous, err := manager.Attach(ctx, target, media.CollectionAvatar,
		pngSource{name: "old.png", content: samplePNGWithSize(t, 30)}, media.WithSingleFile())
	require.NoError(t, err)

	_, err = manager.Attach(ctx, target, media.CollectionAvatar,
		pngSource{name: "new.png", content: samplePNGWithSize(t, 31)}, media.WithSingleFile())
	require.NoError(t, err)

	all, err := manager.All(ctx, target, media.CollectionAvatar)
	require.NoError(t, err)
	assert.Len(t, all, 1, "单文件集合替换后只应保留一条记录")

	exists, err := disk.Exists(ctx, previous.FileName)
	require.NoError(t, err)
	assert.False(t, exists, "被替换记录的物理文件应在提交后被清理")

	// 收尾清理，避免在共享库中留下测试记录。
	require.NoError(t, manager.Delete(ctx, all[0]))
}

// pngSource 将内存 PNG 字节包装为 media.Source，openErr 非空时用于模拟读取失败。
type pngSource struct {
	name    string
	content []byte
	openErr error
}

// FileName 返回上传源文件名。
func (s pngSource) FileName() string { return s.name }

// Size 返回上传源字节数。
func (s pngSource) Size() int64 { return int64(len(s.content)) }

// ContentType 返回上传源声明的 MIME 类型。
func (s pngSource) ContentType() string { return "image/png" }

// Open 打开上传源。
func (s pngSource) Open() (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

// listFilesIn 递归列出目录下的全部文件，用于断言存储清理是否彻底。
func listFilesIn(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	}))
	return files
}

// TestMediaAttachManyLifecycle 针对真实数据库验证批量上传：逐条建记录、编号自增、追加接续。
func TestMediaAttachManyLifecycle(t *testing.T) {
	manager, _ := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_many_"+uniqueUsername())
	gallery := media.Collection("gallery")

	created, err := manager.AttachMany(ctx, target, gallery, []media.Source{
		pngSource{name: "1.png", content: samplePNGWithSize(t, 8)},
		pngSource{name: "2.png", content: samplePNGWithSize(t, 9)},
		pngSource{name: "3.png", content: samplePNGWithSize(t, 10)},
	})
	require.NoError(t, err)
	require.Len(t, created, 3)
	for index, record := range created {
		assert.NotZero(t, record.ID, "批量插入应回填主键")
		require.NotNil(t, record.OrderColumn)
		assert.Equal(t, index, *record.OrderColumn)
		assert.Equal(t, string(gallery), record.CollectionName)
	}

	all, err := manager.All(ctx, target, gallery)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, []string{"1.png", "2.png", "3.png"},
		[]string{all[0].Name, all[1].Name, all[2].Name})

	appended, err := manager.AttachMany(ctx, target, gallery, []media.Source{
		pngSource{name: "4.png", content: samplePNGWithSize(t, 11)},
	})
	require.NoError(t, err)
	require.Len(t, appended, 1)
	require.NotNil(t, appended[0].OrderColumn)
	assert.Equal(t, 3, *appended[0].OrderColumn, "追加批次应接续集合内现有最大排序号")

	for _, record := range append(created, appended...) {
		require.NoError(t, manager.Delete(ctx, record))
	}
}

// TestMediaAttachManySingleFileClearsCollection 验证单文件集合下批量替换：清空旧记录并清理其物理文件。
func TestMediaAttachManySingleFileClearsCollection(t *testing.T) {
	manager, disk := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_many_single_"+uniqueUsername())
	gallery := media.Collection("gallery")

	previous, err := manager.AttachMany(ctx, target, gallery, []media.Source{
		pngSource{name: "old1.png", content: samplePNGWithSize(t, 12)},
		pngSource{name: "old2.png", content: samplePNGWithSize(t, 13)},
	})
	require.NoError(t, err)
	require.Len(t, previous, 2)

	replaced, err := manager.AttachMany(ctx, target, gallery, []media.Source{
		pngSource{name: "new1.png", content: samplePNGWithSize(t, 14)},
		pngSource{name: "new2.png", content: samplePNGWithSize(t, 15)},
	}, media.WithSingleFile())
	require.NoError(t, err)
	require.Len(t, replaced, 2)

	all, err := manager.All(ctx, target, gallery)
	require.NoError(t, err)
	require.Len(t, all, 2, "单文件集合应只保留本批次记录")
	assert.Equal(t, []string{"new1.png", "new2.png"}, []string{all[0].Name, all[1].Name})
	for index, record := range all {
		require.NotNil(t, record.OrderColumn)
		assert.Equal(t, index, *record.OrderColumn, "清空后编号应重新从 0 开始")
	}

	for _, record := range previous {
		exists, existsErr := disk.Exists(ctx, record.FileName)
		require.NoError(t, existsErr)
		assert.False(t, exists, "被替换记录的物理文件应在提交后被清理")
	}

	for _, record := range replaced {
		require.NoError(t, manager.Delete(ctx, record))
	}
}

// TestMediaSharedFileDeletion 验证内容相同的多条记录共享物理文件时，删除其中一条不会连累另一条。
func TestMediaSharedFileDeletion(t *testing.T) {
	manager, disk := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_shared_"+uniqueUsername())
	gallery := media.Collection("gallery")

	// 同一份内容上传两次：内容哈希命名使其落到同一物理路径。
	shared := samplePNGWithSize(t, 20)
	first, err := manager.Attach(ctx, target, gallery, pngSource{name: "a.png", content: shared})
	require.NoError(t, err)
	second, err := manager.Attach(ctx, target, gallery, pngSource{name: "b.png", content: shared})
	require.NoError(t, err)
	require.Equal(t, first.FileName, second.FileName, "相同内容应复用同一物理文件")

	require.NoError(t, manager.Delete(ctx, first))
	exists, existsErr := disk.Exists(ctx, first.FileName)
	require.NoError(t, existsErr)
	assert.True(t, exists, "仍有记录引用该文件时不得删除物理文件")

	require.NoError(t, manager.Delete(ctx, second))
	exists, existsErr = disk.Exists(ctx, first.FileName)
	require.NoError(t, existsErr)
	assert.False(t, exists, "最后一条引用记录删除后物理文件应被清理")
}

// TestMediaSingleFileReplaceSameContentKeepsFile 验证单文件集合以相同内容替换时，不会删掉新记录引用的文件。
func TestMediaSingleFileReplaceSameContentKeepsFile(t *testing.T) {
	manager, disk := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_same_content_"+uniqueUsername())
	gallery := media.Collection("gallery")
	source := pngSource{name: "same.png", content: samplePNGWithSize(t, 21)}

	previous, err := manager.Attach(ctx, target, gallery, source, media.WithSingleFile())
	require.NoError(t, err)

	current, err := manager.Attach(ctx, target, gallery, source, media.WithSingleFile())
	require.NoError(t, err)
	require.Equal(t, previous.FileName, current.FileName, "相同内容替换应复用同一物理文件")

	exists, existsErr := disk.Exists(ctx, current.FileName)
	require.NoError(t, existsErr)
	assert.True(t, exists, "替换后的记录仍引用该文件，不得清理")

	require.NoError(t, manager.Delete(ctx, current))
}

// TestMediaAttachLongFileName 验证超长客户端文件名不会以落库失败的形式暴露给上传方。
func TestMediaAttachLongFileName(t *testing.T) {
	manager, _ := newMediaManager(t)
	ctx := t.Context()
	target := media.Model("user", "it_long_name_"+uniqueUsername())
	gallery := media.Collection("gallery")

	longName := strings.Repeat("长", 200) + ".png"
	record, err := manager.Attach(ctx, target, gallery, pngSource{name: longName, content: samplePNGWithSize(t, 22)})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(record.Name), 255, "名称必须截断到列宽上限")
	assert.True(t, utf8.ValidString(record.Name), "截断必须落在 UTF-8 边界上")

	require.NoError(t, manager.Delete(ctx, record))
}

// TestMediaAttachManyRollsBackOnFailure 验证批量上传中途失败：不落任何记录、不留任何物理文件。
func TestMediaAttachManyRollsBackOnFailure(t *testing.T) {
	root := t.TempDir()
	disk, err := storage.NewLocalDisk(root, "https://cdn.example.com/uploads")
	require.NoError(t, err)

	manager := media.New(
		config.MediaConfig{MaxFileSize: 10 * 1024 * 1024, DefaultCollection: "default"},
		config.StorageConfig{},
		disk,
		openTestDB(t),
	)
	ctx := t.Context()
	target := media.Model("user", "it_many_rollback_"+uniqueUsername())
	gallery := media.Collection("gallery")

	records, err := manager.AttachMany(ctx, target, gallery, []media.Source{
		pngSource{name: "ok.png", content: samplePNGWithSize(t, 16)},
		pngSource{name: "broken.png", openErr: errors.New("read failed")},
	})
	require.Error(t, err)
	assert.Nil(t, records)

	all, err := manager.All(ctx, target, gallery)
	require.NoError(t, err)
	assert.Empty(t, all, "失败批次不得写入任何记录")

	assert.Empty(t, listFilesIn(t, root), "失败批次不得在存储上留下文件")
}
