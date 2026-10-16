// Package media 提供媒体文件上传、安全校验、命名与分区路径策略、Imgproxy 防篡改签名与多态实体附件管理。
package media

import (
	"cmp"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/samber/oops"

	"bfiber/internal/platform/errs"
)

// 头像策略常量。
const (
	// maxAvatarSize 头像文件字节上限（2MB）。
	maxAvatarSize = 2 * 1024 * 1024
	// avatarEdge 头像裁切的正方形边长（像素）。
	avatarEdge = 256
)

// validateAvatarFile 校验头像文件的大小、扩展名与真实魔数。
func validateAvatarFile(src Source) error {
	if src == nil || src.Size() <= 0 {
		return errs.BadRequest("Avatar file cannot be empty")
	}
	if src.Size() > maxAvatarSize {
		return errs.BadRequest("Avatar file size must not exceed 2MB")
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(src.FileName()), "."))
	switch ext {
	case "jpg", "jpeg", "png", "webp":
	default:
		return errs.BadRequest("Invalid image extension; only jpg, jpeg, png, and webp are allowed")
	}

	file, err := src.Open()
	if err != nil {
		return errs.BadRequest("Failed to open uploaded file")
	}
	defer func() { _ = file.Close() }()

	header := make([]byte, 512)
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return errs.BadRequest("Failed to read uploaded file")
	}

	mtype := mimetype.Detect(header[:n])
	switch mtype.String() {
	case "image/jpeg", "image/png", "image/webp":
		return nil
	default:
		return errs.BadRequest("Invalid image format; only JPEG, PNG, and WebP are allowed")
	}
}

// AttachAvatar 校验头像文件规格（魔数嗅探、格式白名单、尺寸限制），
// 将其原子上传并以单文件模式绑定至用户模型，返回解析后的签名访问 URL。
func (m *Manager) AttachAvatar(ctx context.Context, userID string, src Source) (string, error) {
	if err := validateAvatarFile(src); err != nil {
		return "", err
	}
	if m == nil {
		return "", oops.Code(errs.CodeInternal).Errorf("media: manager is not configured")
	}

	mediaRecord, err := m.Attach(
		ctx,
		Model("user", userID),
		CollectionAvatar,
		src,
		WithSingleFile(),
	)
	if err != nil {
		return "", err
	}

	return cmp.Or(m.Avatar(ctx, mediaRecord, avatarEdge), mediaRecord.FileName), nil
}
