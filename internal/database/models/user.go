package models

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/platform/errs"
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
	DeletedAt           *time.Time   `bun:"deleted_at"`

	Accounts []*Account `bun:"rel:has-many,join:id=user_id"`
}

var (
	_ database.Model         = (*User)(nil)
	_ database.SoftDeletable = (*User)(nil)
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

// SearchableAs 返回 User 实体对应的搜索索引基础名称。
func (u *User) SearchableAs() string {
	return "users"
}

// SearchableKey 返回 User 实体的全局唯一搜索键。
func (u *User) SearchableKey() string {
	return u.ID
}

// ToSearchableArray 将用户模型映射为全文搜索文档字典。
func (u *User) ToSearchableArray() map[string]any {
	doc := map[string]any{
		"id":             u.ID,
		"name":           u.Name,
		"email":          u.Email,
		"role":           string(u.Role),
		"status":         string(u.Status),
		"email_verified": u.EmailVerified,
		"created_at":     u.CreatedAt.Unix(),
		"updated_at":     u.UpdatedAt.Unix(),
	}
	if u.Username != nil {
		doc["username"] = *u.Username
	}
	if u.DisplayUsername != nil {
		doc["display_username"] = *u.DisplayUsername
	}
	if u.PhoneNumber != nil {
		doc["phone_number"] = *u.PhoneNumber
	}
	return doc
}

// GetModelType 返回多态媒体附件的模型类型标识。
func (u *User) GetModelType() string {
	return "user"
}

// GetModelID 返回多态媒体附件的宿主主键 ID。
func (u *User) GetModelID() string {
	return u.ID
}

// GetAuthIdentifier 返回认证主体唯一标识符（用户 ID）。
func (u *User) GetAuthIdentifier() string {
	if u == nil {
		return ""
	}
	return u.ID
}

// GetAuthRole 返回认证主体的权限角色。
func (u *User) GetAuthRole() string {
	if u == nil {
		return ""
	}
	return string(u.Role)
}

// HasVerifiedEmail 判断用户邮箱是否已通过验证。
func (u *User) HasVerifiedEmail() bool {
	if u == nil {
		return false
	}
	return u.EmailVerified
}

// GetJWTCustomClaims 返回需要嵌入 JWT 载荷的自定义声明字典。
func (u *User) GetJWTCustomClaims() map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{
		"email_verified": u.EmailVerified,
	}
}

// RouteNotificationFor 根据通知渠道返回接收目标标识（满足 contract.Notifiable 契约）。
func (u *User) RouteNotificationFor(channel string) string {
	if u == nil {
		return ""
	}
	switch channel {
	case "mail":
		return u.Email
	case "sms":
		if u.PhoneNumber != nil {
			return *u.PhoneNumber
		}
	}
	return ""
}

// RouteNotificationForMail 返回接收邮件通知的目标邮箱。
func (u *User) RouteNotificationForMail() string {
	if u == nil {
		return ""
	}
	return u.Email
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
	raw = cmp.Or(strings.TrimSpace(raw), "-createdAt")

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
		col, ok := UserSortColumns[part]
		if !ok {
			continue
		}
		orders = append(orders, col+" "+direction)
	}
	if len(orders) == 0 {
		return []string{"u.created_at DESC"}
	}
	return orders
}

// UserWritableColumns 是允许经 UpdateUserFields 写入的列白名单。
var UserWritableColumns = map[string]bool{
	"name":             true,
	"image":            true,
	"username":         true,
	"display_username": true,
	"phone_number":     true,
	// 以下受控列仅管理端可写。
	"role":           false,
	"status":         false,
	"banned":         false,
	"ban_reason":     false,
	"ban_expires":    false,
	"email_verified": false,
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
		Apply(ScopeNotDeleted).
		Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
			return ApplyUserFilter(sq, filter)
		}).
		Apply(ScopeSort(filter.Sort))
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
		Apply(ScopeNotDeleted).
		Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
			return ApplyUserFilter(sq, filter)
		}).
		Apply(ScopeSort(filter.Sort))
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
		Apply(ScopeNotDeleted).
		Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
			return ApplyUserFilter(sq, filter)
		})
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

// ReconcileBanState 保证 banned 布尔值、status 账号状态与理由、过期时间相互协同一致。
func ReconcileBanState(fields map[string]any) {
	if statusVal, ok := fields["status"]; ok {
		switch s := statusVal.(type) {
		case string:
			if s == string(enums.StatusBanned) {
				fields["banned"] = true
			}
		case enums.UserStatus:
			if s == enums.UserStatusBanned {
				fields["banned"] = true
			}
		}
	}

	bannedVal, ok := fields["banned"]
	if !ok {
		return
	}
	banned, ok := bannedVal.(bool)
	if !ok {
		return
	}
	if banned {
		fields["status"] = string(enums.StatusBanned)
		if reason, has := fields["ban_reason"]; !has || reason == nil || reason == "" {
			fields["ban_reason"] = "Banned by administrator"
		}
		return
	}
	fields["ban_reason"] = nil
	fields["ban_expires"] = nil
}

// UpdateUserFields 按列名更新用户并返回最新实体。列名必须落在 UserWritableColumns 白名单内，否则直接报错。
func UpdateUserFields(ctx context.Context, db bun.IDB, id string, fields map[string]any) (*User, error) {
	if len(fields) == 0 {
		return FindUserByID(ctx, db, id)
	}

	ReconcileBanState(fields)

	cols := make([]string, 0, len(fields))
	for column := range fields {
		if _, ok := UserWritableColumns[column]; !ok {
			return nil, errs.BadRequest("Disallowed user column for update: " + column)
		}
		cols = append(cols, column)
	}
	slices.Sort(cols)

	query := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Where("id = ?", id).
		Where("deleted_at IS NULL")
	for _, column := range cols {
		query = query.Set(column+" = ?", fields[column])
	}
	query = query.Set("updated_at = ?", time.Now().UTC())

	result, err := query.Exec(ctx)
	if err != nil {
		return nil, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	return FindUserByID(ctx, db, id)
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
	now := time.Now().UTC()
	res, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("deleted_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", u.ID).
		Where("deleted_at IS NULL").
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
	u.DeletedAt = &now
	u.UpdatedAt = now
	return nil
}

// SoftDelete 执行软删除（同 Delete）。
func (u *User) SoftDelete(ctx context.Context, db bun.IDB) error {
	return u.Delete(ctx, db)
}

// MarkEmailVerified 置位邮箱验证状态。
func (u *User) MarkEmailVerified(ctx context.Context, db bun.IDB) error {
	now := time.Now().UTC()
	_, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("email_verified = ?", true).
		Set("updated_at = ?", now).
		Where("id = ?", u.ID).
		Exec(ctx)
	if err == nil {
		u.EmailVerified = true
		u.UpdatedAt = now
	}
	return err
}

// RecordLogin 按用户 ID 刷新最近登录时间与登录方式。
func RecordLogin(ctx context.Context, db bun.IDB, userID, method string) error {
	now := time.Now().UTC()
	_, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("last_login_at = ?", now).
		Set("last_login_method = ?", method).
		Where("id = ?", userID).
		Exec(ctx)
	return err
}

// RecordLogin 刷新最近登录时间与登录方式。
func (u *User) RecordLogin(ctx context.Context, db bun.IDB, method string) error {
	now := time.Now().UTC()
	_, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("last_login_at = ?", now).
		Set("last_login_method = ?", method).
		Where("id = ?", u.ID).
		Exec(ctx)
	if err == nil {
		u.LastLoginAt = &now
		u.LastLoginMethod = &method
	}
	return err
}

// Ban 封禁用户。
func (u *User) Ban(ctx context.Context, db bun.IDB, reason string, expiresAt *time.Time) error {
	now := time.Now().UTC()
	q := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("banned = ?", true).
		Set("status = ?", StatusBanned).
		Set("ban_reason = ?", reason).
		Set("ban_expires = ?", expiresAt).
		Set("updated_at = ?", now).
		Where("id = ?", u.ID)
	_, err := q.Exec(ctx)
	if err == nil {
		u.Banned = true
		u.Status = StatusBanned
		u.BanReason = &reason
		u.BanExpires = expiresAt
		u.UpdatedAt = now
	}
	return err
}

// Unban 解封用户。
func (u *User) Unban(ctx context.Context, db bun.IDB) error {
	now := time.Now().UTC()
	_, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("banned = ?", false).
		Set("status = ?", StatusActive).
		Set("ban_reason = NULL").
		Set("ban_expires = NULL").
		Set("updated_at = ?", now).
		Where("id = ?", u.ID).
		Exec(ctx)
	if err == nil {
		u.Banned = false
		u.Status = StatusActive
		u.BanReason = nil
		u.BanExpires = nil
		u.UpdatedAt = now
	}
	return err
}

// uniqueViolation 是 PostgreSQL 的唯一约束冲突错误码。
const uniqueViolation = "23505"

// 唯一索引名，用于把冲突翻译成具体业务错误码。
const (
	indexUserEmail       = "idx_user_email"
	indexUserUsername    = "idx_user_username"
	indexUserPhoneNumber = "idx_user_phone_number"
)

// NormaliseEmail 统一邮箱大小写与首尾空白，与库中 idx_user_email 的口径一致。
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormaliseUsername 统一用户名大小写与首尾空白，与库中 idx_user_username 的口径一致。
func NormaliseUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// CheckUsernameAvailable 校验用户名是否已被其他用户占用。
func CheckUsernameAvailable(ctx context.Context, db bun.IDB, userID string, username string) error {
	normalised := NormaliseUsername(username)
	existing, err := FindUserByUsername(ctx, db, normalised)
	switch {
	case err == nil && existing.ID != userID:
		return errs.New(errs.CodeAuthUsernameTaken, "The username has already been taken")
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return errs.Internal(err)
	}
	return nil
}

// MapUserUniqueConflict 把唯一索引冲突翻译成具体的业务错误码。
func MapUserUniqueConflict(err error) error {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != uniqueViolation {
		return errs.Internal(err)
	}

	switch pgErr.ConstraintName {
	case indexUserEmail:
		return errs.New(errs.CodeAuthEmailAlreadyExists, "An account with this email address already exists")
	case indexUserUsername:
		return errs.New(errs.CodeAuthUsernameTaken, "The username has already been taken")
	case indexUserPhoneNumber:
		return errs.New(errs.CodeAuthPhoneNumberTaken, "The phone number has already been taken")
	default:
		return errs.Conflict("The user conflicts with an existing record")
	}
}

// GuardState 校验用户的通用封禁与活跃状态。
func (u *User) GuardState() error {
	if u == nil {
		return errs.Unauthorized(errs.CodeAuthTokenInvalid, "The token is invalid")
	}
	if u.IsBanned(time.Now().UTC()) || u.Status == StatusBanned {
		return errs.New(errs.CodeAuthAccountBanned, "The account has been banned")
	}
	if u.Status != StatusActive {
		return errs.Forbidden("The account is not active")
	}
	return nil
}

// GuardUserState 校验指定用户的封禁与活跃状态函数。
func GuardUserState(user *User) error {
	return user.GuardState()
}
