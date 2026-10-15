package models

import (
	"context"
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
		Where("id = ?", id).
		Where("deleted_at IS NULL")
	for _, column := range cols {
		query = query.Set(column+" = ?", fields[column])
	}
	query = query.Set("updated_at = ?", time.Now().UTC()).Returning("*")

	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	return u, nil
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
		Where("deleted_at IS NULL").
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
		Where("deleted_at IS NULL").
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
		Where("deleted_at IS NULL").
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
		Where("deleted_at IS NULL").
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
		Where("id = ?", u.ID).
		Where("deleted_at IS NULL")
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
		Where("deleted_at IS NULL").
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
