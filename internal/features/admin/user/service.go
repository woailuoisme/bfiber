package admin

import (
	"context"
	"io"
	"strings"

	"github.com/samber/lo"
	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/excel"
	"bfiber/internal/platform/response"
	"bfiber/internal/platform/validator"
	"bfiber/internal/shared/enums"
)

// passwordHasher 声明用户管理服务所需的口令安全哈希能力（遵循 Uber Go Guide：接口由消费方定义）。
type passwordHasher interface {
	Make(password string) (string, error)
}

// Deps 声明用户管理服务的外部依赖。
type Deps struct {
	DB     bun.IDB
	Hasher passwordHasher
}

// Service 实现后台用户管理用例。
type Service struct {
	db     bun.IDB
	hasher passwordHasher
}

// NewService 构造管理服务。
func NewService(deps Deps) *Service {
	return &Service{
		db:     deps.DB,
		hasher: deps.Hasher,
	}
}

// ListUsers 返回分页用户列表，过滤、排序与分页全部由 UserQuery 规格承载。
func (s *Service) ListUsers(ctx context.Context, query ListUsersQueryParams) ([]UserResource, response.Meta, error) {
	users, meta, err := query.ToQuery().Paginate(ctx, s.db)
	if err != nil {
		return nil, response.Meta{}, errs.Internal(err)
	}

	items := lo.Map(users, func(entity models.User, _ int) UserResource {
		return toUserResource(&entity)
	})
	return items, meta, nil
}

// ExportUsers 流式导出匹配条件的所有用户数据至目标 io.Writer。
// 与 ListUsers 共用同一 UserQuery 规格，采用物理分块拉取杜绝 OOM。
func (s *Service) ExportUsers(ctx context.Context, query ListUsersQueryParams, w io.Writer, opts ...excel.ExportOption) error {
	spec := query.ToQuery()
	fetch := func(ctx context.Context, window database.Window) ([]models.User, error) {
		return spec.Chunk(ctx, s.db, window)
	}
	return ExportChunkedStream(ctx, w, fetch, opts...)
}

// GetUser 返回管理视角的单个用户。
func (s *Service) GetUser(ctx context.Context, id string) (UserResource, error) {
	entity, err := models.FindUserByID(ctx, s.db, id)
	if err != nil {
		return UserResource{}, errs.From(err, errs.UserNotFound())
	}
	return toUserResource(entity), nil
}

// CreateUser 由管理员直接创建账号，初始口令由调用方指定。
func (s *Service) CreateUser(ctx context.Context, body CreateUserBody) (UserResource, error) {
	if err := validator.PasswordStrength("password", body.Password); err != nil {
		return UserResource{}, err
	}

	entity := &models.User{
		Name:          strings.TrimSpace(body.Name),
		Email:         models.NormaliseEmail(body.Email),
		EmailVerified: body.EmailVerified,
		Image:         body.Image,
		Role:          body.Role,
		Status:        enums.StatusActive,
	}
	if body.Username != nil {
		normalised := models.NormaliseUsername(*body.Username)
		entity.Username = &normalised
		entity.DisplayUsername = &normalised
	}

	hash, err := s.hasher.Make(body.Password)
	if err != nil {
		return UserResource{}, errs.Internal(err)
	}

	if err := models.CreateUserWithCredential(ctx, s.db, entity, hash); err != nil {
		return UserResource{}, models.MapUserUniqueConflict(err)
	}
	return toUserResource(entity), nil
}

// UpdateUser 由管理员局部更新用户，含角色、状态与封禁信息。
func (s *Service) UpdateUser(ctx context.Context, id string, body UpdateUserBody) (UserResource, error) {
	updated, err := models.ApplyUserPatch(ctx, s.db, id, body.ToFields())
	if err != nil {
		return UserResource{}, err
	}
	return toUserResource(updated), nil
}

// DeleteUser 软删除指定用户。
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	if err := models.DeleteUser(ctx, s.db, id); err != nil {
		return errs.From(err, errs.UserNotFound())
	}
	return nil
}
