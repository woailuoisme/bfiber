package database

import (
	"strings"

	"github.com/uptrace/bun"
)

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

// Window 描述按偏移量推进的分块窗口，用于流式导出等非请求分页场景的游标。
//
// 与 Pagination 的区别是语义而非形状：Pagination 表达"第几页"（受 MaxPageSize 约束，
// 面向调用方能传入的请求参数），Window 表达"从第几条起取多少条"（条数由导出器自选的
// 分块大小决定，允许大于 MaxPageSize）。把两者混用会把页面上限错误地施加到导出分块上。
//
// 约定：把窗口交给取数方之前先 Normalise，并以同一份结果做断流判断——
// 取数方会再次归一，两边若用了不同的 Limit 会把"取满一批"误判成"已取完"。
type Window struct {
	Offset int
	Limit  int
}

// MaxWindowSize 是分块条数的安全上限：它不是页面上限的复用，而是防止调用方传入
// 病态窗口（一次拉取百万行）导致内存放大。默认导出分块 500 远低于此值。
const MaxWindowSize = 10_000

// Normalise 返回收敛后的窗口：偏移非负，条数至少为 1。
// 未指定（<=0）时回落到 DefaultPageSize，超过安全上限时压到 MaxWindowSize。
// 下限刻意取 1 而不是 DefaultPageSize：分块条数由取数方自选，小分块是合法用法，
// 按下限抬高会把"每批 10 行"的请求悄悄变成每批 20 行。
func (w Window) Normalise() Window {
	limit := w.Limit
	switch {
	case limit <= 0:
		limit = DefaultPageSize
	case limit > MaxWindowSize:
		limit = MaxWindowSize
	}
	return Window{Offset: max(w.Offset, 0), Limit: limit}
}

// Apply 为 Bun 查询附加窗口的 LIMIT 与 OFFSET 子句。
func (w Window) Apply(q *bun.SelectQuery) *bun.SelectQuery {
	normalised := w.Normalise()
	return q.Offset(int64(normalised.Offset)).Limit(int64(normalised.Limit))
}

// Next 返回顺次推进一个窗口后的下一个窗口。
func (w Window) Next() Window {
	normalised := w.Normalise()
	normalised.Offset += normalised.Limit
	return normalised
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
