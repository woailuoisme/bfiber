// Package database_test 验证数据模型与数据库基础设施的各项契约。
package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	"bfiber/internal/shared/enums"
)

// TestIsBanned 校验封禁判定把已过期的封禁视为解除。
func TestIsBanned(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name string
		user models.User
		want bool
	}{
		{"未封禁", models.User{}, false},
		{"永久封禁（无到期时间）", models.User{Banned: true}, true},
		{"封禁未到期", models.User{Banned: true, BanExpires: &future}, true},
		{"封禁已到期", models.User{Banned: true, BanExpires: &expired}, false},
		{"只有到期时间但未打封禁标记", models.User{BanExpires: &future}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.user.IsBanned(now))
		})
	}
}

// TestRoleAndStatusConstants 校验角色与状态的取值与 schema 的 CHECK 约束一致。
func TestRoleAndStatusConstants(t *testing.T) {
	assert.Equal(t, enums.RoleUser, models.RoleUser)
	assert.Equal(t, enums.RoleAdmin, models.RoleAdmin)

	assert.Equal(t, enums.StatusActive, models.StatusActive)
	assert.Equal(t, enums.StatusInactive, models.StatusInactive)
	assert.Equal(t, enums.StatusBanned, models.StatusBanned)

	assert.Equal(t, "credential", models.ProviderCredential)
}

// TestActiveRecord_Interfaces 校验各实体对 Model 与 SoftDeletable 契约的静态实现。
func TestActiveRecord_Interfaces(t *testing.T) {
	var user models.User
	user.SetID("usr-123")
	assert.Equal(t, "usr-123", user.GetID())

	var model database.Model = &user
	assert.Equal(t, "usr-123", model.GetID())

	now := time.Now().UTC()
	var soft database.SoftDeletable = &user
	assert.Nil(t, soft.GetDeletedAt())
	soft.SetDeletedAt(&now)
	assert.Equal(t, &now, soft.GetDeletedAt())
	assert.Equal(t, now, user.UpdatedAt)

	var account models.Account
	account.SetID("acc-456")
	assert.Equal(t, "acc-456", account.GetID())
	var accModel database.Model = &account
	assert.Equal(t, "acc-456", accModel.GetID())

	var verification models.Verification
	verification.SetID("ver-789")
	assert.Equal(t, "ver-789", verification.GetID())
	var verModel database.Model = &verification
	assert.Equal(t, "ver-789", verModel.GetID())
}

// TestActiveRecord_UserQueryScopes 验证用户查询作用域与链式构造能力。
func TestActiveRecord_UserQueryScopes(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())

	t.Run("ActiveUsersQuery contains deleted_at filter", func(t *testing.T) {
		q := models.ActiveUsersQuery(db)
		sql := q.String()
		assert.Contains(t, sql, "deleted_at IS NULL")
	})

	t.Run("ApplyUserFilter and ApplyUserSort", func(t *testing.T) {
		filter := models.UserListFilter{
			Status: enums.StatusActive,
			Role:   enums.RoleAdmin,
			Search: "alice",
			Sort:   []string{"u.email ASC"},
		}
		q := models.ActiveUsersQuery(db)
		q = models.ApplyUserFilter(q, filter)
		q = models.ApplyUserSort(q, filter.Sort)
		sql := q.String()
		assert.Contains(t, sql, `status = 'active'`)
		assert.Contains(t, sql, `role = 'admin'`)
		assert.Contains(t, sql, `name ILIKE '%alice%'`)
		assert.Contains(t, sql, `ORDER BY "u"."email" ASC`)
	})
}

// TestActiveRecord_BaseHooks 校验 Base 模型的生命周期钩子与时间戳行为。
func TestActiveRecord_BaseHooks(t *testing.T) {
	ctx := context.Background()
	db := bun.NewDB(nil, pgdialect.New())

	t.Run("BeforeAppendModel generates UUIDv7 on insert", func(t *testing.T) {
		var base database.Base
		insertQ := db.NewInsert()
		err := base.BeforeAppendModel(ctx, insertQ)
		require.NoError(t, err)
		assert.NotEmpty(t, base.ID)
		assert.False(t, base.CreatedAt.IsZero())
		assert.False(t, base.UpdatedAt.IsZero())
		assert.Equal(t, base.CreatedAt, base.UpdatedAt)
	})

	t.Run("BeforeAppendModel preserves existing ID on insert", func(t *testing.T) {
		base := database.Base{ID: "custom-id-123"}
		insertQ := db.NewInsert()
		err := base.BeforeAppendModel(ctx, insertQ)
		require.NoError(t, err)
		assert.Equal(t, "custom-id-123", base.ID)
	})

	t.Run("BeforeAppendModel updates only UpdatedAt on update", func(t *testing.T) {
		created := time.Now().UTC().Add(-time.Hour)
		base := database.Base{
			ID:        "existing-id",
			CreatedAt: created,
			UpdatedAt: created,
		}
		updateQ := db.NewUpdate()
		err := base.BeforeAppendModel(ctx, updateQ)
		require.NoError(t, err)
		assert.Equal(t, "existing-id", base.ID)
		assert.Equal(t, created, base.CreatedAt)
		assert.True(t, base.UpdatedAt.After(created))
	})

	t.Run("UTCPtr helper converts timezone to UTC", func(t *testing.T) {
		assert.Nil(t, database.UTCPtr(nil))

		loc := time.FixedZone("CST", 8*3600)
		cstTime := time.Date(2026, 10, 1, 12, 0, 0, 0, loc)
		utcTime := database.UTCPtr(&cstTime)
		assert.NotNil(t, utcTime)
		assert.Equal(t, time.UTC, utcTime.Location())
		assert.Equal(t, 4, utcTime.Hour()) // 12:00 CST == 04:00 UTC
	})
}

// TestActiveRecord_CarbonHelpers 校验 Base 与 SoftDeletes 的 Carbon 时间转换辅助方法。
func TestActiveRecord_CarbonHelpers(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	t.Run("Base CreatedAtCarbon and UpdatedAtCarbon", func(t *testing.T) {
		var nilBase *database.Base
		assert.True(t, nilBase.CreatedAtCarbon().IsZero())
		assert.True(t, nilBase.UpdatedAtCarbon().IsZero())

		zeroBase := &database.Base{}
		assert.True(t, zeroBase.CreatedAtCarbon().IsZero())
		assert.True(t, zeroBase.UpdatedAtCarbon().IsZero())

		validBase := &database.Base{
			CreatedAt: now,
			UpdatedAt: now.Add(time.Hour),
		}
		assert.False(t, validBase.CreatedAtCarbon().IsZero())
		assert.Equal(t, 2026, validBase.CreatedAtCarbon().Year())
		assert.False(t, validBase.UpdatedAtCarbon().IsZero())
		assert.Equal(t, 13, validBase.UpdatedAtCarbon().Hour())
	})

	t.Run("SoftDeletes methods and DeletedAtCarbon", func(t *testing.T) {
		var nilSoft *database.SoftDeletes
		assert.Nil(t, nilSoft.GetDeletedAt())
		nilSoft.SetDeletedAt(&now)
		assert.True(t, nilSoft.DeletedAtCarbon().IsZero())

		zeroSoft := &database.SoftDeletes{}
		assert.Nil(t, zeroSoft.GetDeletedAt())
		assert.True(t, zeroSoft.DeletedAtCarbon().IsZero())

		zeroTime := time.Time{}
		zeroTimeSoft := &database.SoftDeletes{DeletedAt: &zeroTime}
		assert.True(t, zeroTimeSoft.DeletedAtCarbon().IsZero())

		validSoft := &database.SoftDeletes{}
		validSoft.SetDeletedAt(&now)
		assert.Equal(t, &now, validSoft.GetDeletedAt())
		assert.False(t, validSoft.DeletedAtCarbon().IsZero())
		assert.Equal(t, 12, validSoft.DeletedAtCarbon().Hour())
	})

	t.Run("DeletedAtCarbon top-level helper", func(t *testing.T) {
		assert.True(t, database.DeletedAtCarbon(nil).IsZero())

		user := &models.User{}
		assert.True(t, database.DeletedAtCarbon(user).IsZero())

		zeroTime := time.Time{}
		user.SetDeletedAt(&zeroTime)
		assert.True(t, database.DeletedAtCarbon(user).IsZero())

		user.SetDeletedAt(&now)
		c := database.DeletedAtCarbon(user)
		assert.False(t, c.IsZero())
		assert.Equal(t, 2026, c.Year())
	})
}

// TestActiveRecord_ContextDB 校验上下文数据库与事务的注入与透明感知。
func TestActiveRecord_ContextDB(t *testing.T) {
	ctx := context.Background()
	defaultDB := bun.NewDB(nil, pgdialect.New())
	txDB := bun.NewDB(nil, pgdialect.New())

	t.Run("ResolveDB returns fallback when context has no DB", func(t *testing.T) {
		resolved := database.ResolveDB(ctx, defaultDB)
		assert.Equal(t, defaultDB, resolved)
	})

	t.Run("ResolveDB returns context DB when injected via WithDB", func(t *testing.T) {
		scopedCtx := database.WithDB(ctx, txDB)
		assert.Equal(t, txDB, database.DBFromContext(scopedCtx))
		resolved := database.ResolveDB(scopedCtx, defaultDB)
		assert.Equal(t, txDB, resolved)
	})
}

// TestNormaliseEmailAndUsername 验证邮箱与用户名的规格化处理。
func TestNormaliseEmailAndUsername(t *testing.T) {
	assert.Equal(t, "user@example.com", models.NormaliseEmail("  User@Example.COM  "))
	assert.Equal(t, "alex_mercer", models.NormaliseUsername("  Alex_Mercer  "))
}

// TestMapUserUniqueConflict 验证把数据库唯一冲突错误映射为精准业务错误码。
func TestMapUserUniqueConflict(t *testing.T) {
	t.Run("邮箱冲突", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_user_email"}
		err := models.MapUserUniqueConflict(pgErr)
		var appErr *errs.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, errs.CodeAuthEmailAlreadyExists, appErr.Code())
	})

	t.Run("用户名冲突", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_user_username"}
		err := models.MapUserUniqueConflict(pgErr)
		var appErr *errs.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, errs.CodeAuthUsernameTaken, appErr.Code())
	})

	t.Run("手机号冲突", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_user_phone_number"}
		err := models.MapUserUniqueConflict(pgErr)
		var appErr *errs.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, errs.CodeAuthPhoneNumberTaken, appErr.Code())
	})

	t.Run("其他唯一索引冲突返回409", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_other"}
		err := models.MapUserUniqueConflict(pgErr)
		var appErr *errs.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, errs.CodeConflict, appErr.Code())
	})

	t.Run("非唯一约束错误返回内部错误", func(t *testing.T) {
		err := models.MapUserUniqueConflict(errors.New("connection lost"))
		var appErr *errs.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, errs.CodeInternal, appErr.Code())
	})
}

// TestPolicyAndGuardState 验证守卫策略与用户状态校验。
func TestPolicyAndGuardState(t *testing.T) {
	ctx := context.Background()

	t.Run("nil 用户校验失败", func(t *testing.T) {
		policy := models.ClientPolicy{}
		err := policy.ValidateUser(ctx, nil)
		require.Error(t, err)
	})

	t.Run("封禁用户校验失败", func(t *testing.T) {
		u := &models.User{Status: models.StatusBanned}
		policy := models.ClientPolicy{}
		err := policy.ValidateUser(ctx, u)
		require.Error(t, err)
	})

	t.Run("活跃普通用户通过 ClientPolicy", func(t *testing.T) {
		u := &models.User{Status: models.StatusActive, Role: enums.RoleUser}
		policy := models.ClientPolicy{}
		assert.NoError(t, policy.ValidateUser(ctx, u))
	})

	t.Run("普通用户无法通过 AdminPolicy", func(t *testing.T) {
		u := &models.User{Status: models.StatusActive, Role: enums.RoleUser}
		policy := models.AdminPolicy{}
		assert.ErrorIs(t, policy.ValidateUser(ctx, u), models.ErrRoleRestricted)
	})

	t.Run("管理员通过 AdminPolicy", func(t *testing.T) {
		u := &models.User{Status: models.StatusActive, Role: enums.RoleAdmin}
		policy := models.AdminPolicy{}
		assert.NoError(t, policy.ValidateUser(ctx, u))
	})

	t.Run("拥有 admin 角色的 Identity 通过 AdminPolicy", func(t *testing.T) {
		adminCtx := authentication.With(ctx, authentication.Identity{
			UserID: "usr_admin",
			Role:   string(enums.RoleAdmin),
		})
		u := &models.User{Status: models.StatusActive, Role: enums.RoleAdmin}
		policy := models.AdminPolicy{}
		assert.NoError(t, policy.ValidateUser(adminCtx, u))
	})
}

// TestReconcileBanState 验证 models.ReconcileBanState 的封禁状态协同。
func TestReconcileBanState(t *testing.T) {
	t.Run("状态为banned时自动设置banned为true", func(t *testing.T) {
		fields := map[string]any{
			"status": string(enums.StatusBanned),
		}
		models.ReconcileBanState(fields)
		assert.Equal(t, true, fields["banned"])
	})

	t.Run("banned为true时自动将状态改为banned", func(t *testing.T) {
		fields := map[string]any{
			"banned": true,
			"status": string(enums.StatusActive),
		}
		models.ReconcileBanState(fields)
		assert.Equal(t, string(enums.StatusBanned), fields["status"])
	})

	t.Run("banned为false且状态为active时保持不变", func(t *testing.T) {
		fields := map[string]any{
			"banned": false,
			"status": string(enums.StatusActive),
		}
		models.ReconcileBanState(fields)
		assert.Equal(t, false, fields["banned"])
		assert.Equal(t, string(enums.StatusActive), fields["status"])
	})
}
