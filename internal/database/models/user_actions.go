package models

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/platform/errs"
	"bfiber/internal/shared/enums"
)

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

	u := new(User)
	query := database.ResolveDB(ctx, db).NewUpdate().
		Model(u).
		Where("id = ?", id)
	for _, column := range cols {
		query = query.Set(column+" = ?", fields[column])
	}
	query = query.Set("updated_at = ?", time.Now().UTC()).Returning("*")

	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// ApplyUserPatch 应用用户局部更新补丁：先做用户名占用校验，再写入字段并归一化错误语义。
//
// 返回的是领域错误码（目标不存在、唯一冲突），HTTP 状态由 errs.Render 依码派生——本包不感知投递层。
// 映射收敛在此而非各切片，是因为两个调用方（管理端与本人档案）需要完全相同的判定顺序，
// 分散就会重新出现两份会漂移的错误映射。
// 单条 UPDATE ... RETURNING 本身即原子操作，占用校验只是提前失败，唯一索引才是最终防线。
func ApplyUserPatch(ctx context.Context, db bun.IDB, id string, fields map[string]any) (*User, error) {
	if username, ok := fields["username"].(string); ok && username != "" {
		if err := CheckUsernameAvailable(ctx, db, id, username); err != nil {
			return nil, err
		}
	}

	updated, err := UpdateUserFields(ctx, db, id, fields)
	if err != nil {
		if errs.IsNoRows(err) {
			return nil, errs.UserNotFound()
		}
		return nil, MapUserUniqueConflict(err)
	}
	return updated, nil
}

// DeleteUser 软删除指定用户；目标不存在或已被删除时返回 sql.ErrNoRows。
// 与同包其他写入器一致，只透出驱动层信号，对外语义由调用方经 errs.From 决定。
func DeleteUser(ctx context.Context, db bun.IDB, id string) error {
	res, err := database.ResolveDB(ctx, db).NewDelete().
		Model((*User)(nil)).
		Where("id = ?", id).
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
	return nil
}

// SetUserAvatar 按用户 ID 更新其头像链接并返回最新实体。
func SetUserAvatar(ctx context.Context, db bun.IDB, id, avatarURL string) (*User, error) {
	u := new(User)
	now := time.Now().UTC()
	err := database.ResolveDB(ctx, db).NewUpdate().
		Model(u).
		Set("image = ?", avatarURL).
		Set("updated_at = ?", now).
		Where("id = ?", id).
		Returning("*").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SetAvatar 在用户聚合上直接更新头像链接。
func (u *User) SetAvatar(ctx context.Context, db bun.IDB, avatarURL string) error {
	now := time.Now().UTC()
	_, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*User)(nil)).
		Set("image = ?", avatarURL).
		Set("updated_at = ?", now).
		Where("id = ?", u.ID).
		Exec(ctx)
	if err == nil {
		u.Image = &avatarURL
		u.UpdatedAt = now
	}
	return err
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
	if err := RecordLogin(ctx, db, u.ID, method); err != nil {
		return err
	}
	u.LastLoginAt = &now
	u.LastLoginMethod = &method
	return nil
}

// GuardState 校验用户的通用封禁与活跃状态。
func (u *User) GuardState() error {
	if u == nil {
		return errs.TokenInvalid()
	}
	if u.IsBanned(time.Now().UTC()) || u.Status == StatusBanned {
		return errs.New(errs.CodeAuthAccountBanned, "The account has been banned")
	}
	if u.Status != StatusActive {
		return errs.Forbidden("The account is not active")
	}
	return nil
}
