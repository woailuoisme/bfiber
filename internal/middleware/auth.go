package middleware

import (
	"context"
	"errors"
	"strings"

	contribcasbin "github.com/gofiber/contrib/v3/casbin/v2"
	jwtware "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	platformsentry "bfiber/internal/platform/sentry"
)

// Auth 【路由/路由组中间件】100% 由 Fiber v3 官方 contrib/jwt (jwtware) 驱动，校验 JWT 签名与有效期。
// 校验通过后由 Guard 校验黑名单并加载用户实体与权限门禁，消除任何手动二次反序列化与冗余验签。
// 支持未指定（使用默认守卫）、单个守卫（如 "client"、"admin"）以及多守卫顺序回退（如 mw.Auth("admin", "client")）。
func (m *Middleware) Auth(guardNames ...string) fiber.Handler {
	if m == nil || m.authManager == nil || (m.tokens == nil && m.signingKey == "") {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: auth manager or tokens not configured"))
		}
	}

	guards := parseGuards(guardNames)

	cfg := m.resolveJWTConfig(nil)
	cfg.Claims = &authentication.Claims{}
	cfg.SuccessHandler = func(c fiber.Ctx) error {
		token := jwtware.FromContext(c)
		if token == nil || !token.Valid {
			return errs.Render(c, errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required"))
		}

		claims, ok := token.Claims.(*authentication.Claims)
		if !ok || claims == nil || claims.Subject == "" {
			return errs.Render(c, errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required"))
		}

		reqCtx, err := m.authenticateClaimsWithManager(c.Context(), claims, guards)
		if err != nil {
			return err
		}

		source := fiber.Locals[extractors.Source](c, authTokenProvenanceLocalsKey)
		if source == 0 {
			source = extractors.SourceAuthHeader
		}
		reqCtx = authentication.WithProvenance(reqCtx, source)

		if m.authz != nil {
			reqCtx = authentication.WithGate(reqCtx, m.authz)
		}
		c.SetContext(reqCtx)

		enrichSentryUser(c, reqCtx)

		return c.Next()
	}

	return jwtware.New(cfg)
}

// enrichSentryUser 将已认证用户身份与角色元信息同步绑定至 Sentry / GlitchTip 请求级作用域。
func enrichSentryUser(c fiber.Ctx, reqCtx context.Context) {
	id, ok := authentication.FromContext(reqCtx)
	if !ok {
		return
	}

	var email string
	if id.Claims != nil {
		if emailVal, found := id.Claims.GetClaim("email"); found {
			email, _ = emailVal.(string)
		}
	}

	platformsentry.SetUser(reqCtx, id.UserID, email, id.Role, c.IP())
	platformsentry.SetTag(reqCtx, "role", id.Role)
}

// authenticateClaimsWithManager 基于注入的 AuthManager 解析守卫并验证已解析的 Claims，消除重复验签与反序列化。
func (m *Middleware) authenticateClaimsWithManager(ctx context.Context, claims *authentication.Claims, guards []string) (context.Context, error) {
	if len(guards) == 0 {
		guard, err := m.authManager.Resolve()
		if err != nil {
			return nil, errs.Internal(err)
		}
		newCtx, _, authErr := guard.AuthenticateClaims(ctx, claims)
		if authErr != nil {
			return nil, authErr
		}
		return newCtx, nil
	}

	var lastErr error
	for _, gName := range guards {
		guard, err := m.authManager.Resolve(gName)
		if err != nil {
			lastErr = errs.Internal(err)
			continue
		}

		newCtx, _, authErr := guard.AuthenticateClaims(ctx, claims)
		if authErr == nil {
			return newCtx, nil
		}
		lastErr = authErr
	}

	if lastErr == nil {
		lastErr = errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required")
	}
	return nil, lastErr
}

// Can 【路由级中间件】基于 Casbin RBAC 细粒度权限校验。
func (m *Middleware) Can(resource, action string) fiber.Handler {
	if m.authz == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	return m.authz.RequirePermission(resource, action)
}

// RequiresPermissions 【路由级中间件】直接暴露 Fiber v3 官方 contrib/casbin/v2 的权限组匹配能力。
func (m *Middleware) RequiresPermissions(permissions []string, opts ...contribcasbin.Option) fiber.Handler {
	if m.authz == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	cbMw := m.authz.FiberMiddleware()
	if cbMw == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	return cbMw.RequiresPermissions(permissions, opts...)
}

// RequiresRoles 【路由级中间件】直接暴露 Fiber v3 官方 contrib/casbin/v2 的角色组匹配能力。
func (m *Middleware) RequiresRoles(roles []string, opts ...contribcasbin.Option) fiber.Handler {
	if m.authz == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	cbMw := m.authz.FiberMiddleware()
	if cbMw == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	return cbMw.RequiresRoles(roles, opts...)
}

// RoutePermission 【路由级中间件】直接暴露 Fiber v3 官方 contrib/casbin/v2 的路由路径映射权限校验能力。
func (m *Middleware) RoutePermission() fiber.Handler {
	if m.authz == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	cbMw := m.authz.FiberMiddleware()
	if cbMw == nil {
		return func(c fiber.Ctx) error {
			return errs.Internal(errors.New("middleware: authz enforcer not configured"))
		}
	}
	return cbMw.RoutePermission()
}

// parseGuards 解析可能存在的逗号分隔或变长守卫名称。
func parseGuards(guardNames []string) []string {
	var guards []string
	for _, gn := range guardNames {
		for part := range strings.SplitSeq(gn, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				guards = append(guards, part)
			}
		}
	}
	return guards
}
