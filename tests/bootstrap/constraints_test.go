package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"bfiber/internal/bootstrap"
)

// TestUserIDConstraint 验证 UserIDConstraint 路由约束对合法与非法参数的判定逻辑。
func TestUserIDConstraint(t *testing.T) {
	t.Parallel()

	constraint := bootstrap.UserIDConstraint{}
	assert.Equal(t, "user_id", constraint.Name())

	tests := []struct {
		name  string
		param string
		want  bool
	}{
		{"合法 User ID", "usr_01J9X4N8Y0ABCD", true},
		{"合法短 User ID", "usr_123", true},
		{"缺少前缀纯数字", "123456", false},
		{"空字符串", "", false},
		{"仅前缀无标识", "usr_", false},
		{"错误前缀", "acc_01J9X4N8Y0ABCD", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, constraint.Execute(tt.param))
		})
	}
}
