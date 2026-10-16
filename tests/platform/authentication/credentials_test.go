package authentication_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/database/models"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/hasher"
)

const (
	credUserID      = "u_cred_1"
	credCurrentPass = "Current123!"
)

// credentialProvider 在通用 mock 之上补齐本地密码凭据能力（CredentialStore 扩展接口）。
type credentialProvider struct {
	*mockUserProvider
	passwords map[string]string
}

func newCredentialProvider(t *testing.T, password string) *credentialProvider {
	t.Helper()
	encoded, err := hasher.New().Make(password)
	require.NoError(t, err)

	base := newMockUserProvider()
	base.users[credUserID] = &models.User{ID: credUserID, Role: models.RoleUser, Status: models.StatusActive}

	return &credentialProvider{
		mockUserProvider: base,
		passwords:        map[string]string{credUserID: encoded},
	}
}

func (p *credentialProvider) FindCredentialPassword(_ context.Context, userID string) (*string, error) {
	encoded, ok := p.passwords[userID]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return &encoded, nil
}

func (p *credentialProvider) UpdatePassword(_ context.Context, userID, passwordHash string) error {
	if _, ok := p.passwords[userID]; !ok {
		return sql.ErrNoRows
	}
	p.passwords[userID] = passwordHash
	return nil
}

// newCredentialManager 构造以待测 Provider 为凭据仓储的主体密码凭据模块。
func newCredentialManager(provider authentication.UserProvider[*models.User]) *authentication.CredentialManager[*models.User] {
	guard := authentication.NewGuard(newTestTokenIssuer(15*time.Minute), provider, models.ClientPolicy{})
	return authentication.NewCredentialManager(guard, hasher.New())
}

// TestCredentialManagerVerifyPassword 校验口令核验把"凭据缺失"与"口令不匹配"归为同一 401 口径。
func TestCredentialManagerVerifyPassword(t *testing.T) {
	t.Parallel()

	provider := newCredentialProvider(t, credCurrentPass)
	manager := newCredentialManager(provider)
	ctx := t.Context()

	t.Run("口令正确放行", func(t *testing.T) {
		assert.NoError(t, manager.VerifyPassword(ctx, credUserID, credCurrentPass))
	})

	t.Run("口令不匹配返回凭据错误", func(t *testing.T) {
		err := manager.VerifyPassword(ctx, credUserID, "WrongPass123!")
		require.Error(t, err)
		_, resp := errs.Map(err)
		assert.Equal(t, string(errs.CodeAuthInvalidCredentials), resp.Code)
	})

	t.Run("凭据缺失与口令错误口径完全一致", func(t *testing.T) {
		err := manager.VerifyPassword(ctx, "u_no_credential", "AnyPass123!")
		require.Error(t, err)
		_, resp := errs.Map(err)
		assert.Equal(t, string(errs.CodeAuthInvalidCredentials), resp.Code, "不得通过错误差异区分账号是否设置过密码")
	})
}

// TestCredentialManagerChangePassword 校验改密的哈希写入、错误口径与凭据缺失的显式信号。
func TestCredentialManagerChangePassword(t *testing.T) {
	t.Parallel()

	const nextPass = "Next456Pass!"

	t.Run("改密成功写入新哈希", func(t *testing.T) {
		provider := newCredentialProvider(t, credCurrentPass)
		manager := newCredentialManager(provider)
		ctx := t.Context()

		require.NoError(t, manager.ChangePassword(ctx, credUserID, credCurrentPass, nextPass))

		encoded, err := provider.FindCredentialPassword(ctx, credUserID)
		require.NoError(t, err)
		matched, err := hasher.New().Check(*encoded, nextPass)
		require.NoError(t, err)
		assert.True(t, matched, "新口令必须可用于登录")

		stale, err := hasher.New().Check(*encoded, credCurrentPass)
		require.NoError(t, err)
		assert.False(t, stale, "旧口令必须失效")
	})

	t.Run("当前口令错误返回 422 口令不匹配", func(t *testing.T) {
		provider := newCredentialProvider(t, credCurrentPass)
		manager := newCredentialManager(provider)

		err := manager.ChangePassword(t.Context(), credUserID, "WrongCurrent123!", nextPass)
		require.Error(t, err)
		_, resp := errs.Map(err)
		assert.Equal(t, string(errs.CodeAuthPasswordMismatch), resp.Code)
	})

	t.Run("凭据缺失返回可判别的哨兵错误", func(t *testing.T) {
		provider := newCredentialProvider(t, credCurrentPass)
		manager := newCredentialManager(provider)

		err := manager.ChangePassword(t.Context(), "u_no_credential", credCurrentPass, nextPass)
		require.ErrorIs(t, err, authentication.ErrPasswordCredentialMissing)
	})
}

// TestCredentialManagerProviderWithoutCredentials 校验 Provider 缺少凭据能力时明确报装配错误，
// 而不是静默降级为"口令错误"。
func TestCredentialManagerProviderWithoutCredentials(t *testing.T) {
	t.Parallel()

	manager := newCredentialManager(newMockUserProvider())
	ctx := t.Context()

	verifyErr := manager.VerifyPassword(ctx, credUserID, credCurrentPass)
	require.Error(t, verifyErr)
	status, _ := errs.Map(verifyErr)
	assert.Equal(t, 500, status)

	changeErr := manager.ChangePassword(ctx, credUserID, credCurrentPass, "Next456Pass!")
	require.Error(t, changeErr)
	status, _ = errs.Map(changeErr)
	assert.Equal(t, 500, status)
}
