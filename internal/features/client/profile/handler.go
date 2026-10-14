package profile

import (
	"github.com/aarondl/opt/omitnull"
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/authentication"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/response"
	"bfiber/internal/platform/validator"
	"bfiber/internal/shared/media"
	"bfiber/internal/shared/user"
)

// Handler 是本人档案端点的 HTTP 适配层。
type Handler struct {
	userService  *user.Service
	mediaManager *media.Manager
}

// NewHandler 构造 Handler。
func NewHandler(userService *user.Service, mediaManager *media.Manager) *Handler {
	return &Handler{
		userService:  userService,
		mediaManager: mediaManager,
	}
}

func (h *Handler) currentUserID(c fiber.Ctx) (string, error) {
	id, ok := authentication.FromContext(c.Context())
	if !ok {
		return "", errs.Unauthorized(errs.CodeAuthTokenInvalid, "Authentication is required")
	}
	return id.UserID, nil
}

// GetProfile 处理 GET /v1/users/me。
//
// @Summary      读取本人档案
// @Description  获取当前登录用户的完整个人档案。
// @Tags         个人资料
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} ProfileResource "本人档案"
// @x-order      1
// @Router       /users/me [get]
func (h *Handler) GetProfile(c fiber.Ctx) error {
	userID, err := h.currentUserID(c)
	if err != nil {
		return err
	}

	profile, err := h.userService.GetProfile(c.Context(), userID)
	if err != nil {
		return err
	}
	return response.OK(c, profile)
}

// UpdateProfile 处理 PATCH /v1/users/me。
//
// @Summary      更新本人档案
// @Description  局部更新个人资料，支持更新昵称、头像、用户名、展示名与手机号。
// @Tags         个人资料
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        payload body UpdateProfileBody true "待更新字段，可只提交其中一部分"
// @Success      200 {object} ProfileResource "更新后的档案"
// @x-order      2
// @Router       /users/me [patch]
func (h *Handler) UpdateProfile(c fiber.Ctx) error {
	userID, err := h.currentUserID(c)
	if err != nil {
		return err
	}

	var body UpdateProfileBody
	if err := c.Bind().Body(&body); err != nil {
		return err
	}

	profile, err := h.userService.UpdateProfile(c.Context(), userID, body.fieldSet(), user.ScopeClient)
	if err != nil {
		return err
	}
	return response.OK(c, profile)
}

// UploadAvatar 处理 POST /v1/users/me/avatar。
//
// @Summary      上传本人头像
// @Description  上传并更新当前登录用户的个人头像。仅允许上传不超过 2MB 的 JPEG、PNG 或 WebP 格式图片，自动按魔数头校验真实图像并清除历史旧头像。
// @Tags         个人资料
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        avatar formData file true "头像图片文件 (最大 2MB，支持 JPEG/PNG/WebP)"
// @Success      200 {object} ProfileResource "更新头像后的本人档案"
// @Failure      401 {object} errs.Response "未认证"
// @Failure      422 {object} errs.Response "文件校验失败"
// @x-order      3
// @Router       /users/me/avatar [post]
func (h *Handler) UploadAvatar(c fiber.Ctx) error {
	userID, err := h.currentUserID(c)
	if err != nil {
		return err
	}

	file, _ := c.FormFile("avatar")
	body := UploadAvatarBody{Avatar: file}
	if err := validator.Engine().Struct(body); err != nil {
		return err
	}

	avatarURL, err := h.mediaManager.AttachAvatar(c.Context(), userID, body.Avatar)
	if err != nil {
		return err
	}

	profile, err := h.userService.UpdateProfile(
		c.Context(),
		userID,
		user.FieldSet{
			Image: omitnull.From(avatarURL),
		},
		user.ScopeClient,
	)
	if err != nil {
		return err
	}

	return response.OK(c, profile)
}
