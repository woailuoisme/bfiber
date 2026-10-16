package models

import (
	"context"
	"strings"

	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/platform/response"
	"bfiber/internal/shared/enums"
)

// defaultUserSort 是所有查询规格缺省或非法时的稳定排序，保证分页顺序可复现。
const defaultUserSort = "u.created_at DESC"

// UserQuery 承载用户列表查询的全部可变量：关键字、状态、角色、排序与分页。
//
// 列名映射、通配符转义、排序白名单校验与分页区间钳制全部内聚于实现内部，
// 调用方只表达业务意图，不需要知道任何 SQL 列名或转义规则。
// 软删除排除由 Bun 的 soft_delete 标签自动完成，此处不再手写谓词。
type UserQuery struct {
	// Search 按昵称、邮箱与用户名模糊检索的关键字。
	Search string
	// Status 账号状态过滤，空值表示不过滤。
	Status enums.Status
	// Role 权限角色过滤，空值表示不过滤。
	Role enums.Role
	// Sort 逗号分隔的排序表达式，字段名前加 - 表示倒序；非白名单字段被忽略。
	Sort string
	// Pagination 偏移量分页参数，越界值会被钳制到安全区间。
	Pagination database.Pagination
}

// Apply 把完整规格附加到 Bun 查询：过滤与排序。
// 分页由调用方自行决定（分页列表附加 LIMIT/OFFSET，流式导出逐块指定），
// 因此本方法只负责与调用方无关的那部分规格。
func (q UserQuery) Apply(sel *bun.SelectQuery) *bun.SelectQuery {
	return q.applySort(q.applyFilters(sel))
}

// Paginate 返回分页结果与分页元信息，单次往返同时完成取数与计数。
func (q UserQuery) Paginate(ctx context.Context, db bun.IDB) ([]User, response.Meta, error) {
	page, pageSize := q.Pagination.Normalise()

	items := make([]User, 0, pageSize)
	sel := q.Apply(database.ResolveDB(ctx, db).NewSelect().Model(&items))
	total, err := q.Pagination.Apply(sel).ScanAndCount(ctx)
	if err != nil {
		return nil, response.Meta{}, err
	}
	return items, response.NewMeta(page, pageSize, int(total)), nil
}

// Chunk 按分块窗口物理拉取一批用户，供流式导出器以同一规格分块消费。
// 与 Paginate 共用同一份过滤与排序，保证导出结果与分页列表完全一致；
// 窗口由 database.Window 收敛，因此导出分块不受页面上限约束，也不会出现负偏移。
func (q UserQuery) Chunk(ctx context.Context, db bun.IDB, window database.Window) ([]User, error) {
	window = window.Normalise()

	items := make([]User, 0, window.Limit)
	if err := window.Apply(q.Apply(database.ResolveDB(ctx, db).NewSelect().Model(&items))).Scan(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// applyFilters 附加状态、角色与关键字过滤。
func (q UserQuery) applyFilters(sel *bun.SelectQuery) *bun.SelectQuery {
	if q.Status != "" {
		sel = sel.Where("u.status = ?", q.Status)
	}
	if q.Role != "" {
		sel = sel.Where("u.role = ?", q.Role)
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		pattern := "%" + database.EscapeLike(search) + "%"
		sel = sel.Where("(u.name ILIKE ? OR u.email ILIKE ? OR u.username ILIKE ?)", pattern, pattern, pattern)
	}
	return sel
}

// applySort 按白名单解析排序表达式，非法或缺失时退回稳定默认排序以杜绝注入。
func (q UserQuery) applySort(sel *bun.SelectQuery) *bun.SelectQuery {
	sorter := database.Sorter{Allowed: UserSortColumns, Default: defaultUserSort}
	return sorter.Apply(sel, q.Sort)
}
