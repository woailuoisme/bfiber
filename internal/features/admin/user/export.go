package admin

import (
	"context"
	"io"
	"time"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/excel"
)

// UserChunkFetcher 按分块窗口物理拉取一批用户实体。
// 过滤与排序规格由调用方在闭包中绑定，导出器只负责推进窗口。
type UserChunkFetcher func(ctx context.Context, window database.Window) ([]models.User, error)

// defaultUserHeadings 用户导出默认表头定义。
var defaultUserHeadings = []string{"ID", "Name", "Email", "Role", "Status", "Created At"}

func userExportOptions(opts []excel.ExportOption) []excel.ExportOption {
	return append([]excel.ExportOption{excel.WithSheetName("Users")}, opts...)
}

func mapUserModel(u *models.User) []any {
	return mapUserRow(u)
}

const defaultUserChunkSize = 500

// ExportChunkedStream 辅助函数：按同一查询规格分块物理拉取并直接流式序列化写入指定的 io.Writer。
func ExportChunkedStream(
	ctx context.Context,
	w io.Writer,
	fetch UserChunkFetcher,
	opts ...excel.ExportOption,
) error {
	size := defaultUserChunkSize
	return excel.NewExportBuilder[*models.User]().
		WithHeadings(defaultUserHeadings...).
		WithMapping(mapUserModel).
		FromGenerator(func(yield func(item *models.User) bool) error {
			if fetch == nil {
				return nil
			}
			// 先归一化再推进：断流判断用的 window.Limit 必须与实际下发的条数一致，
			// 否则调用方传入越界窗口时，取回的一批会小于未归一的 Limit 而被误判为最后一批。
			window := database.Window{Offset: 0, Limit: size}.Normalise()
			for {
				chunk, err := fetch(ctx, window)
				if err != nil {
					return err
				}
				if len(chunk) == 0 {
					break
				}
				for i := range chunk {
					if !yield(&chunk[i]) {
						return nil
					}
				}
				if len(chunk) < window.Limit {
					break
				}
				window = window.Next()
			}
			return nil
		}).
		WithOptions(userExportOptions(opts)...).
		Write(ctx, w)
}

func mapUserRow(row any) []any {
	var target *models.User
	switch u := row.(type) {
	case *models.User:
		target = u
	case models.User:
		target = &u
	default:
		return nil
	}

	if target == nil {
		return nil
	}

	createdAtStr := ""
	if !target.CreatedAt.IsZero() {
		createdAtStr = target.CreatedAt.Format(time.RFC3339)
	}

	return []any{
		target.ID,
		target.Name,
		target.Email,
		target.Role,
		target.Status,
		createdAtStr,
	}
}
