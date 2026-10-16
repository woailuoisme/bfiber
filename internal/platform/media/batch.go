package media

import (
	"cmp"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/samber/oops"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
)

// AttachMany 批量上传并关联多个上传源到指定集合，全部记录在同一事务内写入。
// 单文件集合（WithSingleFile）会先清空该集合的既有记录；排序号缺省接续集合内现有最大值，
// WithOrderColumn 可指定起始编号；WithName 应用于本批次全部记录。
// 任一文件上传失败或落库失败时，本次上传的物理文件都会被清理。
func (m *Manager) AttachMany(
	ctx context.Context,
	target any,
	collection Collection,
	sources []Source,
	opts ...AttachOption,
) ([]*models.Media, error) {
	if len(sources) == 0 {
		return nil, errs.BadRequest("no files provided for upload")
	}

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

	// 1. 批量上传：管道内部已在任一文件失败时回滚本次已落盘的文件。
	fileInfos, err := m.pipeline.UploadMultiple(ctx, sources, string(collection))
	if err != nil {
		return nil, err
	}

	// 2. 组装记录并依次编号。
	startOrder, err := m.batchStartOrder(ctx, modelType, modelID, collection, conf)
	if err != nil {
		discardUploads(ctx, m.pipeline, fileInfos)
		return nil, err
	}
	records := buildBatchRecords(conf, modelType, modelID, collection, fileInfos, startOrder)

	if database.ResolveDB(ctx, m.db) == nil {
		// 未装配数据库句柄（契约测试路径）：仅返回组装好的记录，与 Attach 的缝保持一致。
		return records, nil
	}

	// 3. 同一事务内替换旧记录并写入全部新记录。
	if err := m.insertBatch(ctx, modelType, modelID, collection, conf.SingleFile, records); err != nil {
		// 事务已回滚，本次上传的物理文件不应留在磁盘上。
		discardUploads(ctx, m.pipeline, fileInfos)
		return nil, oops.Code(errs.CodeInternal).In("media.Manager.AttachMany").Wrap(err)
	}

	return records, nil
}

// batchStartOrder 计算批量插入的起始排序号：显式指定优先，否则接续集合内现有最大值。
// 单文件集合会先清空集合，编号从 0 重新开始；未装配数据库句柄时同样从 0 开始。
func (m *Manager) batchStartOrder(
	ctx context.Context,
	modelType, modelID string,
	collection Collection,
	conf AttachConfig,
) (int, error) {
	if conf.OrderColumn != nil {
		return *conf.OrderColumn, nil
	}

	db := database.ResolveDB(ctx, m.db)
	if conf.SingleFile || db == nil {
		return 0, nil
	}

	// 用聚合查询取下一个编号，避免把整个集合读进内存；
	// COALESCE 同时覆盖「集合为空」与「现有排序号全为空」两种情况（空值不参与 MAX）。
	next := 0
	if err := db.NewSelect().
		Model((*models.Media)(nil)).
		ColumnExpr("COALESCE(MAX(order_column), -1) + 1").
		Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, string(collection)).
		Scan(ctx, &next); err != nil {
		return 0, oops.Code(errs.CodeInternal).In("media.Manager.StartOrder").Wrap(err)
	}

	return next, nil
}

// buildBatchRecords 按起始排序号依次组装本批次的媒体记录。
func buildBatchRecords(
	conf AttachConfig,
	modelType, modelID string,
	collection Collection,
	fileInfos []*FileInfo,
	startOrder int,
) []*models.Media {
	records := make([]*models.Media, 0, len(fileInfos))
	for index, fileInfo := range fileInfos {
		order := startOrder + index
		conf.OrderColumn = &order
		records = append(records, buildMediaRecord(conf, modelType, modelID, collection, fileInfo))
	}
	return records
}

// insertBatch 在单一事务内完成「可选清空集合 + 批量插入」，并登记提交后清理被替换记录的物理文件。
func (m *Manager) insertBatch(
	ctx context.Context,
	modelType, modelID string,
	collection Collection,
	singleFile bool,
	records []*models.Media,
) error {
	var replaced []*models.Media

	return database.RunInTx(ctx, m.db, func(txCtx context.Context) error {
		db := database.ResolveDB(txCtx, m.db)

		if singleFile {
			old, loadErr := m.queryMedia(txCtx, modelType, modelID, collection, 0)
			if loadErr != nil {
				return loadErr
			}
			replaced = old
			if _, delErr := db.NewDelete().
				Model((*models.Media)(nil)).
				Where("model_type = ? AND model_id = ? AND collection_name = ?", modelType, modelID, string(collection)).
				Exec(txCtx); delErr != nil {
				return delErr
			}
		}

		if _, insertErr := db.NewInsert().Model(&records).Exec(txCtx); insertErr != nil {
			return insertErr
		}

		// 物理文件删除无法随事务回滚，只能登记到提交之后。
		replacedFiles := replaced
		database.OnCommit(txCtx, func() {
			m.discardFiles(ctx, replacedFiles)
		})
		return nil
	})
}

// buildMediaRecord 依据上传结果与附加配置组装待落库的媒体记录。
func buildMediaRecord(
	conf AttachConfig,
	modelType, modelID string,
	collection Collection,
	fileInfo *FileInfo,
) *models.Media {
	return &models.Media{
		ModelType:        modelType,
		ModelID:          modelID,
		CollectionName:   string(collection),
		Name:             clampName(cmp.Or(strings.TrimSpace(conf.Name), fileInfo.OriginalName, fileInfo.FileName)),
		FileName:         fileInfo.Path,
		MimeType:         fileInfo.MimeType,
		Disk:             cmp.Or(conf.DiskName, "default"),
		Size:             fileInfo.Size,
		OrderColumn:      conf.OrderColumn,
		CustomProperties: conf.CustomProperties,
		Manipulations:    conf.Manipulations,
	}
}

// clampName 将友好名称截断到 name 列宽上限（255 字节，按 UTF-8 边界收尾）。
// 客户端文件名不受长度约束，不截断会以落库失败（500）的形式暴露给上传方。
func clampName(name string) string {
	const maxNameBytes = 255
	if len(name) <= maxNameBytes {
		return name
	}

	trimmed := name[:maxNameBytes]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
