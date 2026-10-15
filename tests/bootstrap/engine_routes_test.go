package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/bootstrap"
	"bfiber/internal/config"
)

// TestNamedRoutesContract 验证应用引擎中所有核心业务端点均已配置唯一路由名称。
func TestNamedRoutesContract(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	app, err := bootstrap.InspectEngine(cfg)
	require.NoError(t, err)
	require.NotNil(t, app)

	routes := app.GetRoutes()
	require.NotEmpty(t, routes)

	names := make(map[string]string)
	for _, r := range routes {
		if r.Name != "" {
			names[r.Name] = r.Method + " " + r.Path
		}
	}

	expectedNamedRoutes := []string{
		"system.index",
		"system.livez",
		"system.readyz",
		"auth.signup.email",
		"auth.signin.email",
		"auth.signin.username",
		"auth.token.refresh",
		"auth.session.get",
		"auth.signout",
		"users.me.get",
		"users.me.update",
		"users.me.avatar",
		"admin.auth.signin.email",
		"admin.users.list",
		"admin.users.create",
		"admin.users.get",
		"realtime.token",
		"payments.notify.channel",
	}

	for _, name := range expectedNamedRoutes {
		assert.Contains(t, names, name, "route name %s should be registered", name)
	}
}
