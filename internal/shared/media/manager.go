// Package media 提供媒体文件上传、验证、处理、命名策略、Imgproxy 防篡改签名与多态实体附件管理。
package media

import (
	"cmp"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/samber/oops"
	"github.com/uptrace/bun"
	"go.uber.org/zap"

	"bfiber/internal/config"
	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/logger"
	"bfiber/internal/platform/storage"
)

// ErrMediaNotFound 表示未查询到目标媒体记录的哨兵错误。
var ErrMediaNotFound = errors.New("media: record not found")

// Attachable 描述可绑定多态媒体附件的业务模型契约。
type Attachable interface {
	GetModelType() string
	GetModelID() string
}

// GenericAttachable 简单通用的 Attachable 实现。
type GenericAttachable struct {
	ModelType string
	ModelID   string
}

// GetModelType 返回业务模型类型标识。
func (g GenericAttachable) GetModelType() string { return g.ModelType }

// GetModelID 返回业务模型主键 ID。
func (g GenericAttachable) GetModelID() string { return g.ModelID }

// Model 快捷创建通用多态载体对象。
func Model(modelType, modelID string) Attachable {
	return GenericAttachable{
		ModelType: strings.TrimSpace(modelType),
		ModelID:   strings.TrimSpace(modelID),
	}
}

// ToAttachable 智能解析输入对象为 Attachable 接口实例。
func ToAttachable(v any) Attachable {
	a, _ := v.(Attachable)
	return a
}

// AttachConfig 保存媒体附加的选项参数。
type AttachConfig struct {
	Name             string         // 原始友好名称（若为空则默认取上传文件名）
	SingleFile       bool           // 是否为单文件集合（若为 true，自动清空旧同名集合下的物理文件与数据库记录）
	CustomProperties map[string]any // 自定义扩展业务属性（JSONB）
	Manipulations    map[string]any // 图片处理指令与参数元数据（JSONB）
	OrderColumn      *int           // 多附件排序号
	DiskName         string         // 显式指定的存储磁盘名称
}

// AttachOption 定义附加选项函数。
type AttachOption func(*AttachConfig)

// WithName 设置媒体附件的展示名称。
func WithName(name string) AttachOption {
	return func(c *AttachConfig) {
		c.Name = strings.TrimSpace(name)
	}
}

// WithSingleFile 设置该附件集合为单文件模式（自动覆盖已有旧附件）。
func WithSingleFile() AttachOption {
	return func(c *AttachConfig) {
		c.SingleFile = true
	}
}

// WithCustomProperties 设置自定义业务元数据。
func WithCustomProperties(props map[string]any) AttachOption {
	return func(c *AttachConfig) {
		c.CustomProperties = props
	}
}

// WithManipulations 设置图片衍生与操作指令。
func WithManipulations(manipulations map[string]any) AttachOption {
	return func(c *AttachConfig) {
		c.Manipulations = manipulations
	}
}

// WithOrderColumn 设置多附件排序号。
func WithOrderColumn(order int) AttachOption {
	return func(c *AttachConfig) {
		c.OrderColumn = &order
	}
}

// Manager 为业务层提供多态媒体附件聚合管理门面。
type Manager struct {
	db       bun.IDB
	disk     storage.Disk
	pipeline Pipeline
	image    Client
	cfg      config.MediaConfig
}

// NewManager 构建多态媒体管理门面实例。
// 形参类型即传播契约：句柄是根连接还是活跃事务由调用方决定，
// 门面内每条语句再经 database.ResolveDB(ctx, db) 解析，调用方的事务不会被静默排除。
func NewManager(
	mediaCfg config.MediaConfig,
	disk storage.Disk,
	db bun.IDB,
	pipeline Pipeline,
	image Client,
) *Manager {
	return &Manager{
		db:       db,
		disk:     disk,
		pipeline: pipeline,
		image:    image,
		cfg:      mediaCfg,
	}
}

// Pipeline 返回底层的无状态媒体处理与上传管道。
func (m *Manager) Pipeline() Pipeline {
	return m.pipeline
}

// Image 返回底层 Imgproxy 图像处理器。
func (m *Manager) Image() Client {
	return m.image
}

// Attach 将文件或流上传并关联至指定业务模型的多态集合中。
func (m *Manager) Attach(
	ctx context.Context,
	target any,
	collection string,
	source any,
	opts ...AttachOption,
) (*models.Media, error) {
	attachable := ToAttachable(target)
	if attachable == nil || strings.TrimSpace(attachable.GetModelID()) == "" {
		return nil, oops.Code(errs.CodeBadRequest).Errorf("media: invalid attachable target or empty model id")
	}

	modelType := strings.TrimSpace(attachable.GetModelType())
	modelID := strings.TrimSpace(attachable.GetModelID())
	collection = cmp.Or(strings.TrimSpace(collection), m.cfg.DefaultCollection, "default")

	var conf AttachConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&conf)
		}
	}

	// 1. 上传文件到底层存储并获取元数据
	fileInfo, err := m.uploadSource(ctx, source, collection, conf.DiskName)
	if err != nil {
		return nil, err
	}

	// 2. 单文件模式下旧记录与新记录在同一事务内原子替换。
	// 物理文件的删除无法回滚，因此先只删记录，待事务提交后再删文件。
	friendlyName := cmp.Or(conf.Name, fileInfo.OriginalName, fileInfo.FileName)
	mediaRecord := &models.Media{
		ModelType:        modelType,
		ModelID:          modelID,
		CollectionName:   collection,
		Name:             friendlyName,
		FileName:         fileInfo.Path,
		MimeType:         fileInfo.MimeType,
		Disk:             cmp.Or(conf.DiskName, "default"),
		Size:             fileInfo.Size,
		ConversionStatus: "completed",
		OrderColumn:      conf.OrderColumn,
		CustomProperties: conf.CustomProperties,
		Manipulations:    conf.Manipulations,
	}

	if m.db == nil {
		return mediaRecord, nil
	}

	var replaced []models.Media
	err = database.RunInTx(ctx, m.db, func(txCtx context.Context) error {
		if conf.SingleFile {
			old, loadErr := m.oldMediaInCollection(txCtx, modelType, modelID, collection)
			if loadErr != nil {
				return loadErr
			}
			replaced = old
			if _, delErr := database.ResolveDB(txCtx, m.db).NewDelete().
				Model((*models.Media)(nil)).
				Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, collection).
				Exec(txCtx); delErr != nil {
				return delErr
			}
		}

		_, insertErr := database.ResolveDB(txCtx, m.db).NewInsert().Model(mediaRecord).Exec(txCtx)
		if insertErr != nil {
			return insertErr
		}

		// 物理文件删除无法随事务回滚，只能登记到提交之后。
		// 若本函数运行在调用方持有的事务内，回调会推迟到那次提交成功之后执行；
		// 若在闭包内直接删除，外层事务回滚后旧记录会被恢复，却已指向不存在的文件。
		replacedFiles := replaced
		database.OnCommit(txCtx, func() {
			for _, item := range replacedFiles {
				if delErr := m.disk.Delete(ctx, item.FileName); delErr != nil {
					logger.FromContext(ctx).Warn("failed to delete replaced media file",
						zap.Error(delErr),
						zap.String("path", item.FileName),
					)
				}
			}
		})
		return nil
	})
	if err != nil {
		// 事务已回滚，新上传的物理文件不应留在磁盘上。
		if delErr := m.pipeline.Delete(ctx, fileInfo.Path); delErr != nil {
			logger.FromContext(ctx).Warn("failed to clean up orphaned media file after DB insert failure",
				zap.Error(delErr),
				zap.String("path", fileInfo.Path),
			)
		}
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.Attach").Wrap(err)
	}

	return mediaRecord, nil
}

// uploadSource 内部按源类型调度底层管道上传。
func (m *Manager) uploadSource(ctx context.Context, source any, category, diskName string) (*FileInfo, error) {
	switch s := source.(type) {
	case *multipart.FileHeader:
		return m.pipeline.Upload(ctx, s, category, diskName)
	case []byte:
		return m.pipeline.UploadBytes(ctx, s, "upload.bin", category, diskName)
	case *FileInfo:
		return s, nil
	case FileInfo:
		return &s, nil
	default:
		return nil, oops.Code(errs.CodeBadRequest).Errorf("media: unsupported upload source type: %T", source)
	}
}

// oldMediaInCollection 查询指定模型集合下现存的附件记录。
// 只读不删：记录删除由调用方在事务内完成，物理文件在事务提交后删除。
func (m *Manager) oldMediaInCollection(ctx context.Context, modelType, modelID, collection string) ([]models.Media, error) {
	var oldMedia []models.Media
	if err := database.ResolveDB(ctx, m.db).NewSelect().
		Model(&oldMedia).
		Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, collection).
		Scan(ctx); err != nil {
		return nil, err
	}
	return oldMedia, nil
}

// URL 智能生成媒体文件的访问 URL。
// 如果指定了图像处理选项且配置了 Imgproxy，则生成 Imgproxy 防篡改签名 URL；否则返回 Storage Disk 原始/预签名 URL。
func (m *Manager) URL(mediaItem *models.Media, opts ...ProcessOption) string {
	if mediaItem == nil || mediaItem.FileName == "" {
		return ""
	}

	isImage := strings.HasPrefix(mediaItem.MimeType, "image/") && mediaItem.MimeType != "image/svg+xml"
	if len(opts) > 0 && isImage && m.image != nil {
		imgURL, err := m.image.URL(mediaItem.FileName, opts...)
		if err == nil && imgURL != "" {
			return imgURL
		}
	}

	url, _ := m.disk.URL(mediaItem.FileName)
	return url
}

// Avatar 快捷获取多态媒体文件的头像裁切 URL。
func (m *Manager) Avatar(mediaItem *models.Media, size int) string {
	return m.URL(mediaItem,
		WithResizingType(ResizeFill),
		WithResize(size, size),
		WithGravity(GravityFocusPoint),
	)
}

const maxAvatarSize = 2 * 1024 * 1024 // 2MB

// ValidateAvatarFile 校验头像文件的大小、扩展名与真实魔数。
func ValidateAvatarFile(file *multipart.FileHeader) error {
	if file == nil || file.Size <= 0 {
		return errs.BadRequest("Avatar file cannot be empty")
	}
	if file.Size > maxAvatarSize {
		return errs.BadRequest("Avatar file size must not exceed 2MB")
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(file.Filename), "."))
	switch ext {
	case "jpg", "jpeg", "png", "webp":
	default:
		return errs.BadRequest("Invalid image extension; only jpg, jpeg, png, and webp are allowed")
	}

	src, err := file.Open()
	if err != nil {
		return errs.BadRequest("Failed to open uploaded file")
	}
	defer func() { _ = src.Close() }()

	header := make([]byte, 512)
	n, err := io.ReadFull(src, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return errs.BadRequest("Failed to read uploaded file")
	}

	mtype := mimetype.Detect(header[:n])
	switch mtype.String() {
	case "image/jpeg", "image/png", "image/webp":
		return nil
	default:
		return errs.BadRequest("Invalid image format; only JPEG, PNG, and WebP are allowed")
	}
}

// AttachAvatar 校验头像文件规格（魔数嗅探、格式白名单、尺寸限制），
// 将其原子上传并以单文件模式绑定至用户模型，返回解析后的签名访问 URL。
func (m *Manager) AttachAvatar(ctx context.Context, userID string, file *multipart.FileHeader) (string, error) {
	if err := ValidateAvatarFile(file); err != nil {
		return "", err
	}
	if m == nil {
		return "", oops.Code(errs.CodeInternal).Errorf("media: manager is not configured")
	}

	mediaRecord, err := m.Attach(
		ctx,
		Model("user", userID),
		"avatar",
		file,
		WithSingleFile(),
	)
	if err != nil {
		return "", err
	}

	return cmp.Or(m.Avatar(mediaRecord, 256), m.URL(mediaRecord), mediaRecord.FileName), nil
}
