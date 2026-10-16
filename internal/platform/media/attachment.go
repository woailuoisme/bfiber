// Package media 提供媒体文件上传、安全校验、命名与分区路径策略、Imgproxy 防篡改签名与多态实体附件管理。
package media

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"

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

// Collection 标识媒体附件的逻辑集合，避免裸字符串在调用点扩散。
type Collection string

const (
	// CollectionDefault 是未显式指定集合时的默认集合名。
	CollectionDefault Collection = "default"
	// CollectionAvatar 是用户头像附件集合。
	CollectionAvatar Collection = "avatar"
)

// ErrMediaNotFound 表示未查询到目标媒体记录。
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
	pipeline *PipelineImpl
	image    Client
	cfg      config.MediaConfig
}

// newManager 构建多态媒体管理门面实例。
// 形参类型即传播契约：句柄是根连接还是活跃事务由调用方决定，
// 门面内每条语句再经 database.ResolveDB(ctx, db) 解析，调用方的事务不会被静默排除。
func newManager(
	mediaCfg config.MediaConfig,
	disk storage.Disk,
	db bun.IDB,
	pipeline *PipelineImpl,
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

// Image 返回底层 Imgproxy 图像处理器。
func (m *Manager) Image() Client {
	return m.image
}

// Attach 将文件或流上传并关联至指定业务模型的多态集合中。
func (m *Manager) Attach(
	ctx context.Context,
	target any,
	collection Collection,
	source any,
	opts ...AttachOption,
) (*models.Media, error) {
	modelType, modelID, err := attachableTarget(target)
	if err != nil {
		return nil, err
	}

	collection = m.normalizeCollection(collection)

	var conf AttachConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&conf)
		}
	}

	// 1. 上传文件到底层存储并获取元数据
	fileInfo, err := m.uploadSource(ctx, source, string(collection))
	if err != nil {
		return nil, err
	}

	// 2. 单文件模式下旧记录与新记录在同一事务内原子替换。
	// 物理文件的删除无法回滚，因此先只删记录，待事务提交后再删文件。
	mediaRecord := buildMediaRecord(conf, modelType, modelID, collection, fileInfo)

	if m.db == nil {
		return mediaRecord, nil
	}

	var replaced []*models.Media
	err = database.RunInTx(ctx, m.db, func(txCtx context.Context) error {
		if conf.SingleFile {
			old, loadErr := m.queryMedia(txCtx, modelType, modelID, collection, 0)
			if loadErr != nil {
				return loadErr
			}
			replaced = old
			if _, delErr := database.ResolveDB(txCtx, m.db).NewDelete().
				Model((*models.Media)(nil)).
				Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, string(collection)).
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
			m.discardFiles(ctx, replacedFiles)
		})
		return nil
	})
	if err != nil {
		// 事务已回滚，新上传的物理文件不应留在磁盘上。
		discardUploads(ctx, m.pipeline, []*FileInfo{fileInfo})
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.Attach").Wrap(err)
	}

	return mediaRecord, nil
}

// All 查询指定宿主在指定集合下的全部媒体记录。
func (m *Manager) All(ctx context.Context, target any, collection Collection) ([]*models.Media, error) {
	modelType, modelID, err := attachableTarget(target)
	if err != nil {
		return nil, err
	}

	return m.queryMedia(ctx, modelType, modelID, m.normalizeCollection(collection), 0)
}

// First 查询指定宿主在指定集合下的首条媒体记录；不存在时返回 ErrMediaNotFound。
func (m *Manager) First(ctx context.Context, target any, collection Collection) (*models.Media, error) {
	modelType, modelID, err := attachableTarget(target)
	if err != nil {
		return nil, err
	}

	items, err := m.queryMedia(ctx, modelType, modelID, m.normalizeCollection(collection), 1)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, oops.Code(errs.CodeNotFound).In("media.Manager.First").Wrap(ErrMediaNotFound)
	}

	return items[0], nil
}

// Update 局部更新指定媒体记录的可编辑字段并返回更新后的记录。
// 仅 WithName / WithCustomProperties / WithManipulations / WithOrderColumn 生效，未提供的选项对应列不参与更新；
// WithName 传入空串或纯空白视为「不修改」（name 列 NOT NULL，无法清空）；
// 一个可更新字段都没提供时报错，避免 WithSingleFile 这类选项被静默忽略；
// 目标记录不存在时返回 ErrMediaNotFound。
// 返回值是入参记录与本次写入字段的合并结果，不做回读（写入的列即库内值）。
func (m *Manager) Update(ctx context.Context, mediaItem *models.Media, opts ...AttachOption) (*models.Media, error) {
	if mediaItem == nil || mediaItem.ID == 0 {
		return nil, oops.Code(errs.CodeBadRequest).Errorf("media: invalid media record for update")
	}

	var conf AttachConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&conf)
		}
	}

	// 浅拷贝：下面只整体替换受控字段，不解引用记录中的 map 与指针。
	updated := *mediaItem
	// 容量按「4 个可更新字段 + 恒定的 updated_at」预分配，避免中途扩容。
	columns := make([]string, 0, 5)

	if name := strings.TrimSpace(conf.Name); name != "" {
		updated.Name = name
		columns = append(columns, "name")
	}
	if conf.CustomProperties != nil {
		updated.CustomProperties = conf.CustomProperties
		columns = append(columns, "custom_properties")
	}
	if conf.Manipulations != nil {
		updated.Manipulations = conf.Manipulations
		columns = append(columns, "manipulations")
	}
	if conf.OrderColumn != nil {
		updated.OrderColumn = conf.OrderColumn
		columns = append(columns, "order_column")
	}
	if len(columns) == 0 {
		return nil, oops.Code(errs.CodeBadRequest).Errorf("media: no updatable field provided")
	}

	db := database.ResolveDB(ctx, m.db)
	if db == nil {
		return nil, oops.Code(errs.CodeInternal).Errorf("media: database is not configured")
	}

	// updated_at 没有数据库侧触发器，必须显式刷新。
	updated.UpdatedAt = time.Now().UTC()
	columns = append(columns, "updated_at")

	result, err := db.NewUpdate().Model(&updated).Column(columns...).WherePK().Exec(ctx)
	if err != nil {
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.Update").Wrap(err)
	}

	// UPDATE 匹配 0 行在 Postgres 里不是错误：不检查会把「记录已不存在」当成保存成功返回。
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.Update").Wrap(err)
	}
	if affected == 0 {
		return nil, oops.Code(errs.CodeNotFound).In("media.Manager.Update").Wrap(ErrMediaNotFound)
	}

	return &updated, nil
}

// Delete 硬删除指定媒体记录：事务内先取回记录真实的文件路径，再删除数据库记录，
// 并在事务提交后删除该物理文件。
// 记录不存在时按幂等成功处理（重试安全），刻意不返回 ErrMediaNotFound——与 First/Update 的语义不同。
func (m *Manager) Delete(ctx context.Context, mediaItem *models.Media) error {
	if mediaItem == nil || mediaItem.ID == 0 {
		return oops.Code(errs.CodeBadRequest).Errorf("media: invalid media record for delete")
	}
	if database.ResolveDB(ctx, m.db) == nil {
		return oops.Code(errs.CodeInternal).Errorf("media: database is not configured")
	}

	err := database.RunInTx(ctx, m.db, func(txCtx context.Context) error {
		db := database.ResolveDB(txCtx, m.db)

		// 物理文件路径以库内记录为准：调用方传入的结构体可能陈旧、甚至来自请求体，
		// 直接拿它删文件会误删同一存储下的其它对象。
		var current models.Media
		if loadErr := db.NewSelect().
			Model(&current).
			Column("file_name").
			Where("id = ?", mediaItem.ID).
			Scan(txCtx); loadErr != nil {
			if errs.IsNoRows(loadErr) {
				return nil
			}
			return loadErr
		}

		if _, delErr := db.NewDelete().
			Model((*models.Media)(nil)).
			Where("id = ?", mediaItem.ID).
			Exec(txCtx); delErr != nil {
			return delErr
		}

		// 物理文件删除无法随事务回滚，只能登记到提交之后；
		// 若在闭包内直接删除，外层事务回滚后记录会被恢复，却已指向不存在的文件。
		fileName := current.FileName
		database.OnCommit(txCtx, func() {
			m.discardFile(ctx, fileName)
		})
		return nil
	})
	if err != nil {
		return oops.Code(errs.CodeInternal).In("media.Manager.Delete").Wrap(err)
	}

	return nil
}

// uploadSource 内部按源类型调度底层管道上传。
func (m *Manager) uploadSource(ctx context.Context, source any, category string) (*FileInfo, error) {
	switch s := source.(type) {
	case Source:
		return m.pipeline.Upload(ctx, s, category)
	case []byte:
		return m.pipeline.UploadBytes(ctx, s, "upload.bin", category)
	default:
		return nil, oops.Code(errs.CodeBadRequest).Errorf("media: unsupported upload source type: %T", source)
	}
}

// attachableTarget 解析并校验多态宿主，返回清洗后的模型类型与主键。
func attachableTarget(target any) (modelType, modelID string, err error) {
	attachable, ok := target.(Attachable)
	if !ok || attachable == nil || strings.TrimSpace(attachable.GetModelID()) == "" {
		return "", "", oops.Code(errs.CodeBadRequest).Errorf("media: invalid attachable target or empty model id")
	}

	return strings.TrimSpace(attachable.GetModelType()), strings.TrimSpace(attachable.GetModelID()), nil
}

// normalizeCollection 补全缺省集合名：调用方值 → 配置默认值 → 内置默认值。
func (m *Manager) normalizeCollection(collection Collection) Collection {
	return Collection(cmp.Or(
		strings.TrimSpace(string(collection)),
		m.cfg.DefaultCollection,
		string(CollectionDefault),
	))
}

// queryMedia 按多态宿主与集合查询媒体记录，按排序号与自增主键升序（未设排序号的记录排在已设排序号之后）；
// limit <= 0 表示不限制条数。
func (m *Manager) queryMedia(
	ctx context.Context,
	modelType, modelID string,
	collection Collection,
	limit int,
) ([]*models.Media, error) {
	db := database.ResolveDB(ctx, m.db)
	if db == nil {
		return nil, oops.Code(errs.CodeInternal).Errorf("media: database is not configured")
	}

	items := make([]*models.Media, 0, 1)
	query := db.NewSelect().
		Model(&items).
		Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, string(collection)).
		// 未设排序号的记录排在已设排序号之后；同序时按自增主键保持插入顺序。
		OrderBy("order_column", bun.OrderAscNullsLast).
		OrderBy("id", bun.OrderAsc)
	if limit > 0 {
		query = query.Limit(int64(limit))
	}

	if err := query.Scan(ctx); err != nil {
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.Query").Wrap(err)
	}

	return items, nil
}

// discardFile 删除已无记录引用的物理文件；仍被其它记录引用时跳过。
// 内容哈希命名下内容相同的上传会复用同一路径，因此删除前必须排除共享文件的场景。
func (m *Manager) discardFile(ctx context.Context, fileName string) {
	if fileName == "" {
		return
	}

	if m.referencedFiles(ctx, []string{fileName})[fileName] {
		return
	}

	m.deleteObject(ctx, fileName)
}

// discardFiles 批量删除已无记录引用的物理文件。
// 引用关系一次性查出，避免「每个文件一次查询」的 N+1。
func (m *Manager) discardFiles(ctx context.Context, items []*models.Media) {
	fileNames := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && item.FileName != "" {
			fileNames = append(fileNames, item.FileName)
		}
	}
	if len(fileNames) == 0 {
		return
	}

	referenced := m.referencedFiles(ctx, fileNames)
	for _, fileName := range fileNames {
		if referenced[fileName] {
			continue
		}
		m.deleteObject(ctx, fileName)
	}
}

// referencedFiles 一次性查出给定路径中仍被媒体记录引用的子集。
// 查询失败时按「全部仍被引用」处理：宁可留下孤儿文件，也不误删其它记录正在使用的文件。
func (m *Manager) referencedFiles(ctx context.Context, fileNames []string) map[string]bool {
	referenced := make(map[string]bool, len(fileNames))

	db := database.ResolveDB(ctx, m.db)
	if db == nil {
		return referenced
	}

	found := make([]string, 0, len(fileNames))
	if err := db.NewSelect().
		Model((*models.Media)(nil)).
		Column("file_name").
		Where("file_name IN (?)", bun.List(fileNames)).
		Scan(ctx, &found); err != nil {
		logger.FromContext(ctx).Warn("failed to check media file references",
			zap.Error(err),
			zap.Int("paths", len(fileNames)),
		)
		for _, fileName := range fileNames {
			referenced[fileName] = true
		}
		return referenced
	}

	for _, fileName := range found {
		referenced[fileName] = true
	}

	return referenced
}

// deleteObject 删除物理对象；失败只记日志，不阻断调用方清理流程。
func (m *Manager) deleteObject(ctx context.Context, fileName string) {
	if err := m.disk.Delete(ctx, fileName); err != nil {
		logger.FromContext(ctx).Warn("failed to delete media file",
			zap.Error(err),
			zap.String("path", fileName),
		)
	}
}
