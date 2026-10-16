package profile

import (
	"mime/multipart"
	"strings"
	"time"

	"github.com/aarondl/opt/omitnull"

	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
	"bfiber/internal/shared/enums"
)

// ProfileResource 是客户端个人资料响应视图。
type ProfileResource struct {
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
	Role enums.Role `json:"role" example:"user" enums:"user,admin"`
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

// toProfileResource 把用户数据实体投影为个人资料响应资源。
func toProfileResource(entity *models.User) ProfileResource {
	return ProfileResource{
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

// UploadAvatarBody 接收 multipart/form-data 格式的用户头像上传表单载荷。
type UploadAvatarBody struct {
	// 头像图片文件（最大 2MB，支持 JPEG/PNG/WebP）
	Avatar *multipart.FileHeader `form:"avatar" json:"avatar" validate:"required,file,image,mimes=jpg jpeg png webp,maxsize=2M"`
}

// UpdateProfileBody 是 PATCH /v1/users/me 的局部更新请求体。
type UpdateProfileBody struct {
	// 用户展示昵称
	Name omitnull.Val[string] `json:"name" validate:"omitempty,min=1,max=120" swaggertype:"string" extensions:"x-nullable" example:"Alex Mercer"`
	// 用户头像 URL
	Image omitnull.Val[string] `json:"image" validate:"omitempty,max=2048" swaggertype:"string" extensions:"x-nullable" example:"https://example.com/avatar.png"`
	// 唯一登录用户名 (3-30位)
	Username omitnull.Val[string] `json:"username" validate:"omitempty,min=3,max=30,usernameslug,notreserved" swaggertype:"string" extensions:"x-nullable" example:"alexmercer"`
	// 用户显示名称
	DisplayUsername omitnull.Val[string] `json:"displayUsername" validate:"omitempty,min=1,max=120" swaggertype:"string" extensions:"x-nullable" example:"Alex Mercer"`
	// E.164 格式手机号
	PhoneNumber omitnull.Val[string] `json:"phoneNumber" validate:"omitempty,e164" swaggertype:"string" extensions:"x-nullable" example:"+8613800138000"`
}

// Validate 实现 Validatable 契约，阻断库层非空字段的显式置空。
func (b UpdateProfileBody) Validate() error {
	if b.Name.IsNull() {
		return errs.Validation(map[string][]string{
			"name": {"cannot be null"},
		})
	}
	return nil
}

// ToFields 把客户端个人资料更新请求转换为数据库可写字段映射。
func (b UpdateProfileBody) ToFields() map[string]any {
	fields := make(map[string]any, 5)
	if !b.Name.IsUnset() && !b.Name.IsNull() {
		fields["name"] = strings.TrimSpace(b.Name.MustGet())
	}
	if !b.Image.IsUnset() {
		setNullable(fields, "image", b.Image)
	}
	if !b.PhoneNumber.IsUnset() {
		setNullable(fields, "phone_number", b.PhoneNumber)
	}
	setUsernameFields(fields, b.Username, b.DisplayUsername.IsUnset())
	if !b.DisplayUsername.IsUnset() {
		setNullable(fields, "display_username", b.DisplayUsername)
	}
	return fields
}

func setUsernameFields(fields map[string]any, username omitnull.Val[string], displayUnset bool) {
	if username.IsUnset() {
		return
	}
	if username.IsNull() {
		fields["username"] = nil
		fields["display_username"] = nil
		return
	}
	raw := strings.TrimSpace(username.MustGet())
	if raw == "" {
		fields["username"] = nil
		fields["display_username"] = nil
		return
	}
	fields["username"] = models.NormaliseUsername(raw)
	if displayUnset {
		fields["display_username"] = raw
	}
}

func setNullable(fields map[string]any, column string, value omitnull.Val[string]) {
	if value.IsNull() {
		fields[column] = nil
		return
	}
	trimmed := strings.TrimSpace(value.MustGet())
	if trimmed == "" {
		fields[column] = nil
	} else {
		fields[column] = trimmed
	}
}
