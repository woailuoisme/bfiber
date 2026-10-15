package models

import (
	"context"
	"database/sql"
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

	Accounts []*Account `bun:"rel:has-many,join:id=user_id"`
	Media    []*Media   `bun:"rel:has-many,join:id=model_id"`
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

// AvatarMedia 返回用户关联的头像媒体资源，未设置或无头像时返回 nil。
func (u *User) AvatarMedia() *Media {
	if u == nil {
		return nil
	}
	for _, m := range u.Media {
		if m != nil && m.CollectionName == "avatar" {
			return m
		}
	}
	return nil
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

// UserListFilter 是用户列表查询的过滤条件。
type UserListFilter struct {
	Search string
	Status enums.Status
	Role   enums.Role
	Sort   []string
}

// UserSortColumns 是允许排序的字段白名单，未列出的字段一律忽略以杜绝注入。
var UserSortColumns = map[string]string{
	"createdAt":   "u.created_at",
	"updatedAt":   "u.updated_at",
	"email":       "u.email",
	"name":        "u.name",
	"lastLoginAt": "u.last_login_at",
}

// NormaliseUserSort 把 API 的排序表达式翻译成白名单内的列与方向。
// 缺省或全部非法时退回默认排序，保证分页顺序稳定。
func NormaliseUserSort(raw string) []string {
	sorter := database.Sorter{
		Allowed: UserSortColumns,
		Default: "u.created_at DESC",
	}
	return sorter.Parse(raw)
}

// ScopeNotDeleted 排除软删除记录作用域。
func ScopeNotDeleted(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("u.deleted_at IS NULL")
}

// ScopeActive 活跃状态用户作用域。
func ScopeActive(q *bun.SelectQuery) *bun.SelectQuery {
	return q.Where("u.status = ?", enums.StatusActive)
}

// ScopeRole 按角色过滤作用域。
func ScopeRole(role enums.Role) func(*bun.SelectQuery) *bun.SelectQuery {
	return func(q *bun.SelectQuery) *bun.SelectQuery {
		if role != "" {
			return q.Where("u.role = ?", role)
		}
		return q
	}
}

// ScopeStatus 按状态过滤作用域。
func ScopeStatus(status enums.Status) func(*bun.SelectQuery) *bun.SelectQuery {
	return func(q *bun.SelectQuery) *bun.SelectQuery {
		if status != "" {
			return q.Where("u.status = ?", status)
		}
		return q
	}
}

// ScopeSearch 按关键词模糊检索用户。
func ScopeSearch(search string) func(*bun.SelectQuery) *bun.SelectQuery {
	return func(q *bun.SelectQuery) *bun.SelectQuery {
		trimmed := strings.TrimSpace(search)
		if trimmed != "" {
			pattern := "%" + escapeLike(trimmed) + "%"
			return q.Where("(u.name ILIKE ? OR u.email ILIKE ? OR u.username ILIKE ?)", pattern, pattern, pattern)
		}
		return q
	}
}

// ScopeSort 针对用户字段白名单安全排序。
func ScopeSort(sorts []string) func(*bun.SelectQuery) *bun.SelectQuery {
	return func(q *bun.SelectQuery) *bun.SelectQuery {
		return ApplyUserSort(q, sorts)
	}
}

// ActiveUsersQuery 返回已排除软删除记录的基础查询。
func ActiveUsersQuery(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().Model((*User)(nil)).Apply(ScopeNotDeleted)
}

// ApplyUserFilter 应用用户状态、角色、检索关键词过滤。
func ApplyUserFilter(q *bun.SelectQuery, filter UserListFilter) *bun.SelectQuery {
	return q.
		Apply(ScopeStatus(filter.Status)).
		Apply(ScopeRole(filter.Role)).
		Apply(ScopeSearch(filter.Search))
}

// ApplyUserSort 针对用户字段白名单安全排序。
func ApplyUserSort(q *bun.SelectQuery, sorts []string) *bun.SelectQuery {
	if len(sorts) == 0 {
		return q.Order("u.created_at DESC")
	}
	for _, order := range sorts {
		q = q.Order(order)
	}
	return q
}

// FindUserByID 按主键查找用户（优先从上下文感知活跃事务）。
func FindUserByID(ctx context.Context, db bun.IDB, id string) (*User, error) {
	u := new(User)
	err := database.ResolveDB(ctx, db).NewSelect().
		Model(u).
		Where("u.id = ?", id).
		Apply(ScopeNotDeleted).
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
		Apply(ScopeNotDeleted).
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
		Apply(ScopeNotDeleted).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ListUsers 按条件分页查询用户（优先从上下文感知活跃事务）。
func ListUsers(ctx context.Context, db bun.IDB, filter UserListFilter, offset, limit int) ([]User, error) {
	users := make([]User, 0)
	q := database.ResolveDB(ctx, db).NewSelect().
		Model(&users).
		Apply(ScopeNotDeleted)
	q = ApplyUserFilter(q, filter)
	q = ApplyUserSort(q, filter.Sort)
	if offset > 0 {
		q = q.Offset(int64(offset))
	}
	if limit > 0 {
		q = q.Limit(int64(limit))
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return users, nil
}

// ListUsersAndCount 分页查询用户并返回总数（优先从上下文感知活跃事务）。
func ListUsersAndCount(ctx context.Context, db bun.IDB, filter UserListFilter, page, pageSize int) ([]User, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}
	users := make([]User, 0)
	q := database.ResolveDB(ctx, db).NewSelect().
		Model(&users).
		Apply(ScopeNotDeleted)
	q = ApplyUserFilter(q, filter)
	q = ApplyUserSort(q, filter.Sort)
	total, err := q.Offset(int64((page - 1) * pageSize)).Limit(int64(pageSize)).ScanAndCount(ctx)
	if err != nil {
		return nil, 0, err
	}
	return users, int(total), nil
}

// CountUsers 统计满足条件的用户数（优先从上下文感知活跃事务）。
func CountUsers(ctx context.Context, db bun.IDB, filter UserListFilter) (int, error) {
	q := database.ResolveDB(ctx, db).NewSelect().
		Model((*User)(nil)).
		Apply(ScopeNotDeleted)
	q = ApplyUserFilter(q, filter)
	cnt, err := q.Count(ctx)
	return int(cnt), err
}

// CountActiveUsers 统计未删除用户数，供健康检查与 db:seed 使用（优先从上下文感知活跃事务）。
func CountActiveUsers(ctx context.Context, db bun.IDB) (int, error) {
	cnt, err := database.ResolveDB(ctx, db).NewSelect().
		Model((*User)(nil)).
		Apply(ScopeNotDeleted).
		Count(ctx)
	return int(cnt), err
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
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
	res, err := database.ResolveDB(ctx, db).NewDelete().
		Model(u).
		WherePK().
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	now := time.Now().UTC()
	u.DeletedAt = &now
	return nil
}

// SoftDelete 执行软删除（同 Delete）。
func (u *User) SoftDelete(ctx context.Context, db bun.IDB) error {
	return u.Delete(ctx, db)
}
