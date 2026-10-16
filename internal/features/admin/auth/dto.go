package adminauth

import (
	"time"

	"bfiber/internal/database/models"
	"bfiber/internal/shared/enums"
)

// AdminUser 是管理员身份资料响应视图。
type AdminUser struct {
	// 用户唯一标识
	ID string `json:"id" example:"019572d4-a6c8-78b1-8cf0-281b94c032a1"`
	// 用户展示昵称
	Name string `json:"name" example:"Alex Mercer"`
	// 用户电子邮箱
	Email string `json:"email" example:"alex@example.com"`
	// 邮箱是否已验证
	EmailVerified bool `json:"emailVerified" example:"true"`
	// 用户头像 URL
	Image *string `json:"image" example:"https://example.com/avatar.png"`
	// 用户权限角色 (user: 普通用户, admin: 系统管理员)
	Role enums.Role `json:"role" example:"admin" enums:"user,admin"`
	// 账号状态 (active: 正常活跃, inactive: 未激活, banned: 已封禁)
	Status enums.Status `json:"status" example:"active" enums:"active,inactive,banned"`
	// 唯一登录用户名
	Username *string `json:"username" example:"alexmercer"`
	// 用户显示名称
	DisplayUsername *string `json:"displayUsername" example:"Alex Mercer"`
	// E.164 格式手机号
	PhoneNumber *string `json:"phoneNumber" example:"+8613800138000"`
	// 账号创建时间 (ISO 8601 UTC)
	CreatedAt time.Time `json:"createdAt" example:"2026-09-27T10:00:00Z"`
	// 账号更新时间 (ISO 8601 UTC)
	UpdatedAt time.Time `json:"updatedAt" example:"2026-09-27T10:00:00Z"`
}

// toAdminUser 把用户模型投影为管理员档案视图。
func toAdminUser(entity *models.User) AdminUser {
	return AdminUser{
		ID:              entity.ID,
		Name:            entity.Name,
		Email:           entity.Email,
		EmailVerified:   entity.EmailVerified,
		Image:           entity.Image,
		Role:            entity.Role,
		Status:          entity.Status,
		Username:        entity.Username,
		DisplayUsername: entity.DisplayUsername,
		PhoneNumber:     entity.PhoneNumber,
		CreatedAt:       entity.CreatedAt.UTC(),
		UpdatedAt:       entity.UpdatedAt.UTC(),
	}
}

// Profile 是管理员档案视图的类型别名，保持内部兼容。
type Profile = AdminUser

// SignInEmailBody 是 POST /v1/admin/auth/sign-in/email 的请求体。
type SignInEmailBody struct {
	// 管理员电子邮箱
	Email string `json:"email" example:"admin@example.com" validate:"required,email"`
	// 管理员登录口令
	Password string `json:"password" example:"Admin123!" validate:"required"`
}

// RefreshTokenBody 是 POST /v1/admin/auth/token/refresh 的请求体。
type RefreshTokenBody struct {
	// 刷新令牌凭据
	RefreshToken string `json:"refreshToken" example:"rt_01J9X4N8Y0EFGH" validate:"required"`
}

// SignOutBody 是 POST /v1/admin/auth/sign-out 的请求体。
type SignOutBody struct {
	// 待作废的刷新令牌 (可选，同时作废可真正阻断续签)
	RefreshToken *string `json:"refreshToken" example:"rt_01J9X4N8Y0EFGH" validate:"omitempty"`
}

// ChangePasswordBody 是 POST /v1/admin/auth/change-password 的请求体。
type ChangePasswordBody struct {
	// 当前登录口令
	CurrentPassword string `json:"currentPassword" example:"Admin123!" validate:"required"`
	// 新登录口令 (8-128位，须含字母与数字)
	NewPassword string `json:"newPassword" example:"NewAdmin456!" validate:"required,min=8,max=128"`
}

// SessionResource 是签发令牌类端点的统一响应体。
type SessionResource struct {
	// 访问令牌 (JWT Access Token)
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	// 刷新令牌 (Refresh Token)
	RefreshToken string `json:"refreshToken" example:"rt_01J9X4N8Y0EFGH"`
	// 访问令牌有效期（秒）
	ExpiresIn int `json:"expiresIn" example:"900"`
	// 管理员身份资料投影
	User Profile `json:"user"`
}

// GetSessionResource 是 GET /v1/admin/auth/get-session 的响应体。
type GetSessionResource struct {
	// 当前登录管理员身份资料投影
	User Profile `json:"user"`
}
