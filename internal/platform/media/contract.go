// Package media 提供媒体文件上传、安全校验、命名与分区路径策略、Imgproxy 防篡改签名与多态实体附件管理。
package media

import (
	"context"
	"io"
	"time"
)

// Source 描述一次上传的输入源，与传输协议解耦。
// 注意：*multipart.FileHeader 不满足本契约（其 Open 返回 multipart.File），须由传输层适配。
type Source interface {
	FileName() string
	Size() int64
	ContentType() string
	Open() (io.ReadCloser, error)
}

// FileInfo 封装文件上传成功后的完整元数据描述。
type FileInfo struct {
	Path         string    `json:"path"`
	URL          string    `json:"url"`
	FileName     string    `json:"fileName"`
	OriginalName string    `json:"originalName"`
	MimeType     string    `json:"mimeType"`
	Size         int64     `json:"size"`
	Hash         string    `json:"hash"`
	UploadedAt   time.Time `json:"uploadedAt"`

	// created 报告本次写入是否新建了对象；内容相同且对象已存在时为 false。
	// 仅供回滚清理判定「该不该删这个文件」，不对外暴露。
	created bool
}

// NamingStrategy 定义根据文件内容与原始文件名生成安全目标文件名与内容哈希的策略契约。
type NamingStrategy interface {
	GenerateName(ctx context.Context, originalName string, content []byte) (fileName, hash string)
}

// PathStrategy 定义将文件分散存储到分区目录（如按业务类别与日期）的路径规划契约。
type PathStrategy interface {
	GeneratePath(ctx context.Context, category, fileName string) string
}

// Validator 定义文件安全性、大小上限及 MIME 格式兼容性的校验契约。
type Validator interface {
	Validate(ctx context.Context, originalName string, size int64, mimeType string) error
}

// SizeValidator 是可选能力：实现它的 Validator 允许调用方在读取文件内容之前，
// 先按上传源声明的长度拒绝超大文件，避免整读进内存后才失败。
// 未实现该接口的校验器只是失去这层前置优化，完整校验流程不受影响。
type SizeValidator interface {
	ValidateSize(size int64) error
}

// PipelineOption 定义用于定制 Pipeline 的函数式选项。
type PipelineOption func(*PipelineImpl)
