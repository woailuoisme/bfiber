package bootstrap

import "github.com/google/uuid"

// UserIDConstraint 校验路由参数是否符合系统 User ID 规范（UUIDv7）。
//
// 主键由 database.Base.BeforeAppendModel 生成 UUIDv7，因此约束按 UUID 语法判定，
// 而非固定前缀——前缀判定会让 /v1/admin/users/{id} 这一整组端点永远匹配不上路由。
type UserIDConstraint struct{}

// Name 返回约束标识符，用于路由模板绑定（如 /:id<user_id>）。
func (c UserIDConstraint) Name() string {
	return "user_id"
}

// Execute 判定参数是否满足 user_id 规范。
// 约束的职责是避免 /admin/users/export 这类字面量路径被动态段吞掉，语法合法即可。
func (c UserIDConstraint) Execute(param string, _ ...string) bool {
	_, err := uuid.Parse(param)
	return err == nil
}
