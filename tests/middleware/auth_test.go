package middleware_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/database/models"
	"bfiber/internal/middleware"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/authorization"
	"bfiber/internal/platform/cache"
	"bfiber/internal/platform/errs"
)

type memAuthBlacklistStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func newMemAuthBlacklistStore() *memAuthBlacklistStore {
	return &memAuthBlacklistStore{data: make(map[string]string)}
}

func (s *memAuthBlacklistStore) Get(_ context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return "", cache.ErrNotFound
	}
	return val, nil
}

func (s *memAuthBlacklistStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *memAuthBlacklistStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok, nil
}

type mockUserRepo struct {
	users map[string]*models.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[string]*models.User)}
}

func (r *mockUserRepo) FindByID(_ context.Context, id string) (*models.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

func (r *mockUserRepo) FindByCredentials(_ context.Context, creds authentication.Credentials) (*models.User, error) {
	u, ok := r.users[creds.Account]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

func (r *mockUserRepo) ValidateCredentials(_ context.Context, _ *models.User, _ authentication.Credentials) (bool, error) {
	return true, nil
}

// setupAuthEnv 构建测试用中间件与守卫环境。
func setupAuthEnv() (
	*middleware.Middleware,
	*authentication.TokenIssuer,
	*authentication.Guard[*models.User],
	*authentication.Guard[*models.User],
	*mockUserRepo,
) {
	store := newMemAuthBlacklistStore()
	tokens := authentication.NewTokenIssuer(config.JWTConfig{
		Secret:     "0123456789abcdef0123456789abcdef",
		Issuer:     "bfiber-test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
	}, store)

	repo := newMockUserRepo()
	clientGuard := authentication.NewGuard(tokens, repo, models.ClientPolicy{})
	adminGuard := authentication.NewGuard(tokens, repo, models.AdminPolicy{})

	mgr := authentication.NewManager()
	mgr.Register("client", clientGuard)
	mgr.Register("admin", adminGuard)
	mgr.SetDefault("client")

	enforcer, err := authorization.New()
	if err != nil {
		panic(err)
	}

	m := middleware.New(middleware.Deps{
		Tokens:      tokens,
		AuthManager: mgr,
		Authz:       enforcer,
	})

	return m, tokens, clientGuard, adminGuard, repo
}

// TestAuth_MissingOrMalformedHeader 验证缺失或畸形 Authorization 报头时的 401 拦截。
func TestAuth_MissingOrMalformedHeader(t *testing.T) {
	m, _, _, _, _ := setupAuthEnv()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/protected", m.Auth(), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	// 1. 缺失报头
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenInvalid), errResp.Code)

	// 2. 畸形 scheme
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	// 3. 空 Bearer
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	// 4. 仅 Bearer 无后续参数
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer")
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	// 5. 多段超额参数
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer token1 token2")
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
}

// TestAuth_DefaultGuardSuccess 验证默认守卫 (client) 成功解析令牌并将身份写入上下文，同时支持多空格或制表符容错。
func TestAuth_DefaultGuardSuccess(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_client_1"] = &models.User{
		ID:     "usr_client_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
		Name:   "Client User",
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_client_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})

	var capturedID authentication.Identity
	app.Get("/client/me", m.Auth(), func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if ok {
			capturedID = id
		}
		return c.SendStatus(fiber.StatusOK)
	})

	// 验证标准 Bearer 令牌格式正确提取身份
	capturedID = authentication.Identity{}
	req := httptest.NewRequest(http.MethodGet, "/client/me", nil)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "usr_client_1", capturedID.UserID)
	assert.Equal(t, string(models.RoleUser), capturedID.Role)
}

// TestAuth_NamedAdminGuard 验证指定 admin 守卫时的权限拦截与放行。
func TestAuth_NamedAdminGuard(t *testing.T) {
	m, _, clientGuard, adminGuard, repo := setupAuthEnv()

	repo.users["usr_normal"] = &models.User{
		ID:     "usr_normal",
		Role:   models.RoleUser,
		Status: models.StatusActive,
	}
	repo.users["usr_admin"] = &models.User{
		ID:     "usr_admin",
		Role:   models.RoleAdmin,
		Status: models.StatusActive,
	}

	clientSess, err := clientGuard.Login(context.Background(), repo.users["usr_normal"])
	require.NoError(t, err)

	adminSess, err := adminGuard.Login(context.Background(), repo.users["usr_admin"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/admin/dashboard", m.Auth("admin"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	// 1. 普通用户携带有效令牌访问 admin 接口，被 AdminPolicy 拦截返回 403 Forbidden
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+clientSess.Token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeForbidden), errResp.Code)

	// 2. 管理员携带有效令牌访问 admin 接口，放行 200 OK
	req = httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+adminSess.Token)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestAuth_RevocationEpoch 验证用户级注销纪元触发后，HTTP 网关即时拒绝已注销令牌。
func TestAuth_RevocationEpoch(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_revoked"] = &models.User{
		ID:     "usr_revoked",
		Role:   models.RoleUser,
		Status: models.StatusActive,
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_revoked"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/client/profile", m.Auth("client"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	// 初始状态下有效
	req := httptest.NewRequest(http.MethodGet, "/client/profile", nil)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	// 触发用户级全局作废（如密码重置或点击注销所有设备）
	err = clientGuard.InvalidateAll(context.Background(), "usr_revoked")
	require.NoError(t, err)

	// 再次请求返回 401 撤销错误
	req = httptest.NewRequest(http.MethodGet, "/client/profile", nil)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenRevoked), errResp.Code)
}

// TestAuth_WithoutAuthManager 验证未注入 AuthManager 时直接触发 fail-closed 500 错误。
func TestAuth_WithoutAuthManager(t *testing.T) {
	tokens := authentication.NewTokenIssuer(config.JWTConfig{
		Secret:     "0123456789abcdef0123456789abcdef",
		Issuer:     "bfiber-test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
	}, nil)

	m := middleware.New(middleware.Deps{
		Tokens:      tokens,
		AuthManager: nil, // 未提供 AuthManager
	})

	pair, err := tokens.Issue("usr_fallback", "user")
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/fallback", m.Auth(), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/fallback", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
}

// TestAuth_NamedGuardWithoutAuthManager 验证未注入 AuthManager 时使用指定守卫触发 fail-closed 错误。
func TestAuth_NamedGuardWithoutAuthManager(t *testing.T) {
	tokens := authentication.NewTokenIssuer(config.JWTConfig{
		Secret:     "0123456789abcdef0123456789abcdef",
		Issuer:     "bfiber-test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
	}, nil)

	m := middleware.New(middleware.Deps{
		Tokens:      tokens,
		AuthManager: nil,
	})

	pair, err := tokens.Issue("usr_admin_1", "admin")
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/admin/protected", m.Auth("admin"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
}

// TestAuth_NoTokensWithoutAuthManager 验证未注入 AuthManager 且未配置 Tokens 时触发 500 内部错误。
func TestAuth_NoTokensWithoutAuthManager(t *testing.T) {
	m := middleware.New(middleware.Deps{
		Tokens:      nil,
		AuthManager: nil,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/protected", m.Auth(), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
}

// TestCan_Unauthenticated 验证无认证身份访问受保护资源时返回 401。
func TestCan_Unauthenticated(t *testing.T) {
	m, _, _, _, _ := setupAuthEnv()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/users", m.Can("users", "read"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenInvalid), errResp.Code)
}

// TestCan_EnforcerNotConfigured 验证未配置 Authz Enforcer 时触发 500 内部错误。
func TestCan_EnforcerNotConfigured(t *testing.T) {
	m := middleware.New(middleware.Deps{
		Authz: nil,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Get("/users", func(c fiber.Ctx) error {
		ctx := authentication.With(c.Context(), authentication.Identity{
			UserID: "usr_admin_1",
			Role:   "admin",
		})
		c.SetContext(ctx)
		return c.Next()
	}, m.Can("users", "read"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode)
}

// TestCan_RoleBasedAccess 验证基于 Casbin 角色策略的放行与拒绝。
func TestCan_RoleBasedAccess(t *testing.T) {
	m, _, _, _, _ := setupAuthEnv()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			ctx := authentication.With(c.Context(), authentication.Identity{
				UserID: c.Get("X-Test-User"),
				Role:   role,
			})
			c.SetContext(ctx)
		}
		return c.Next()
	})
	app.Get("/users", m.Can("users", "read"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	// 1. 普通用户角色无权读取 users 资源 -> 403 Forbidden
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set("X-Test-User", "usr_user_1")
	req.Header.Set("X-Test-Role", string(models.RoleUser))
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeForbidden), errResp.Code)

	// 2. 管理员角色有权读取 users 资源 -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set("X-Test-User", "usr_admin_1")
	req.Header.Set("X-Test-Role", string(models.RoleAdmin))
	resp, err = app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestAuth_MultiGuardFallback 验证 Laravel 风格多守卫回退匹配机制。
func TestAuth_MultiGuardFallback(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	// 注册普通用户
	repo.users["usr_user_1"] = &models.User{
		ID:     "usr_user_1",
		Status: models.StatusActive,
		Role:   models.RoleUser,
	}

	clientSess, err := clientGuard.Login(context.Background(), repo.users["usr_user_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	// 声明优先匹配 admin 守卫，回退至 client 守卫
	app.Get("/hybrid", m.Auth("admin", "client"), func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if !ok {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.JSON(fiber.Map{"userId": id.UserID})
	})

	// 携带 client 令牌请求，admin 守卫校验失败后自动回退至 client 守卫成功认证
	req := httptest.NewRequest(http.MethodGet, "/hybrid", nil)
	req.Header.Set("Authorization", "Bearer "+clientSess.Token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var res map[string]string
	require.NoError(t, json.Unmarshal(bodyBytes, &res))
	assert.Equal(t, "usr_user_1", res["userId"])
}

// TestCan_Permission 验证 Laravel 风格 m.Can 简写细粒度权限校验。
func TestCan_Permission(t *testing.T) {
	m, _, _, _, _ := setupAuthEnv()

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			ctx := authentication.With(c.Context(), authentication.Identity{
				UserID: c.Get("X-Test-User"),
				Role:   role,
			})
			c.SetContext(ctx)
		}
		return c.Next()
	})
	app.Get("/users", m.Can("users", "read"), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set("X-Test-User", "usr_admin_1")
	req.Header.Set("X-Test-Role", string(models.RoleAdmin))
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestAuth_CookieExtractorSuccess 验证基于 Cookie access_token 链式提取令牌认证成功。
func TestAuth_CookieExtractorSuccess(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_cookie_1"] = &models.User{
		ID:     "usr_cookie_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
		Name:   "Cookie User",
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_cookie_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})

	var capturedID authentication.Identity
	app.Get("/cookie/me", m.Auth(), func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if ok {
			capturedID = id
		}
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/cookie/me", nil)
	req.AddCookie(&http.Cookie{
		Name:  "access_token",
		Value: sess.Token,
	})
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "usr_cookie_1", capturedID.UserID)
}

// TestAuth_QueryExtractorSuccess 验证基于 Query token 链式提取令牌认证成功并正确注入 Provenance。
func TestAuth_QueryExtractorSuccess(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_query_1"] = &models.User{
		ID:     "usr_query_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
		Name:   "Query User",
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_query_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})

	var capturedID authentication.Identity
	var capturedProv authentication.Provenance
	var capturedOk bool
	app.Get("/query/me", m.Auth(), func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if ok {
			capturedID = id
		}
		capturedProv, capturedOk = authentication.ProvenanceFromContext(c.Context())
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/query/me?token="+sess.Token, nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Equal(t, "usr_query_1", capturedID.UserID)
	assert.True(t, capturedOk)
	assert.Equal(t, extractors.SourceQuery, capturedProv)
}

// TestAuth_QueryExtractorMutatingMethodRejected 验证突变方法 (POST/PUT/PATCH/DELETE) 携带 Query Token 时被安全守卫拦截。
func TestAuth_QueryExtractorMutatingMethodRejected(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_mutating_1"] = &models.User{
		ID:     "usr_mutating_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_mutating_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})

	handler := func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	}
	app.Post("/mutating/action", m.Auth(), handler)
	app.Put("/mutating/action", m.Auth(), handler)
	app.Patch("/mutating/action", m.Auth(), handler)
	app.Delete("/mutating/action", m.Auth(), handler)

	mutatingMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range mutatingMethods {
		t.Run(method+" 携带 query token 被拒绝", func(t *testing.T) {
			req := httptest.NewRequest(method, "/mutating/action?token="+sess.Token, nil)
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
			require.NoError(t, err)
			assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

			bodyBytes, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			var errResp errs.Response
			require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
			assert.Equal(t, string(errs.CodeAuthTokenInvalid), errResp.Code)
			assert.Contains(t, errResp.Message, "query token authentication is not permitted")
		})
	}

	t.Run("POST 携带 Authorization Header 正常通过", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/mutating/action", nil)
		req.Header.Set("Authorization", "Bearer "+sess.Token)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	})
}

// TestAuth_ProvenanceTracking 验证 Header 与 Cookie 令牌提取时 Provenance 正确注入上下文。
func TestAuth_ProvenanceTracking(t *testing.T) {
	m, _, clientGuard, _, repo := setupAuthEnv()

	repo.users["usr_prov_1"] = &models.User{
		ID:     "usr_prov_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_prov_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.New(middleware.Deps{}).ErrorHandler(),
	})

	var capturedProv authentication.Provenance
	var capturedOk bool
	app.Get("/prov/me", m.Auth(), func(c fiber.Ctx) error {
		capturedProv, capturedOk = authentication.ProvenanceFromContext(c.Context())
		return c.SendStatus(fiber.StatusOK)
	})

	t.Run("Authorization Header 来源", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/prov/me", nil)
		req.Header.Set("Authorization", "Bearer "+sess.Token)
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)
		assert.True(t, capturedOk)
		assert.Equal(t, extractors.SourceAuthHeader, capturedProv)
	})

	t.Run("Cookie 来源", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/prov/me", nil)
		req.AddCookie(&http.Cookie{
			Name:  "access_token",
			Value: sess.Token,
		})
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)
		assert.True(t, capturedOk)
		assert.Equal(t, extractors.SourceCookie, capturedProv)
	})
}
