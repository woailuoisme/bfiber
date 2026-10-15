package bootstrap

import "strings"

// UserIDConstraint 校验路由参数是否符合系统 User ID 规范（usr_ 前缀且具备有效长度）。
type UserIDConstraint struct{}

// Name 返回约束标识符，用于路由模板绑定（如 /:id<user_id>）。
func (c UserIDConstraint) Name() string {
	return "user_id"
}

// Execute 判定参数是否满足 user_id 规范。
func (c UserIDConstraint) Execute(param string, _ ...string) bool {
	return strings.HasPrefix(param, "usr_") && len(param) > 4
}
