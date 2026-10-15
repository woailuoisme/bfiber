package profile

import (
	"context"
	"database/sql"
	"errors"
	"mime/multipart"

	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
	"bfiber/internal/shared/media"
)

// Service 实现用户个人资料领域用例。
type Service struct {
	db    bun.IDB
	media *media.Manager
}

// NewService 构造个人资料服务。
func NewService(db bun.IDB, media *media.Manager) *Service {
	return &Service{
		db:    db,
		media: media,
	}
}

// GetProfile 获取当前登录用户的个人资料。
func (s *Service) GetProfile(ctx context.Context, userID string) (ProfileResource, error) {
	entity, err := models.FindUserByID(ctx, s.db, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProfileResource{}, errs.NotFound("The requested user does not exist")
		}
		return ProfileResource{}, errs.Internal(err)
	}
	return toProfileResource(entity), nil
}

// UpdateProfile 执行本人档案的局部更新并做用户名防重。
func (s *Service) UpdateProfile(ctx context.Context, userID string, body UpdateProfileBody) (ProfileResource, error) {
	fields := body.ToFields()
	if username, ok := fields["username"].(string); ok && username != "" {
		if err := models.CheckUsernameAvailable(ctx, s.db, userID, username); err != nil {
			return ProfileResource{}, err
		}
	}

	updated, err := models.UpdateUserFields(ctx, s.db, userID, fields)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProfileResource{}, errs.NotFound("The requested user does not exist")
		}
		return ProfileResource{}, models.MapUserUniqueConflict(err)
	}
	return toProfileResource(updated), nil
}

// UploadAvatar 上传新头像并原子更新用户档案中的头像链接（单事务自洽闭环）。
func (s *Service) UploadAvatar(ctx context.Context, userID string, file *multipart.FileHeader) (ProfileResource, error) {
	var updated *models.User
	err := database.RunInTx(ctx, s.db, func(txCtx context.Context) error {
		avatarURL, err := s.media.AttachAvatar(txCtx, userID, file)
		if err != nil {
			return err
		}

		u, err := models.SetUserAvatar(txCtx, s.db, userID, avatarURL)
		if err != nil {
			return err
		}
		updated = u
		return nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProfileResource{}, errs.NotFound("The requested user does not exist")
		}
		return ProfileResource{}, err
	}
	return toProfileResource(updated), nil
}
