package models

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/shared/enums"
)

// ProviderCredential 是本地密码账号的 provider_id。
// 一个 User 可以有多个 Account，但本地密码只能有一条。
const ProviderCredential = string(enums.ProviderCredential)

// Account 对应 "account" 表：一个 User 的一条登录途径。
type Account struct {
	bun.BaseModel `bun:"table:account,alias:a"`
	database.Base

	UserID                string     `bun:"user_id"`
	AccountID             string     `bun:"account_id"`
	ProviderID            string     `bun:"provider_id"`
	Password              *string    `bun:"password"`
	AccessToken           *string    `bun:"access_token"`
	RefreshToken          *string    `bun:"refresh_token"`
	IDToken               *string    `bun:"id_token"`
	AccessTokenExpiresAt  *time.Time `bun:"access_token_expires_at"`
	RefreshTokenExpiresAt *time.Time `bun:"refresh_token_expires_at"`
	Scope                 *string    `bun:"scope"`
}

var _ database.Model = (*Account)(nil)

// FindCredentialAccount 取某用户的本地密码凭据；不存在时返回 sql.ErrNoRows（优先从上下文感知活跃事务）。
func FindCredentialAccount(ctx context.Context, db bun.IDB, userID string) (*Account, error) {
	account := new(Account)
	err := database.ResolveDB(ctx, db).NewSelect().
		Model(account).
		Where("a.user_id = ?", userID).
		Where("a.provider_id = ?", ProviderCredential).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return account, nil
}

// FindCredentialPassword 获取指定用户的本地哈希密码指针。
func FindCredentialPassword(ctx context.Context, db bun.IDB, userID string) (*string, error) {
	account, err := FindCredentialAccount(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	return account.Password, nil
}

// UpdatePassword 改写本地密码凭据的哈希；凭据不存在时返回 sql.ErrNoRows（自动透传上下文活跃事务）。
func UpdatePassword(ctx context.Context, db bun.IDB, userID, passwordHash string) error {
	result, err := database.ResolveDB(ctx, db).NewUpdate().
		Model((*Account)(nil)).
		Set("password = ?", passwordHash).
		Set("updated_at = ?", time.Now().UTC()).
		Where("user_id = ?", userID).
		Where("provider_id = ?", ProviderCredential).
		Exec(ctx)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CreateUserWithCredential 在同一事务内创建用户与其本地密码凭据（自动复用上下文活跃事务）。
func CreateUserWithCredential(ctx context.Context, db bun.IDB, user *User, passwordHash string) error {
	return database.RunInTx(ctx, db, func(txCtx context.Context) error {
		idb := database.ResolveDB(txCtx, db)
		if _, err := idb.NewInsert().Model(user).Exec(txCtx); err != nil {
			return err
		}

		account := &Account{
			UserID:     user.ID,
			AccountID:  user.ID,
			ProviderID: ProviderCredential,
			Password:   &passwordHash,
		}
		_, err := idb.NewInsert().Model(account).Exec(txCtx)
		return err
	})
}

// Insert 将账号记录插入数据库。
func (a *Account) Insert(ctx context.Context, db bun.IDB) error {
	_, err := database.ResolveDB(ctx, db).NewInsert().Model(a).Exec(ctx)
	return err
}

// Delete 删除账号记录（执行物理硬删除）。
func (a *Account) Delete(ctx context.Context, db bun.IDB) error {
	_, err := database.ResolveDB(ctx, db).NewDelete().Model(a).WherePK().Exec(ctx)
	return err
}

// UpdatePassword 更新自身口令哈希（自动透传上下文活跃事务）。
func (a *Account) UpdatePassword(ctx context.Context, db bun.IDB, newHash string) error {
	now := time.Now().UTC()
	result, err := database.ResolveDB(ctx, db).NewUpdate().
		Model(a).
		Set("password = ?", newHash).
		Set("updated_at = ?", now).
		WherePK().
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	a.Password = &newHash
	a.UpdatedAt = now
	return nil
}
