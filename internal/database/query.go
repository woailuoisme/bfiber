package database

import (
	"context"
	"strings"

	"github.com/uptrace/bun"

	"bfiber/internal/platform/response"
)

// QueryApplier 描述能够直接应用过滤、排序与分页规格的查询构建对象。
type QueryApplier interface {
	Apply(q *bun.SelectQuery) *bun.SelectQuery
	Pagination() Pagination
}

// Paginate 执行具备规格化过滤、排序、分页与总数统计的通用查询，并返回统一的分页元数据。
func Paginate[T any](ctx context.Context, db bun.IDB, applier QueryApplier) ([]T, response.Meta, error) {
	page, pageSize := applier.Pagination().Normalise()
	// 显式初始化为非 nil 空切片，确保在无命中记录时 JSON 序列化为 `[]` 而非 `null`，避免前端消费空指针。
	items := make([]T, 0)

	q := ResolveDB(ctx, db).NewSelect().Model(&items)
	total, err := applier.Apply(q).ScanAndCount(ctx)
	if err != nil {
		return nil, response.Meta{}, err
	}
	return items, response.NewMeta(page, pageSize, int(total)), nil
}

// Pagination 封装基于偏移量的安全分页参数与区间校验。
type Pagination struct {
	Page     int `form:"page" json:"page" query:"page"`
	PageSize int `form:"pageSize" json:"pageSize" query:"pageSize"`
}

const (
	// DefaultPage 默认起始页码。
	DefaultPage = 1
	// DefaultPageSize 默认每页记录数。
	DefaultPageSize = 20
	// MaxPageSize 单页允许获取的最大记录数上限。
	MaxPageSize = 100
	// MaxPage 最大允许翻页页数，防止深翻页导致的内存与数据库溢出。
	MaxPage = 1_000_000
)

// Normalise 返回符合系统安全区间的标准化 (page, pageSize)。
func (p Pagination) Normalise() (int, int) {
	page := p.Page
	if page < 1 {
		page = DefaultPage
	} else if page > MaxPage {
		page = MaxPage
	}

	pageSize := p.PageSize
	if pageSize < 1 {
		pageSize = DefaultPageSize
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}

// Offset 返回基于 0 索引的 SQL 查询偏移量。
func (p Pagination) Offset() int {
	page, pageSize := p.Normalise()
	return (page - 1) * pageSize
}

// Limit 返回 SQL 查询限制行数。
func (p Pagination) Limit() int {
	_, pageSize := p.Normalise()
	return pageSize
}

// Apply 为 Bun 查询附加 LIMIT 与 OFFSET 分页子句。
func (p Pagination) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Offset(int64(p.Offset())).Limit(int64(p.Limit()))
}

// Sorter 封装排序字段白名单与方向解析，杜绝 SQL 注入。
type Sorter struct {
	Allowed map[string]string // e.g. "createdAt" -> "u.created_at"
	Default string            // e.g. "u.created_at DESC"
}

// Parse 解析逗号分隔的排序表达式，返回校验合法的 ORDER BY 子句列表。
func (s Sorter) Parse(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if s.Default != "" {
			return []string{s.Default}
		}
		return nil
	}

	orders := make([]string, 0, 4)
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		direction := "ASC"
		if strings.HasPrefix(part, "-") {
			direction = "DESC"
			part = strings.TrimPrefix(part, "-")
		}
		col, ok := s.Allowed[part]
		if !ok {
			continue
		}
		orders = append(orders, col+" "+direction)
	}
	if len(orders) == 0 && s.Default != "" {
		return []string{s.Default}
	}
	return orders
}

// Apply 为 Bun 查询附加 ORDER BY 子句。
func (s Sorter) Apply(q *bun.SelectQuery, raw string) *bun.SelectQuery {
	for _, order := range s.Parse(raw) {
		q = q.Order(order)
	}
	return q
}

// EscapeLike 转义用于 SQL LIKE / ILIKE 模糊检索的特殊通配符（\、%、_）。
func EscapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}
