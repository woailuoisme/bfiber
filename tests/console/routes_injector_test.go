package console_test

import (
	"go/format"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/console/core"
)

func TestDeriveRouteInfo_Admin(t *testing.T) {
	opts := core.GeneratorOptions{
		Name:    "product_category",
		IsAdmin: true,
	}

	info := core.DeriveRouteInfo(opts)
	assert.Equal(t, "admin", info.Scope)
	assert.Equal(t, "adminproductcategory", info.PkgAlias)
	assert.Equal(t, "bfiber/internal/features/admin/product_category", info.ImportPath)
	assert.Contains(t, info.ImportStatement, "adminproductcategory \"bfiber/internal/features/admin/product_category\"")
	assert.Contains(t, info.RouteStatement, "adminproductcategory.RegisterAdminRoutes(v1, adminproductcategory.NewHandler(adminproductcategory.NewService(deps.DBConn())), mw)")
}

func TestDeriveRouteInfo_Client(t *testing.T) {
	opts := core.GeneratorOptions{
		Name:    "order",
		IsAdmin: false,
	}

	info := core.DeriveRouteInfo(opts)
	assert.Equal(t, "client", info.Scope)
	assert.Equal(t, "clientorder", info.PkgAlias)
	assert.Equal(t, "bfiber/internal/features/client/order", info.ImportPath)
	assert.Contains(t, info.ImportStatement, "clientorder \"bfiber/internal/features/client/order\"")
	assert.Contains(t, info.RouteStatement, "clientorder.RegisterRoutes(v1, clientorder.NewHandler(clientorder.NewService(deps.DBConn())), mw)")
}

func TestInjectRoute_And_RemoveRoute_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	bootstrapDir := filepath.Join(tempDir, "internal", "bootstrap")
	require.NoError(t, os.MkdirAll(bootstrapDir, 0o755))

	rawTemplate := `package bootstrap

import (
	"github.com/gofiber/fiber/v3"
	// [MAKE:CRUD:ADMIN_IMPORTS_START]
	// [MAKE:CRUD:ADMIN_IMPORTS_END]

	// [MAKE:CRUD:CLIENT_IMPORTS_START]
	// [MAKE:CRUD:CLIENT_IMPORTS_END]
)

func registerAllRoutes(app *fiber.App, deps *RouteDeps) {
	mw := deps.Middleware
	v1 := app.Group("/v1")

	// [MAKE:CRUD:ADMIN_ROUTES_START]
	// [MAKE:CRUD:ADMIN_ROUTES_END]

	// [MAKE:CRUD:CLIENT_ROUTES_START]
	// [MAKE:CRUD:CLIENT_ROUTES_END]
}
`
	routesFile := core.ResolveRoutesFilePath(tempDir)
	require.NoError(t, os.WriteFile(routesFile, []byte(rawTemplate), 0o600))

	opts := core.GeneratorOptions{
		Name:    "article",
		IsAdmin: true,
	}

	// 1. 首次注入测试
	injected, err := core.InjectRoute(tempDir, opts)
	require.NoError(t, err)
	assert.True(t, injected, "first injection should succeed")

	contentBytes, err := os.ReadFile(routesFile)
	require.NoError(t, err)
	content := string(contentBytes)

	assert.Contains(t, content, "adminarticle \"bfiber/internal/features/admin/article\"")
	assert.Contains(t, content, "adminarticle.RegisterAdminRoutes(v1, adminarticle.NewHandler(adminarticle.NewService(deps.DBConn())), mw)")

	// 确保注入后的代码是合法 Go 语法
	_, err = format.Source(contentBytes)
	require.NoError(t, err, "injected routes.go should have valid Go syntax")

	// 2. 幂等性测试（重复注入应跳过）
	injectedAgain, err := core.InjectRoute(tempDir, opts)
	require.NoError(t, err)
	assert.False(t, injectedAgain, "second injection should be idempotent and return false")

	// 3. 注销删除测试
	removed, err := core.RemoveRoute(tempDir, opts)
	require.NoError(t, err)
	assert.True(t, removed, "first removal should succeed")

	cleanedBytes, err := os.ReadFile(routesFile)
	require.NoError(t, err)
	cleanedContent := string(cleanedBytes)

	assert.NotContains(t, cleanedContent, "adminarticle \"bfiber/internal/features/admin/article\"")
	assert.NotContains(t, cleanedContent, "adminarticle.RegisterAdminRoutes")
	assert.Contains(t, cleanedContent, "// [MAKE:CRUD:ADMIN_ROUTES_START]")
	assert.Contains(t, cleanedContent, "// [MAKE:CRUD:ADMIN_ROUTES_END]")

	// 确保删除后的代码依然是合法 Go 语法
	_, err = format.Source(cleanedBytes)
	require.NoError(t, err, "cleaned routes.go should have valid Go syntax")

	// 4. 重复注销测试
	removedAgain, err := core.RemoveRoute(tempDir, opts)
	require.NoError(t, err)
	assert.False(t, removedAgain, "second removal should return false")
}

func TestActualRoutesCrudFile_HasAllMarkers(t *testing.T) {
	contentBytes, err := os.ReadFile(filepath.Join("..", "..", "internal", "bootstrap", "routes_crud.go"))
	require.NoError(t, err)
	content := string(contentBytes)

	markers := []string{
		"// [MAKE:CRUD:ADMIN_IMPORTS_START]",
		"// [MAKE:CRUD:ADMIN_IMPORTS_END]",
		"// [MAKE:CRUD:CLIENT_IMPORTS_START]",
		"// [MAKE:CRUD:CLIENT_IMPORTS_END]",
		"// [MAKE:CRUD:ADMIN_ROUTES_START]",
		"// [MAKE:CRUD:ADMIN_ROUTES_END]",
		"// [MAKE:CRUD:CLIENT_ROUTES_START]",
		"// [MAKE:CRUD:CLIENT_ROUTES_END]",
	}

	for _, m := range markers {
		assert.Contains(t, content, m, "routes_crud.go must contain marker %s", m)
	}
}
