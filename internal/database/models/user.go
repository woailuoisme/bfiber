package models

import (
	"context"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/shared/enums"
)

// 用户角色枚举别名，取值统一定义于 internal/shared/enums。
const (
	RoleUser  = enums.RoleUser
	RoleAdmin = enums.RoleAdmin
)

// 用户账号状态枚举别名，取值统一定义于 internal/shared/enums。
const (
	StatusActive   = enums.StatusActive
	StatusInactive = enums.StatusInactive
	StatusBanned   = enums.StatusBanned
)

// User 映射系统用户表 ("user")。
type User struct {
	bun.BaseModel `bun:"table:user,alias:u"`
	database.Base

	Name                string       `bun:"name"`
	Email               string       `bun:"email"`
	EmailVerified       bool         `bun:"email_verified"`
	Image               *string      `bun:"image"`
	Role                enums.Role   `bun:"role"`
	Status              enums.Status `bun:"status"`
	Banned              bool         `bun:"banned"`
	TwoFactorEnabled    bool         `bun:"two_factor_enabled"`
	Username            *string      `bun:"username"`
	DisplayUsername     *string      `bun:"display_username"`
	PhoneNumber         *string      `bun:"phone_number"`
	PhoneNumberVerified bool         `bun:"phone_number_verified"`
	IsAnonymous         bool         `bun:"is_anonymous"`
	LastLoginMethod     *string      `bun:"last_login_method"`
	BanReason           *string      `bun:"ban_reason"`
	BanExpires          *time.Time   `bun:"ban_expires"`
	LastLoginAt         *time.Time   `bun:"last_login_at"`
	DeletedAt           *time.Time   `bun:"deleted_at,soft_delete"`
}

var (
	_ database.Model         = (*User)(nil)
	_ database.SoftDeletable = (*User)(nil)
	_ bun.BeforeInsertHook   = (*User)(nil)
	_ bun.BeforeUpdateHook   = (*User)(nil)
)

// GetDeletedAt 返回用户软删除时间指针（满足 SoftDeletable 契约）。
func (u *User) GetDeletedAt() *time.Time {
	if u == nil {
		return nil
	}
	return u.DeletedAt
}

// SetDeletedAt 设置用户软删除时间（满足 SoftDeletable 契约）。
func (u *User) SetDeletedAt(t *time.Time) {
	if u != nil {
		u.DeletedAt = t
		if t != nil {
			u.UpdatedAt = *t
		}
	}
}

// IsBanned 判定账号是否处于封禁状态，已过期的封禁视为解除。
func (u *User) IsBanned(now time.Time) bool {
	if !u.Banned {
		return false
	}
	return u.BanExpires == nil || u.BanExpires.After(now)
}

// ReconcileBanState 调和用户封禁状态不变量。
func (u *User) ReconcileBanState() {
	if u == nil {
		return
	}
	if u.Status == enums.StatusBanned {
		u.Banned = true
	}
	if u.Banned {
		u.Status = enums.StatusBanned
		if u.BanReason == nil || *u.BanReason == "" {
			reason := "Banned by administrator"
			u.BanReason = &reason
		}
		return
	}
	u.BanReason = nil
	u.BanExpires = nil
}

// BeforeInsert 挂载 Bun 实体写入前钩子。
func (u *User) BeforeInsert(_ context.Context, _ *bun.InsertQuery) error {
	u.ReconcileBanState()
	return nil
}

// BeforeUpdate 挂载 Bun 实体更新前钩子。
func (u *User) BeforeUpdate(_ context.Context, _ *bun.UpdateQuery) error {
	u.ReconcileBanState()
	return nil
}

// UserSortColumns 是允许排序的字段白名单，未列出的字段一律忽略以杜绝注入。
var UserSortColumns = map[string]string{
	"createdAt":   "u.created_at",
	"updatedAt":   "u.updated_at",
	"email":       "u.email",
	"name":        "u.name",
	"lastLoginAt": "u.last_login_at",
}

// FindUserByID 按主键查找用户（优先从上下文感知活跃事务）。
// 软删除记录由 Bun 依据 soft_delete 标签自动排除。
func FindUserByID(ctx context.Context, db bun.IDB, id string) (*User, error) {
	u := new(User)
	err := database.ResolveDB(ctx, db).NewSelect().
		Model(u).
		Where("u.id = ?", id).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// FindUserByUsername 按用户名大小写不敏感查找用户（优先从上下文感知活跃事务）。
func FindUserByUsername(ctx context.Context, db bun.IDB, username string) (*User, error) {
	clean := strings.ToLower(strings.TrimSpace(username))
	u := new(User)
	err := database.ResolveDB(ctx, db).NewSelect().
		Model(u).
		Where("u.username IS NOT NULL").
		Where("LOWER(u.username) = ?", clean).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// FindUserByEmail 按邮箱大小写不敏感查找用户（优先从上下文感知活跃事务）。
func FindUserByEmail(ctx context.Context, db bun.IDB, email string) (*User, error) {
	clean := strings.ToLower(strings.TrimSpace(email))
	u := new(User)
	err := database.ResolveDB(ctx, db).NewSelect().
		Model(u).
		Where("LOWER(u.email) = ?", clean).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CountActiveUsers 统计未删除用户数，供健康检查与 db:seed 使用（优先从上下文感知活跃事务）。
func CountActiveUsers(ctx context.Context, db bun.IDB) (int, error) {
	cnt, err := database.ResolveDB(ctx, db).NewSelect().
		Model((*User)(nil)).
		Count(ctx)
	return int(cnt), err
}

// Insert 将用户记录插入数据库。
func (u *User) Insert(ctx context.Context, db bun.IDB) error {
	_, err := database.ResolveDB(ctx, db).NewInsert().Model(u).Exec(ctx)
	return err
}

// Update 更新指定列或全量更新。
func (u *User) Update(ctx context.Context, db bun.IDB, columns ...string) error {
	q := database.ResolveDB(ctx, db).NewUpdate().Model(u).WherePK()
	if len(columns) > 0 {
		q = q.Column(columns...)
	}
	_, err := q.Exec(ctx)
	return err
}

// Delete 删除用户记录（执行软删除）。
func (u *User) Delete(ctx context.Context, db bun.IDB) error {
	if err := DeleteUser(ctx, db, u.ID); err != nil {
		return err
	}
	now := time.Now().UTC()
	u.DeletedAt = &now
	return nil
}

// SoftDelete 执行软删除（同 Delete）。
func (u *User) SoftDelete(ctx context.Context, db bun.IDB) error {
	return u.Delete(ctx, db)
}
