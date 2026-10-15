package middleware_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/database/models"
	"bfiber/internal/middleware"
	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
)

func TestJWT_ValidToken(t *testing.T) {
	t.Parallel()

	m, tokens, _, _, _ := setupAuthEnv()
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	var capturedToken *jwt.Token
	app.Get("/jwt/protected", m.JWT(), func(c fiber.Ctx) error {
		capturedToken = middleware.JWTFromContext(c)
		return c.SendString("jwt-ok")
	})

	// 签发有效访问令牌
	pair, err := tokens.Issue("usr_jwt_1", "user")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/jwt/protected", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "jwt-ok", string(body))

	require.NotNil(t, capturedToken)
	assert.True(t, capturedToken.Valid)
	claims, ok := capturedToken.Claims.(jwt.MapClaims)
	require.True(t, ok)
	assert.Equal(t, "usr_jwt_1", claims["sub"])
}

func TestJWT_MissingToken(t *testing.T) {
	t.Parallel()

	m, _, _, _, _ := setupAuthEnv()
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/jwt/protected", m.JWT(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/jwt/protected", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenInvalid), errResp.Code)
}

func TestJWT_ExpiredToken(t *testing.T) {
	t.Parallel()

	m, tokens, _, _, _ := setupAuthEnv()
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/jwt/protected", m.JWT(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// 手动使用相同的 key 构造一个已经过期的 token
	secret := tokens.Secret()
	expiredClaims := jwt.MapClaims{
		"sub": "usr_expired",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims)
	tokenStr, err := token.SignedString(secret)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/jwt/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenExpired), errResp.Code)
}

func TestJWT_InvalidSignature(t *testing.T) {
	t.Parallel()

	m, _, _, _, _ := setupAuthEnv()
	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	app.Get("/jwt/protected", m.JWT(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// 使用错误密钥签署的 token
	wrongSecret := []byte("wrong_secret_key_0123456789abcde")
	claims := jwt.MapClaims{
		"sub": "usr_fake",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(wrongSecret)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/jwt/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnauthorized, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp errs.Response
	require.NoError(t, json.Unmarshal(bodyBytes, &errResp))
	assert.Equal(t, string(errs.CodeAuthTokenInvalid), errResp.Code)
}

func TestJWTAuth_EndToEnd(t *testing.T) {
	t.Parallel()

	m, _, clientGuard, _, repo := setupAuthEnv()
	repo.users["usr_jwt_auth_1"] = &models.User{
		ID:     "usr_jwt_auth_1",
		Role:   models.RoleUser,
		Status: models.StatusActive,
		Name:   "JWT Auth User",
	}

	sess, err := clientGuard.Login(context.Background(), repo.users["usr_jwt_auth_1"])
	require.NoError(t, err)

	app := fiber.New(fiber.Config{
		ErrorHandler: m.ErrorHandler(),
	})

	var capturedID authentication.Identity
	app.Get("/jwt-auth/me", m.Auth("client"), func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if ok {
			capturedID = id
		}
		return c.SendString("authenticated")
	})

	req := httptest.NewRequest(http.MethodGet, "/jwt-auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", string(body))

	assert.Equal(t, "usr_jwt_auth_1", capturedID.UserID)
	assert.Equal(t, string(models.RoleUser), capturedID.Role)
}
