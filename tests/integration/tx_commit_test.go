//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/database"
)

// TestOnCommitOrdering 校验提交后回调的时序契约：只在最外层事务提交成功后执行，回滚时一律丢弃。
//
// 这条契约保护的是「数据库之外的副作用」——例如媒体库删除被替换的物理文件。
// 若在事务闭包内直接删除，外层事务回滚后旧记录会被恢复却已指向不存在的文件；
// 若回调在回滚路径上被执行，则连新写入的数据也会丢失而文件已被删。
func TestOnCommitOrdering(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	t.Run("嵌套事务中登记的回调延迟到最外层提交之后", func(t *testing.T) {
		var order []string

		err := database.RunInTx(ctx, db, func(outerCtx context.Context) error {
			return database.RunInTx(outerCtx, db, func(innerCtx context.Context) error {
				database.OnCommit(innerCtx, func() {
					order = append(order, "commit-hook")
				})
				order = append(order, "inside-tx")
				return nil
			})
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"inside-tx", "commit-hook"}, order,
			"回调必须在闭包返回之后执行，即外层事务提交之后")
	})

	t.Run("回滚时回调被丢弃", func(t *testing.T) {
		hookRan := false
		sentinel := errors.New("rollback")

		err := database.RunInTx(ctx, db, func(outerCtx context.Context) error {
			return database.RunInTx(outerCtx, db, func(innerCtx context.Context) error {
				database.OnCommit(innerCtx, func() { hookRan = true })
				return sentinel
			})
		})
		require.ErrorIs(t, err, sentinel)
		assert.False(t, hookRan, "事务回滚后提交后回调不得执行")
	})

	t.Run("无事务时立即执行", func(t *testing.T) {
		ran := false
		database.OnCommit(ctx, func() { ran = true })
		assert.True(t, ran, "不存在事务边界时应立即执行，否则回调将永不执行")
	})
}
