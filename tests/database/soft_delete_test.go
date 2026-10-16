// Package database_test 验证 Bun 原生软删除对 User 模型的实际行为。
package database_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"bfiber/internal/database/models"
)

// softDeletePredicate 是 Bun 为 soft_delete 字段生成的谓词（限定别名）。
// 断言前会剥掉标识符引号：Bun 的引号策略属实现细节，写死它会让升级即失效。
const softDeletePredicate = "u.deleted_at IS NULL"

// unquoteIdents 去掉 SQL 中的双引号标识符包装，便于稳定断言谓词语义。
func unquoteIdents(sql string) string {
	return strings.ReplaceAll(sql, `"`, "")
}

// TestNativeSoftDelete 校验 *time.Time 字段上的 soft_delete 标签会被 Bun 自动追加谓词。
// 这是删除手写 "deleted_at IS NULL" 作用域的前置依据：若本测试不成立，
// 读路径必须保留共享作用域而不是各处手写谓词。
func TestNativeSoftDelete(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())

	t.Run("Select 自动追加软删除谓词", func(t *testing.T) {
		q := db.NewSelect().Model((*models.User)(nil)).Where("u.id = ?", "u_1")
		assert.Contains(t, unquoteIdents(q.String()), softDeletePredicate)
	})

	t.Run("Update 自动追加软删除谓词", func(t *testing.T) {
		q := db.NewUpdate().Model((*models.User)(nil)).Set("name = ?", "x").Where("u.id = ?", "u_1")
		assert.Contains(t, unquoteIdents(q.String()), softDeletePredicate)
	})

	t.Run("Delete 转写为 UPDATE 并设置 deleted_at", func(t *testing.T) {
		q := db.NewDelete().Model((*models.User)(nil)).Where("u.id = ?", "u_1")
		assert.Contains(t, q.String(), "UPDATE")
		assert.Contains(t, q.String(), "deleted_at")
	})
}
