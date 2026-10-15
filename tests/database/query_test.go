package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"bfiber/internal/database"
)

func TestPagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		p            database.Pagination
		wantPage     int
		wantPageSize int
		wantOffset   int
		wantLimit    int
	}{
		{
			name:         "零值默认化",
			p:            database.Pagination{},
			wantPage:     database.DefaultPage,
			wantPageSize: database.DefaultPageSize,
			wantOffset:   0,
			wantLimit:    20,
		},
		{
			name:         "越界自动纠偏",
			p:            database.Pagination{Page: -1, PageSize: 500},
			wantPage:     database.DefaultPage,
			wantPageSize: database.MaxPageSize,
			wantOffset:   0,
			wantLimit:    100,
		},
		{
			name:         "超大页码封顶",
			p:            database.Pagination{Page: 2_000_000, PageSize: 10},
			wantPage:     database.MaxPage,
			wantPageSize: 10,
			wantOffset:   9_999_990,
			wantLimit:    10,
		},
		{
			name:         "正常分页计算",
			p:            database.Pagination{Page: 3, PageSize: 15},
			wantPage:     3,
			wantPageSize: 15,
			wantOffset:   30,
			wantLimit:    15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, pageSize := tt.p.Normalise()
			assert.Equal(t, tt.wantPage, page)
			assert.Equal(t, tt.wantPageSize, pageSize)
			assert.Equal(t, tt.wantOffset, tt.p.Offset())
			assert.Equal(t, tt.wantLimit, tt.p.Limit())
		})
	}
}

func TestSorter(t *testing.T) {
	t.Parallel()

	sorter := database.Sorter{
		Allowed: map[string]string{
			"name":      "u.name",
			"createdAt": "u.created_at",
		},
		Default: "u.created_at DESC",
	}

	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "空串回退默认排序",
			raw:  "",
			want: []string{"u.created_at DESC"},
		},
		{
			name: "空白回退默认排序",
			raw:  "   ",
			want: []string{"u.created_at DESC"},
		},
		{
			name: "正常升序与降序组合",
			raw:  "name,-createdAt",
			want: []string{"u.name ASC", "u.created_at DESC"},
		},
		{
			name: "忽略未知列与注入尝试",
			raw:  "unknown, name, id; DROP TABLE test",
			want: []string{"u.name ASC"},
		},
		{
			name: "全部非法时退回默认排序",
			raw:  "foo, bar, baz",
			want: []string{"u.created_at DESC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sorter.Parse(tt.raw))
		})
	}
}

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"normal", "normal"},
		{"100%", `100\%`},
		{"user_name", `user\_name`},
		{`path\to`, `path\\to`},
		{`%_\\`, `\%\_\\\\`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, database.EscapeLike(tt.input))
		})
	}
}

func TestPaginationAndSorter_Apply(t *testing.T) {
	t.Parallel()

	db := bun.NewDB(nil, pgdialect.New())

	p := database.Pagination{Page: 2, PageSize: 10}
	sorter := database.Sorter{
		Allowed: map[string]string{"name": "u.name"},
		Default: "u.created_at DESC",
	}

	q := db.NewSelect().TableExpr("users AS u")
	q = p.Apply(q)
	q = sorter.Apply(q, "-name")

	sql := q.String()
	assert.Contains(t, sql, "LIMIT 10")
	assert.Contains(t, sql, "OFFSET 10")
	assert.Contains(t, sql, "ORDER BY")
	assert.Contains(t, sql, "name\" DESC")
}
