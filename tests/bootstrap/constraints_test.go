package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"bfiber/internal/bootstrap"
)

// TestUserIDConstraint 验证 UserIDConstraint 路由约束对合法与非法参数的判定逻辑。
// 主键为 UUIDv7，因此约束必须接受 UUID 并拒绝字面量路径段。
func TestUserIDConstraint(t *testing.T) {
	t.Parallel()

	constraint := bootstrap.UserIDConstraint{}
	assert.Equal(t, "user_id", constraint.Name())

	tests := []struct {
		name  string
		param string
		want  bool
	}{
		{"合法 UUIDv7", "01a141ec-85e6-78b2-a392-2e45977a56a4", true},
		{"合法 UUID 大写", "01A141EC-85E6-78B2-A392-2E45977A56A4", true},
		{"无连字符的 32 位十六进制", "01a141ec85e678b2a3922e45977a56a4", true},
		{"字面量路径 export 必须不匹配", "export", false},
		{"空字符串", "", false},
		{"非十六进制字符", "01a141ec-85e6-78b2-a392-2e45977a56zz", false},
		{"过短", "01a141ec-85e6-78b2", false},
		{"带前缀但非 UUID", "usr_01J9X4N8Y0ABCD", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, constraint.Execute(tt.param))
		})
	}
}
