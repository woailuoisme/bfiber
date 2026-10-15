package admin

import (
	"github.com/gofiber/fiber/v3"

	"bfiber/internal/platform/response"
)

// Handler 是管理端用户 CRUD 的 HTTP 适配层。
type Handler struct {
	service *Service
}

// NewHandler 构造 Handler。
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// ListUsers 处理 GET /v1/admin/users。
//
// @Summary      用户分页列表
// @Description  根据分页、关键字、状态与角色条件查询系统用户列表。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        query query ListUsersQueryParams false "查询与分页参数"
// @Success      200 {object} UserPageResource "用户分页列表"
// @x-order      1
// @Router       /admin/users [get]
func (h *Handler) ListUsers(c fiber.Ctx) error {
	var query ListUsersQueryParams
	if err := c.Bind().Query(&query); err != nil {
		return err
	}

	items, meta, err := h.service.ListUsers(c.Context(), query)
	if err != nil {
		return err
	}
	return response.Paginated(c, items, meta)
}

// ExportUsers 处理 GET /v1/admin/users/export。
//
// @Summary      导出系统用户
// @Description  根据关键字、状态与角色条件分块流式导出用户列表为 Excel 文件，免除内存溢出风险。
// @Tags         用户管理
// @Accept       json
// @Produce      application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Security     BearerAuth
// @Param        query query ListUsersQueryParams false "筛选与排序条件"
// @Success      200 {file} binary "用户数据 Excel 文件"
// @x-order      6
// @Router       /admin/users/export [get]
func (h *Handler) ExportUsers(c fiber.Ctx) error {
	var query ListUsersQueryParams
	if err := c.Bind().Query(&query); err != nil {
		return err
	}

	c.Attachment("users.xlsx")
	c.Type("xlsx")

	if err := h.service.ExportUsers(c.Context(), query, c); err != nil {
		return err
	}
	return nil
}

// GetUser 处理 GET /v1/admin/users/:id。
//
// @Summary      获取用户详情
// @Description  根据用户唯一标识查询用户完整档案与管理审计信息。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "用户唯一标识" example("usr_01J9X4N8Y0ABCD")
// @Success      200 {object} UserResource "用户完整档案"
// @x-order      2
// @Router       /admin/users/{id} [get]
func (h *Handler) GetUser(c fiber.Ctx) error {
	var params UserPathParams
	if err := c.Bind().URI(&params); err != nil {
		return err
	}

	entity, err := h.service.GetUser(c.Context(), params.ID)
	if err != nil {
		return err
	}
	return response.OK(c, entity)
}

// CreateUser 处理 POST /v1/admin/users。
//
// @Summary      创建系统用户
// @Description  管理员直接创建新用户账号，指定邮箱、口令、昵称与角色等信息。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        payload body CreateUserBody true "创建用户载荷"
// @Success      201 {object} UserResource "新建成功的用户对象"
// @x-order      3
// @Router       /admin/users [post]
func (h *Handler) CreateUser(c fiber.Ctx) error {
	var body CreateUserBody
	if err := c.Bind().Body(&body); err != nil {
		return err
	}

	created, err := h.service.CreateUser(c.Context(), body)
	if err != nil {
		return err
	}
	return response.Created(c, created)
}

// UpdateUser 处理 PATCH /v1/admin/users/:id。
//
// @Summary      更新用户信息
// @Description  支持局部字段三态更新，可修改昵称、头像、用户名、手机号、角色、状态及封禁属性。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "用户唯一标识" example("usr_01J9X4N8Y0ABCD")
// @Param        payload body UpdateUserBody true "局部更新载荷"
// @Success      200 {object} UserResource "更新后的用户对象"
// @x-order      4
// @Router       /admin/users/{id} [patch]
func (h *Handler) UpdateUser(c fiber.Ctx) error {
	var params UserPathParams
	if err := c.Bind().URI(&params); err != nil {
		return err
	}

	var body UpdateUserBody
	if err := c.Bind().Body(&body); err != nil {
		return err
	}

	updated, err := h.service.UpdateUser(c.Context(), params.ID, body)
	if err != nil {
		return err
	}
	return response.OK(c, updated)
}

// DeleteUser 处理 DELETE /v1/admin/users/:id，返回 204。
//
// @Summary      注销删除用户
// @Description  物理删除指定用户及其关联账号数据，并级联清理权限策略。
// @Tags         用户管理
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "用户唯一标识" example("usr_01J9X4N8Y0ABCD")
// @Success      204 "删除成功，无响应体"
// @x-order      5
// @Router       /admin/users/{id} [delete]
func (h *Handler) DeleteUser(c fiber.Ctx) error {
	var params UserPathParams
	if err := c.Bind().URI(&params); err != nil {
		return err
	}

	if err := h.service.DeleteUser(c.Context(), params.ID); err != nil {
		return err
	}
	return response.NoContent(c)
}
