package media

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"bfiber/internal/database/models"
	"bfiber/internal/platform/logger"
)

// URL 智能生成媒体文件的访问 URL。
// 传入图像处理选项且图像服务可用时生成防篡改签名派生 URL；否则返回存储驱动自报的公开地址。
// 驱动未配置公开地址时两者不同：local 返回空串；s3 返回按 endpoint 推导的直连地址，
// 而该地址只在桶允许匿名读取时可用，私有桶下请求侧会收到 403（此时应改走派生或预签名 URL）。
// 记录为空或派生失败时返回空串，调用方以空串视为「无可访问链接」。
func (m *Manager) URL(ctx context.Context, mediaItem *models.Media, opts ...ProcessOption) string {
	if len(opts) == 0 {
		return m.originalURL(mediaItem)
	}

	return m.derivedURL(ctx, mediaItem, opts...)
}

// thumbEdge 缩略图外接盒边长（像素）：等比缩放，长边不超过该值。
const thumbEdge = 300

// Thumbnail 获取缩略图 URL：等比缩放到 thumbEdge 外接盒内，保持长宽比且不放大原图。
func (m *Manager) Thumbnail(ctx context.Context, mediaItem *models.Media) string {
	return m.derivedURL(ctx, mediaItem, thumbnailOptions(thumbEdge, thumbEdge)...)
}

// Optimized 获取按图像服务默认格式与质量重编码、尺寸保持不变的原图 URL。
// 默认格式与质量取自 ImgproxyConfig（缺省 webp / 85）。
func (m *Manager) Optimized(ctx context.Context, mediaItem *models.Media) string {
	return m.derivedURL(ctx, mediaItem)
}

// Avatar 快捷获取多态媒体文件的头像裁切 URL。
func (m *Manager) Avatar(ctx context.Context, mediaItem *models.Media, size int) string {
	return m.derivedURL(ctx, mediaItem, avatarOptions(size)...)
}

// derivedURL 经图像服务派生处理后的 URL；非位图、服务不可用或派生失败时回落到原始直链。
func (m *Manager) derivedURL(ctx context.Context, mediaItem *models.Media, opts ...ProcessOption) string {
	if mediaItem == nil || mediaItem.FileName == "" {
		return ""
	}

	if m.isProcessableImage(mediaItem) {
		imgURL, err := m.image.URL(mediaItem.FileName, opts...)
		if err == nil && imgURL != "" {
			return imgURL
		}

		// 派生失败会静默退化为原图直链，必须留下痕迹，否则线上只能看到「图片没优化」而查不到原因。
		logger.FromContext(ctx).Warn("failed to derive media url, falling back to original",
			zap.Error(err),
			zap.String("path", mediaItem.FileName),
		)
	}

	return m.originalURL(mediaItem)
}

// originalURL 返回底层存储的原始文件直链。
func (m *Manager) originalURL(mediaItem *models.Media) string {
	if mediaItem == nil || mediaItem.FileName == "" {
		return ""
	}

	// disk.URL 失败即按无链接处理：local 驱动在未配置公开地址时返回 ErrNoPublicURL，
	// s3 驱动则会推导 endpoint 直连地址（该地址要求桶匿名可读，否则请求侧收到 403）。
	url, _ := m.disk.URL(mediaItem.FileName)
	return url
}

// isProcessableImage 判断记录是否可交给图像服务处理（排除 SVG 等非位图）。
func (m *Manager) isProcessableImage(mediaItem *models.Media) bool {
	return mediaItem != nil &&
		m.image != nil &&
		strings.HasPrefix(mediaItem.MimeType, "image/") &&
		mediaItem.MimeType != "image/svg+xml"
}
