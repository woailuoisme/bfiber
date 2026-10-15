// Package authorization 提供基于 Casbin 的高性能 RBAC 权限授权与 Gate 门禁能力。
package authorization

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"

	"github.com/casbin/casbin/v3"
	"github.com/casbin/casbin/v3/model"
	stringadapter "github.com/casbin/casbin/v3/persist/string-adapter"
	contribcasbin "github.com/gofiber/contrib/v3/casbin/v2"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
)

// 常用资源与动作常量。
const (
	ResourceUsers = "users"

	ActionCreate = "create"
	ActionList   = "list"
	ActionRead   = "read"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// Resource 辅助函数，便捷返回资源名称字符串。
func Resource(name string) string {
	return name
}

//go:embed rbac/model.conf rbac/policy.csv
var rbacFS embed.FS

// Enforcer 判断角色对资源的操作权限，同时完整实现 authentication.Gate 契约。
type Enforcer struct {
	enforcer         *casbin.Enforcer
	defaultContribMw *contribcasbin.Middleware // 预编译单例中间件适配器，零每请求堆分配
}

var _ authentication.Gate = (*Enforcer)(nil)

// New 从内嵌策略文件构造 Enforcer 并预编译 Casbin 引擎与中间件适配器。
func New() (*Enforcer, error) {
	modelText, err := fs.ReadFile(rbacFS, "rbac/model.conf")
	if err != nil {
		return nil, fmt.Errorf("authorization: read model failed: %w", err)
	}
	policyText, err := fs.ReadFile(rbacFS, "rbac/policy.csv")
	if err != nil {
		return nil, fmt.Errorf("authorization: read policy failed: %w", err)
	}

	parsedModel, err := model.NewModelFromString(string(modelText))
	if err != nil {
		return nil, fmt.Errorf("authorization: parse model failed: %w", err)
	}

	enforcer, err := casbin.NewEnforcer(parsedModel, stringadapter.NewAdapter(string(policyText)))
	if err != nil {
		return nil, fmt.Errorf("authorization: new enforcer failed: %w", err)
	}
	// 策略全部来自内嵌文件，运行期没有任何变更来源。
	enforcer.EnableAutoSave(false)

	e := &Enforcer{
		enforcer: enforcer,
	}
	e.defaultContribMw = e.newContribMiddleware(nil)

	return e, nil
}

// Casbin 返回底层的 Casbin v3 Enforcer 实例。
func (e *Enforcer) Casbin() *casbin.Enforcer {
	if e == nil {
		return nil
	}
	return e.enforcer
}

// FiberMiddleware 构造或复用基于 Fiber v3 官方 contrib/casbin/v2 的权限拦截中间件。
// 当未传入自定义 lookup 函数时，直接返回预编译的单例实例，避免请求热路径上的对象分配与闭包逃逸。
func (e *Enforcer) FiberMiddleware(lookup ...func(fiber.Ctx) string) *contribcasbin.Middleware {
	if e == nil || e.enforcer == nil {
		return nil
	}

	if len(lookup) > 0 && lookup[0] != nil {
		return e.newContribMiddleware(lookup[0])
	}
	return e.defaultContribMw
}

func (e *Enforcer) newContribMiddleware(lookup func(fiber.Ctx) string) *contribcasbin.Middleware {
	if lookup == nil {
		lookup = func(c fiber.Ctx) string {
			if id, ok := authentication.FromContext(c.Context()); ok && id.UserID != "" {
				return id.Role
			}
			return ""
		}
	}

	return contribcasbin.New(contribcasbin.Config{
		Enforcer: e.enforcer,
		Lookup:   lookup,
		Unauthorized: func(c fiber.Ctx) error {
			return errs.Render(c, errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required"))
		},
		Forbidden: func(c fiber.Ctx) error {
			return errs.Render(c, errs.Forbidden("You are not allowed to perform this action"))
		},
	})
}

// RequirePermission 返回一个执行细粒度权限校验的 Fiber 中间件。
// 支持单角色与多角色（AllRoles）判定，未通过时直接返回 403 Forbidden 统一错误。
func (e *Enforcer) RequirePermission(resource, action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, ok := authentication.FromContext(c.Context())
		if !ok || id.UserID == "" {
			return errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required")
		}

		roles := id.AllRoles()
		if len(roles) == 0 || !e.CanRoles(roles, resource, action) {
			return errs.Forbidden("You are not allowed to perform this action")
		}

		return c.Next()
	}
}

// CanRole 判定指定单一角色是否被授予指定资源和动作的操作权限。
func (e *Enforcer) CanRole(role, resource, action string) (bool, error) {
	if role == "" || resource == "" || action == "" || e == nil || e.enforcer == nil {
		return false, nil
	}

	allowed, err := e.enforcer.Enforce(role, resource, action)
	if err != nil {
		return false, fmt.Errorf("authorization: enforce failed: %w", err)
	}
	return allowed, nil
}

// CanRoles 判定一组角色中是否有任一角色具备指定权限。
func (e *Enforcer) CanRoles(roles []string, resource, action string) bool {
	return slices.ContainsFunc(roles, func(role string) bool {
		allowed, _ := e.CanRole(role, resource, action)
		return allowed
	})
}

// Can 实现 authentication.Gate 契约：从上下文提取主体角色并判断权限。
func (e *Enforcer) Can(ctx context.Context, resource, action string) bool {
	id, ok := authentication.FromContext(ctx)
	if !ok || id.UserID == "" {
		return false
	}
	return e.CanRoles(id.AllRoles(), resource, action)
}

// Authorize 实现 authentication.Gate 契约：校验权限，无权限直接返回 403 Forbidden 统一错误。
func (e *Enforcer) Authorize(ctx context.Context, resource, action string) error {
	if !e.Can(ctx, resource, action) {
		return errs.Forbidden("You are not allowed to perform this action")
	}
	return nil
}

// Allows 实现 authentication.Gate 契约，是 Can 的语义别名。
func (e *Enforcer) Allows(ctx context.Context, resource, action string) bool {
	return e.Can(ctx, resource, action)
}

// Denies 实现 authentication.Gate 契约：判定当前请求主体是否被禁止该操作。
func (e *Enforcer) Denies(ctx context.Context, resource, action string) bool {
	return !e.Can(ctx, resource, action)
}
