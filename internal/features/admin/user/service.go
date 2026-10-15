package admin

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"

	"github.com/samber/lo"
	"github.com/uptrace/bun"

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
	DB     *bun.DB
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

// ListUsers 返回分页用户列表，排序与过滤都走白名单。
func (s *Service) ListUsers(ctx context.Context, query ListUsersQueryParams) ([]UserResource, response.Meta, error) {
	page, pageSize := NormalisePagination(query.Page, query.PageSize)
	users, total, err := models.ListUsersAndCount(ctx, s.db, query.ToFilter(), page, pageSize)
	if err != nil {
		return nil, response.Meta{}, errs.Internal(err)
	}

	items := lo.Map(users, func(entity models.User, _ int) UserResource {
		return toUserResource(&entity)
	})
	return items, response.NewMeta(page, pageSize, total), nil
}

// ExportUsers 流式导出匹配条件的所有用户数据至目标 io.Writer，采用物理分块拉取杜绝 OOM。
func (s *Service) ExportUsers(ctx context.Context, query ListUsersQueryParams, w io.Writer, opts ...excel.ExportOption) error {
	fetcher := UserFetchFunc(func(ctx context.Context, filter models.UserListFilter, offset, limit int) ([]models.User, error) {
		return models.ListUsers(ctx, s.db, filter, offset, limit)
	})
	return ExportChunkedStream(ctx, w, fetcher, query.ToFilter(), opts...)
}

// GetUser 返回管理视角的单个用户。
func (s *Service) GetUser(ctx context.Context, id string) (UserResource, error) {
	entity, err := models.FindUserByID(ctx, s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserResource{}, errs.NotFound("The requested user does not exist")
		}
		return UserResource{}, errs.Internal(err)
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
	fields := body.ToFields()
	if username, ok := fields["username"].(string); ok && username != "" {
		if err := models.CheckUsernameAvailable(ctx, s.db, id, username); err != nil {
			return UserResource{}, err
		}
	}

	updated, err := models.UpdateUserFields(ctx, s.db, id, fields)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserResource{}, errs.NotFound("The requested user does not exist")
		}
		return UserResource{}, models.MapUserUniqueConflict(err)
	}
	return toUserResource(updated), nil
}

// DeleteUser 软删除指定用户。
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	entity, err := models.FindUserByID(ctx, s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errs.NotFound("The requested user does not exist")
		}
		return errs.Internal(err)
	}
	if err := entity.Delete(ctx, s.db); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errs.NotFound("The requested user does not exist")
		}
		return errs.Internal(err)
	}
	return nil
}

// NormalisePagination 将分页参数收敛到允许的安全区间。
func NormalisePagination(page, pageSize int) (int, int) {
	page = min(max(page, defaultPage), maxPage)
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	return page, min(pageSize, maxPageSize)
}
