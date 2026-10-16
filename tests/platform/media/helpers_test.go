package media_test

import (
	"bytes"
	stdImage "image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"bfiber/internal/platform/storage"
)

// newTestDisk 构造指向临时目录的本地磁盘。
func newTestDisk(t *testing.T) storage.Disk {
	t.Helper()

	disk, err := storage.NewLocalDisk(t.TempDir(), "https://cdn.example.com/uploads")
	require.NoError(t, err)
	return disk
}

// solidPNG 生成纯色 PNG 字节。
func solidPNG(t *testing.T, width, height int, fill color.Color) []byte {
	t.Helper()

	img := stdImage.NewRGBA(stdImage.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, fill)
		}
	}

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// stubSource 是可注入读取失败的测试上传源。
type stubSource struct {
	name    string
	content []byte
	openErr error
}

// FileName 返回上传源文件名。
func (s stubSource) FileName() string { return s.name }

// Size 返回上传源字节数。
func (s stubSource) Size() int64 { return int64(len(s.content)) }

// ContentType 返回上传源声明的 MIME 类型。
func (s stubSource) ContentType() string { return "image/png" }

// Open 打开上传源；openErr 非空时用于模拟读取失败。
func (s stubSource) Open() (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

// mustExist 断言存储对象存在并返回其存在性，便于直接用于 require/assert。
func mustExist(t *testing.T, disk storage.Disk, path string) bool {
	t.Helper()

	exists, err := disk.Exists(t.Context(), path)
	require.NoError(t, err)
	return exists
}

// listFiles 递归列出目录下的全部文件路径，用于断言存储清理是否彻底。
func listFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}
