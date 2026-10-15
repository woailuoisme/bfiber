package models

// SearchableAs 返回 User 实体对应的搜索索引基础名称。
func (u *User) SearchableAs() string {
	return "users"
}

// SearchableKey 返回 User 实体的全局唯一搜索键。
func (u *User) SearchableKey() string {
	return u.ID
}

// ToSearchableArray 将用户模型映射为全文搜索文档字典。
func (u *User) ToSearchableArray() map[string]any {
	doc := map[string]any{
		"id":             u.ID,
		"name":           u.Name,
		"email":          u.Email,
		"role":           string(u.Role),
		"status":         string(u.Status),
		"email_verified": u.EmailVerified,
		"created_at":     u.CreatedAt.Unix(),
		"updated_at":     u.UpdatedAt.Unix(),
	}
	if u.Username != nil {
		doc["username"] = *u.Username
	}
	if u.DisplayUsername != nil {
		doc["display_username"] = *u.DisplayUsername
	}
	if u.PhoneNumber != nil {
		doc["phone_number"] = *u.PhoneNumber
	}
	return doc
}

// GetModelType 返回多态媒体附件的模型类型标识。
func (u *User) GetModelType() string {
	return "user"
}

// GetModelID 返回多态媒体附件的宿主主键 ID。
func (u *User) GetModelID() string {
	return u.ID
}

// RouteNotificationFor 根据通知渠道返回接收目标标识（满足 contract.Notifiable 契约）。
func (u *User) RouteNotificationFor(channel string) string {
	if u == nil {
		return ""
	}
	switch channel {
	case "mail":
		return u.Email
	case "sms":
		if u.PhoneNumber != nil {
			return *u.PhoneNumber
		}
	}
	return ""
}

// RouteNotificationForMail 返回接收邮件通知的目标邮箱。
func (u *User) RouteNotificationForMail() string {
	if u == nil {
		return ""
	}
	return u.Email
}
