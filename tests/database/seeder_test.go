package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/schema"

	"bfiber/internal/database/models"
	"bfiber/internal/database/seeders"
)

type mockCounterSeeder struct {
	name  string
	count int
}

func (m *mockCounterSeeder) Name() string {
	return m.name
}

func (m *mockCounterSeeder) Seed(_ context.Context, _ *bun.DB) error {
	m.count++
	return nil
}

func TestSeeder_Registry(t *testing.T) {
	// DatabaseSeeder, AdminSeeder, UserSeeder 已经在 init() 注册
	list := seeders.List()
	assert.Contains(t, list, "DatabaseSeeder")
	assert.Contains(t, list, "AdminSeeder")
	assert.Contains(t, list, "UserSeeder")

	s, ok := seeders.Get("DatabaseSeeder")
	require.True(t, ok)
	require.NotNil(t, s)

	_, ok = seeders.Get("NonExistentSeeder")
	assert.False(t, ok)

	// 测试动态注册与别名检索
	customMock := &mockCounterSeeder{name: "CustomDynamicSeeder"}
	seeders.Register(customMock, "CustomAlias")

	found, ok := seeders.Get("CustomDynamicSeeder")
	require.True(t, ok)
	assert.Equal(t, customMock, found)

	foundAlias, ok := seeders.Get("CustomAlias")
	require.True(t, ok)
	assert.Equal(t, customMock, foundAlias)

	all := seeders.List()
	assert.Contains(t, all, "CustomDynamicSeeder")
	assert.Contains(t, all, "CustomAlias")
}

func TestSeeder_RunnerAntiDuplicate(t *testing.T) {
	ctx := t.Context()
	runner := seeders.NewRunner(nil)

	s1 := &mockCounterSeeder{name: "MockUserSeeder"}
	s2 := &mockCounterSeeder{name: "MockPostSeeder"}

	var steps []string
	runner.WithStepHook(func(name string, index, total int, duration time.Duration, done bool) {
		if done {
			steps = append(steps, name)
		}
	})

	// 第一次调用 s1, s2 以及重复传入 s1（模拟菱形依赖触发重复调用）
	err := runner.Call(ctx, s1, s2, s1)
	require.NoError(t, err)
	assert.Equal(t, 1, s1.count, "相同 Seeder 在同一 Runner 生命周期内不应重复执行")
	assert.Equal(t, 1, s2.count)
	assert.Equal(t, []string{"MockUserSeeder", "MockPostSeeder"}, steps)

	// 第二次单独调用 s1
	err = runner.Call(ctx, s1)
	require.NoError(t, err)
	assert.Equal(t, 1, s1.count, "二次调度已执行过的 Seeder 应当被幂等过滤")
}

func TestSeeder_TruncateTableQuery_SQL(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())
	q1 := db.NewTruncateTable().Table("users").Cascade()
	query1, err := q1.AppendQuery(schema.NewQueryGen(pgdialect.New()), nil)
	require.NoError(t, err)
	assert.Equal(t, `TRUNCATE TABLE "users" RESTART IDENTITY CASCADE`, string(query1))

	q2 := db.NewTruncateTable().Model((*models.User)(nil)).Cascade()
	query2, err := q2.AppendQuery(schema.NewQueryGen(pgdialect.New()), nil)
	require.NoError(t, err)
	assert.Equal(t, `TRUNCATE TABLE "user" RESTART IDENTITY CASCADE`, string(query2))
}

func TestSeeder_TruncateValidation(t *testing.T) {
	ctx := t.Context()
	db := bun.NewDB(nil, pgdialect.New())
	runner := seeders.NewRunner(db)

	// 空 targets 应安全跳过
	err := runner.Truncate(ctx)
	require.NoError(t, err)

	// 包含 nil 应报错拦截
	err = runner.Truncate(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target cannot be nil")
}
