// Package database_test 验证 UserQuery 深模块的对外行为。
package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/shared/enums"
)

// renderUserQuery 把规格应用到 User 查询并返回 SQL 文本（不触库）。
func renderUserQuery(t *testing.T, q models.UserQuery) string {
	t.Helper()
	db := bun.NewDB(nil, pgdialect.New())
	return q.Apply(db.NewSelect().Model((*models.User)(nil))).String()
}

// TestUserQueryFilters 校验状态、角色与关键字过滤由规格独占，软删除谓词由 Bun 自动附加。
func TestUserQueryFilters(t *testing.T) {
	t.Parallel()

	t.Run("状态与角色过滤", func(t *testing.T) {
		t.Parallel()
		sql := renderUserQuery(t, models.UserQuery{Status: enums.StatusBanned, Role: enums.RoleAdmin})
		assert.Contains(t, sql, "u.status = 'banned'")
		assert.Contains(t, sql, "u.role = 'admin'")
	})

	t.Run("空过滤不产生条件", func(t *testing.T) {
		t.Parallel()
		sql := renderUserQuery(t, models.UserQuery{})
		assert.NotContains(t, sql, "status =")
		assert.NotContains(t, sql, "role =")
		assert.NotContains(t, sql, "ILIKE")
	})

	t.Run("关键字转义通配符并修剪空白", func(t *testing.T) {
		t.Parallel()
		sql := renderUserQuery(t, models.UserQuery{Search: " 50%_off "})
		assert.Contains(t, sql, `%50\%\_off%`)
	})

	t.Run("软删除谓词由 Bun 自动附加", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, renderUserQuery(t, models.UserQuery{}), `"u"."deleted_at" IS NULL`)
	})
}

// TestUserQueryComposesPagination 校验规格把过滤、排序与分页组合进同一条查询。
func TestUserQueryComposesPagination(t *testing.T) {
	t.Parallel()

	db := bun.NewDB(nil, pgdialect.New())
	q := models.UserQuery{
		Search:     "alex",
		Status:     enums.StatusActive,
		Sort:       "-email",
		Pagination: database.Pagination{Page: 3, PageSize: 25},
	}

	sql := q.Pagination.Apply(q.Apply(db.NewSelect().Model((*models.User)(nil)))).String()

	assert.Contains(t, sql, "u.status = 'active'")
	assert.Contains(t, sql, "ILIKE '%alex%'")
	assert.Contains(t, sql, `ORDER BY "u"."email" DESC`)
	assert.Contains(t, sql, "LIMIT 25")
	assert.Contains(t, sql, "OFFSET 50")
}

// TestUserQueryChunkUsesSameSpec 校验流式导出分块与分页列表共用同一份过滤与排序规格。
func TestUserQueryChunkUsesSameSpec(t *testing.T) {
	t.Parallel()

	db := bun.NewDB(nil, pgdialect.New())
	q := models.UserQuery{Search: "alex", Status: enums.StatusActive, Sort: "-email"}

	listSQL := q.Pagination.Apply(q.Apply(db.NewSelect().Model((*models.User)(nil)))).String()
	chunkSQL := q.Apply(db.NewSelect().Model((*models.User)(nil))).Offset(0).Limit(500).String()

	for _, fragment := range []string{"u.status = 'active'", "ILIKE '%alex%'", `ORDER BY "u"."email" DESC`} {
		assert.Contains(t, listSQL, fragment)
		assert.Contains(t, chunkSQL, fragment, "分块与分页列表必须使用同一份规格")
	}
	assert.NotContains(t, chunkSQL, "OFFSET 50", "分块偏移量由导出器推进，不受分页参数影响")
}
