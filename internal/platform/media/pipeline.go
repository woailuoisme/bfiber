// Package media 提供媒体文件上传、安全校验、命名与分区路径策略、Imgproxy 防篡改签名与多态实体附件管理。
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/samber/oops"
	"go.uber.org/zap"

	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
	"bfiber/internal/platform/storage"
)

// defaultOctetStreamMime 默认通用二进制流 MIME 类型。
const defaultOctetStreamMime = "application/octet-stream"

// WithNamingStrategy 自定义文件命名生成策略。
func WithNamingStrategy(naming NamingStrategy) PipelineOption {
	return func(p *PipelineImpl) {
		if naming != nil {
			p.naming = naming
		}
	}
}

// WithHashNaming 快捷配置基于 SHA-256 内容哈希的命名策略。
func WithHashNaming(hashLength ...int) PipelineOption {
	return WithNamingStrategy(NewHashNamingStrategy(hashLength...))
}

// WithUUIDNaming 快捷配置基于单调递增 UUIDv7 的文件名生成策略。
func WithUUIDNaming() PipelineOption {
	return WithNamingStrategy(NewUUIDNamingStrategy())
}

// WithPathStrategy 自定义存储分区路径规划策略。
func WithPathStrategy(path PathStrategy) PipelineOption {
	return func(p *PipelineImpl) {
		if path != nil {
			p.path = path
		}
	}
}

// WithCategoryDatePath 快捷配置按业务类别与日期分区的路径策略（uploads/{category}/{YYYYMMDD}/{filename}）。
func WithCategoryDatePath(basePrefix ...string) PipelineOption {
	return WithPathStrategy(NewCategoryDatePathStrategy(basePrefix...))
}

// WithDatePath 快捷配置按日期分区的相对存储路径策略（uploads/{YYYYMMDD}/{filename}）。
func WithDatePath(basePrefix ...string) PipelineOption {
	return WithPathStrategy(NewDatePathStrategy(basePrefix...))
}

// WithValidator 自定义上传文件校验器。
func WithValidator(validator Validator) PipelineOption {
	return func(p *PipelineImpl) {
		if validator != nil {
			p.validator = validator
		}
	}
}

// WithImageProcessor 注入图像处理器。
func WithImageProcessor(proc Client) PipelineOption {
	return func(p *PipelineImpl) {
		p.imageProcessor = proc
	}
}

// WithMaxBatchFiles 自定义单次批量上传的文件数上限；limit <= 0 时回退默认值。
func WithMaxBatchFiles(limit int) PipelineOption {
	return func(p *PipelineImpl) {
		if limit > 0 {
			p.maxBatchFiles = limit
		}
	}
}

// PipelineImpl 整合可插拔命名、路径分区、安全校验、底层存储与图像签名。
type PipelineImpl struct {
	disk           storage.Disk
	naming         NamingStrategy
	path           PathStrategy
	validator      Validator
	imageProcessor Client
	maxBatchFiles  int
}

// NewPipeline 构建并初始化带有工业级默认配置的媒体管道实例。
// 未经 WithImageProcessor 注入处理器时上传流程会跳过图片合法性校验，生产装配始终注入。
func NewPipeline(disk storage.Disk, opts ...PipelineOption) *PipelineImpl {
	p := &PipelineImpl{
		disk:          disk,
		naming:        NewHashNamingStrategy(16),
		path:          NewCategoryDatePathStrategy("uploads"),
		maxBatchFiles: defaultMaxBatchFiles,
		validator:     NewValidator(),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Upload 校验并存储单个上传源，利用缓冲区池降低内存分配。
func (p *PipelineImpl) Upload(ctx context.Context, src Source, category string) (*FileInfo, error) {
	if src == nil {
		return nil, errs.BadRequest("uploaded file is nil")
	}

	// 前置尺寸拒绝：上传源已声明长度时先行拦截，
	// 避免超大文件被整读进内存后才在校验阶段失败。
	if sizeValidator, ok := p.validator.(SizeValidator); ok && src.Size() > 0 {
		if err := sizeValidator.ValidateSize(src.Size()); err != nil {
			return nil, oops.Code(errs.CodeBadRequest).In("media.Upload.PrecheckSize").Wrap(err)
		}
	}

	buf, err := readSource(src)
	if err != nil {
		return nil, oops.Code(errs.CodeBadRequest).In("media.Upload").Wrap(err)
	}
	defer putBuffer(buf)

	content := buf.Bytes()
	mimeType := detectMimeType(src.FileName(), content)
	headerMime := src.ContentType()
	if mimeType == defaultOctetStreamMime && headerMime != "" && headerMime != defaultOctetStreamMime {
		mimeType = headerMime
	}

	return p.uploadBytes(ctx, content, src.FileName(), mimeType, category)
}

// defaultMaxBatchFiles 单次批量上传的默认文件数上限：每个源都会整读进内存，必须封顶避免打爆内存。
const defaultMaxBatchFiles = 20

// maxBatchLimit 返回当前生效的单次批量上传文件数上限。
func (p *PipelineImpl) maxBatchLimit() int {
	if p.maxBatchFiles <= 0 {
		return defaultMaxBatchFiles
	}
	return p.maxBatchFiles
}

// UploadMultiple 批量校验并存储多个上传源；任一失败即回滚本次已上传的物理文件。
// 注意回滚是尽力而为：物理删除无法参与事务，失败只记日志，不掩盖原始错误。
func (p *PipelineImpl) UploadMultiple(ctx context.Context, sources []Source, category string) ([]*FileInfo, error) {
	if len(sources) == 0 {
		return nil, errs.BadRequest("no files provided for upload")
	}
	if limit := p.maxBatchLimit(); len(sources) > limit {
		return nil, errs.BadRequest(fmt.Sprintf("too many files in one batch: %d (max %d)", len(sources), limit))
	}

	results := make([]*FileInfo, 0, len(sources))
	for _, src := range sources {
		info, err := p.Upload(ctx, src, category)
		if err != nil {
			discardUploads(ctx, p, results)
			return nil, err
		}
		results = append(results, info)
	}

	return results, nil
}

// discardUploads 尽力删除已落盘的物理文件，用于批量上传或落库失败后的清理。
// 只删本次写入新建的对象：内容哈希命名下，同名文件可能是先前上传留下的，删掉会连累已有记录。
func discardUploads(ctx context.Context, pipeline *PipelineImpl, infos []*FileInfo) {
	for _, info := range infos {
		if info == nil || info.Path == "" || !info.created {
			continue
		}
		if err := pipeline.Delete(ctx, info.Path); err != nil {
			logger.FromContext(ctx).Warn("failed to discard uploaded media file",
				zap.Error(err),
				zap.String("path", info.Path),
			)
		}
	}
}

// UploadBytes 将原生字节切片进行安全校验与哈希命名后上传至存储驱动。
func (p *PipelineImpl) UploadBytes(
	ctx context.Context,
	content []byte,
	originalName, category string,
) (*FileInfo, error) {
	if len(content) == 0 {
		return nil, errs.BadRequest("uploaded file content cannot be empty")
	}

	mimeType := detectMimeType(originalName, content)
	return p.uploadBytes(ctx, content, originalName, mimeType, category)
}

// UploadReader 流式校验并写入任意 io.Reader 数据流至底层存储磁盘。
func (p *PipelineImpl) UploadReader(
	ctx context.Context,
	reader io.Reader,
	originalName string,
	size int64,
	mimeType string,
	category string,
) (*FileInfo, error) {
	if reader == nil {
		return nil, errs.BadRequest("reader cannot be nil")
	}

	maxSize := size
	if fv, ok := p.validator.(*FileValidator); ok && fv != nil && fv.MaxFileSize > 0 {
		if maxSize <= 0 || maxSize > fv.MaxFileSize {
			maxSize = fv.MaxFileSize
		}
	}

	limitedReader := reader
	if maxSize > 0 {
		limitedReader = io.LimitReader(reader, maxSize+1)
	}

	buf := getBuffer()
	defer putBuffer(buf)

	if _, err := io.Copy(buf, limitedReader); err != nil {
		return nil, oops.Code(errs.CodeInternal).In("media.UploadReader").Wrap(err)
	}

	content := buf.Bytes()
	if maxSize > 0 && int64(len(content)) > maxSize {
		return nil, errs.BadRequest(fmt.Sprintf("uploaded content exceeds maximum allowed size of %d bytes", maxSize))
	}

	if strings.TrimSpace(mimeType) == "" {
		mimeType = detectMimeType(originalName, content)
	}

	return p.uploadBytes(ctx, content, originalName, mimeType, category)
}

// Delete 根据相对存储路径删除文件。
func (p *PipelineImpl) Delete(ctx context.Context, filePath string) error {
	if p.disk == nil {
		return errs.Internal(errors.New("storage disk unavailable"))
	}

	return p.disk.Delete(ctx, filePath)
}

// URL 获取指定文件的公开访问 URL。
func (p *PipelineImpl) URL(filePath string) string {
	if p.disk == nil {
		return ""
	}

	// 未配置公开访问地址时 disk.URL 会报错，此处按无链接处理。
	url, _ := p.disk.URL(filePath)
	return url
}

// uploadBytes 是上传字节切片的内部核心流程，包含安全校验、图片合法性校验、策略命名、路径生成与磁盘写入。
func (p *PipelineImpl) uploadBytes(
	ctx context.Context,
	content []byte,
	originalName string,
	mimeType string,
	category string,
) (*FileInfo, error) {
	size := int64(len(content))

	if p.validator != nil {
		if err := p.validator.Validate(ctx, originalName, size, mimeType); err != nil {
			return nil, oops.Code(errs.CodeBadRequest).In("media.Validate").Wrap(err)
		}
	}

	if p.imageProcessor != nil && strings.HasPrefix(mimeType, "image/") && mimeType != "image/svg+xml" {
		if _, inspectErr := p.imageProcessor.Inspect(content); inspectErr != nil {
			return nil, oops.Code(errs.CodeBadRequest).In("media.InspectImage").Wrap(inspectErr)
		}
	}

	fileName, hash := p.naming.GenerateName(ctx, originalName, content)
	storagePath := p.path.GeneratePath(ctx, category, fileName)

	if p.disk == nil {
		return nil, errs.Internal(errors.New("storage disk unavailable"))
	}

	// 内容哈希命名下相同内容会命中同一路径：先探测是否已存在，
	// 使调用方能够区分「本次新建」与「复用了既有对象」，回滚时只删前者。
	existed, existsErr := p.disk.Exists(ctx, storagePath)
	if existsErr != nil {
		// 无法确认时按「已存在」处理：宁可回滚时不删，也不误删他人文件。
		existed = true
		logger.FromContext(ctx).Warn("failed to check media object existence",
			zap.Error(existsErr),
			zap.String("path", storagePath),
		)
	}

	if err := p.disk.Put(ctx, storagePath, content); err != nil {
		return nil, oops.Code(errs.CodeInternal).In("media.Put").Wrap(err)
	}

	// 未配置公开访问地址时 disk.URL 会报错，此处按无链接处理。
	url, _ := p.disk.URL(storagePath)

	return &FileInfo{
		Path:         storagePath,
		URL:          url,
		FileName:     fileName,
		OriginalName: originalName,
		MimeType:     mimeType,
		Size:         size,
		Hash:         hash,
		UploadedAt:   time.Now().UTC(),
		created:      !existed,
	}, nil
}

// detectMimeType 结合魔数头嗅探与扩展名推导文件的真实 MIME 类型。
func detectMimeType(filename string, content []byte) string {
	if len(content) > 0 {
		if detected := mimetype.Detect(content).String(); detected != "" && detected != defaultOctetStreamMime {
			return detected
		}
	}

	if ext := filepath.Ext(filename); ext != "" {
		if byExt := mime.TypeByExtension(ext); byExt != "" {
			return byExt
		}
	}

	return defaultOctetStreamMime
}

// bufferPool 提供高并发流式上传场景下的字节缓冲区复用池，减轻 GC 压力。
var bufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 64*1024))
	},
}

// getBuffer 从缓冲区池中获取并重置字节缓冲区。
func getBuffer() *bytes.Buffer {
	if buf, ok := bufferPool.Get().(*bytes.Buffer); ok {
		buf.Reset()
		return buf
	}
	return bytes.NewBuffer(make([]byte, 0, 64*1024))
}

// putBuffer 将小于容量上限（4MB）的缓冲区重置后归还至池中。
func putBuffer(buf *bytes.Buffer) {
	if buf != nil && buf.Cap() <= 4*1024*1024 {
		buf.Reset()
		bufferPool.Put(buf)
	}
}

// readSource 从上传源打开并读取文件内容至临时缓冲池对象。
// 调用方使用完毕后必须负责通过 putBuffer(buf) 归还缓冲池。
func readSource(src Source) (*bytes.Buffer, error) {
	reader, err := src.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	buf := getBuffer()
	if _, err := io.Copy(buf, reader); err != nil {
		putBuffer(buf)
		return nil, err
	}
	return buf, nil
}
