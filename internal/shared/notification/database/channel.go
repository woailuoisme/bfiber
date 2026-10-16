// Package database 提供基于 PostgreSQL 关系型数据库持久化的通知渠道实现。
package database

import (
	"cmp"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/samber/oops"
	"github.com/uptrace/bun"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	"bfiber/internal/platform/errs"
	"bfiber/internal/platform/notification/contract"
)

// Channel 数据库通知渠道实现。
type Channel struct {
	db bun.IDB
}

// NewChannel 创建数据库通知渠道实例。
// 形参类型即传播契约：调用方传入根句柄还是活跃事务由其决定，本渠道每条语句再经
// database.ResolveDB(ctx, db) 解析，因此调用方在事务内的写入不会被静默排除在外。
func NewChannel(db bun.IDB) *Channel {
	return &Channel{db: db}
}

// Name 返回渠道标识名。
func (c *Channel) Name() string {
	return "database"
}

// Format 将领域通知对象转换为数据库持久化载荷 *Message。
func (c *Channel) Format(n contract.Notification, notifiable any) (any, error) {
	notifType := reflect.TypeOf(n).String()

	if dbNotif, ok := n.(Notification); ok {
		data := dbNotif.ToDatabase(notifiable)
		return NewMessage(notifType, data), nil
	}

	if cf, ok := n.(contract.ChannelFormatter); ok {
		raw := cf.Format(c.Name(), notifiable)
		if dataMap, ok := raw.(map[string]any); ok {
			return NewMessage(notifType, dataMap), nil
		}
	}

	return nil, oops.Code(errs.CodeNotificationUnsupportedDB).
		With("notification", n).
		Errorf("notification does not implement database.Notification or ChannelFormatter")
}

// extractMessage 从任意输入中解析 *Message。
func (c *Channel) extractMessage(notifiable, message any) (*Message, error) {
	switch v := message.(type) {
	case *Message:
		return v, nil
	case Message:
		return &v, nil
	case contract.Notification:
		formatted, err := c.Format(v, notifiable)
		if err != nil {
			return nil, err
		}
		msg, ok := formatted.(*Message)
		if !ok {
			return nil, oops.Code(errs.CodeBadRequest).Errorf("database: formatted message is not *database.Message, got %T", formatted)
		}
		return msg, nil
	default:
		return nil, oops.Code(errs.CodeBadRequest).Errorf("database: expected *database.Message, got %T", message)
	}
}

// Send 执行数据库通知入库持久化。
func (c *Channel) Send(ctx context.Context, notifiable any, message any) error {
	msg, err := c.extractMessage(notifiable, message)
	if err != nil {
		return err
	}

	if notifiable == nil {
		return oops.Code(errs.CodeNotificationMissingRecipient).Errorf("database: notifiable entity is nil")
	}

	target := contract.ToNotifiable(notifiable)
	if target == nil {
		return oops.Code(errs.CodeNotificationMissingRecipient).Errorf("database: cannot resolve notifiable target")
	}

	notifiableID := target.GetNotifiableID()
	if notifiableID == "" {
		return oops.Code(errs.CodeNotificationMissingRecipientID).Errorf("database: recipient ID cannot be empty")
	}
	notifiableType := cmp.Or(target.GetNotifiableType(), contract.DefaultNotifiableType)

	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return oops.Code(errs.CodeNotificationEncodeError).In("DatabaseChannel.Send").Wrap(err)
	}

	if c.db == nil {
		return oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available for notification")
	}

	notif := &models.Notification{
		Type:           msg.Type,
		NotifiableType: notifiableType,
		NotifiableID:   notifiableID,
		Data:           string(dataBytes),
	}

	_, err = database.ResolveDB(ctx, c.db).NewInsert().Model(notif).Exec(ctx)
	if err != nil {
		return oops.Code(errs.CodeInternal).In("DatabaseChannel.Send").Wrap(err)
	}

	return nil
}

// MarkAsRead 将指定通知记录标记为已读。
func (c *Channel) MarkAsRead(ctx context.Context, id string) error {
	if c.db == nil {
		return oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	now := time.Now().UTC()
	if _, err := database.ResolveDB(ctx, c.db).NewUpdate().
		Model((*models.Notification)(nil)).
		Set("read_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ? AND read_at IS NULL", id).
		Exec(ctx); err != nil {
		return oops.Code(errs.CodeInternal).In("DatabaseChannel.MarkAsRead").Wrap(err)
	}
	return nil
}

// resolveRecipient 解析可通知实体的类型标识与主键 ID。
func resolveRecipient(notifiable any) (string, string, error) {
	target := contract.ToNotifiable(notifiable)
	if target == nil || target.GetNotifiableID() == "" {
		return "", "", oops.Code(errs.CodeNotificationMissingRecipientID).Errorf("database: recipient ID cannot be empty")
	}
	return cmp.Or(target.GetNotifiableType(), contract.DefaultNotifiableType), target.GetNotifiableID(), nil
}

// MarkAllAsRead 将指定接收者的所有未读通知标记为已读。
func (c *Channel) MarkAllAsRead(ctx context.Context, notifiable any) error {
	if c.db == nil {
		return oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	notifiableType, notifiableID, err := resolveRecipient(notifiable)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	_, err = database.ResolveDB(ctx, c.db).NewUpdate().
		Model((*models.Notification)(nil)).
		Set("read_at = ?", now).
		Set("updated_at = ?", now).
		Where("notifiable_type = ? AND notifiable_id = ? AND read_at IS NULL", notifiableType, notifiableID).
		Exec(ctx)
	if err != nil {
		return oops.Code(errs.CodeInternal).In("DatabaseChannel.MarkAllAsRead").Wrap(err)
	}
	return nil
}

// UnreadCount 查询指定接收者的未读通知数量。
func (c *Channel) UnreadCount(ctx context.Context, notifiable any) (int, error) {
	if c.db == nil {
		return 0, oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	notifiableType, notifiableID, err := resolveRecipient(notifiable)
	if err != nil {
		return 0, err
	}

	count, err := database.ResolveDB(ctx, c.db).NewSelect().
		Model((*models.Notification)(nil)).
		Where("notifiable_type = ? AND notifiable_id = ? AND read_at IS NULL", notifiableType, notifiableID).
		Count(ctx)
	if err != nil {
		return 0, oops.Code(errs.CodeInternal).In("DatabaseChannel.UnreadCount").Wrap(err)
	}
	return int(count), nil
}

// Paginate 分页查询指定接收者的通知列表。
// 区间钳制与 LIMIT/OFFSET 全部交给 database.Pagination，与用户列表共用同一套分页规则；
// ScanAndCount 的计数查询会自动剔除 ORDER BY 与分页子句，因此 total 是全量计数而非页内计数。
func (c *Channel) Paginate(ctx context.Context, notifiable any, page, perPage int, unreadOnly bool) ([]models.Notification, int, error) {
	if c.db == nil {
		return nil, 0, oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	notifiableType, notifiableID, err := resolveRecipient(notifiable)
	if err != nil {
		return nil, 0, err
	}

	var items []models.Notification
	q := database.ResolveDB(ctx, c.db).NewSelect().
		Model(&items).
		Where("notifiable_type = ? AND notifiable_id = ?", notifiableType, notifiableID)
	if unreadOnly {
		q = q.Where("read_at IS NULL")
	}
	q = q.Order("created_at DESC")

	count, err := database.Pagination{Page: page, PageSize: perPage}.Apply(q).ScanAndCount(ctx)
	if err != nil {
		return nil, 0, oops.Code(errs.CodeInternal).In("DatabaseChannel.Paginate").Wrap(err)
	}

	return items, int(count), nil
}

// PaginateUnread 仅分页查询指定接收者的未读通知列表。
func (c *Channel) PaginateUnread(ctx context.Context, notifiable any, page, perPage int) ([]models.Notification, int, error) {
	return c.Paginate(ctx, notifiable, page, perPage, true)
}

// Delete 软删除指定的通知记录。
func (c *Channel) Delete(ctx context.Context, id string) error {
	if c.db == nil {
		return oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	_, err := database.ResolveDB(ctx, c.db).NewDelete().
		Model((*models.Notification)(nil)).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return oops.Code(errs.CodeInternal).In("DatabaseChannel.Delete").Wrap(err)
	}
	return nil
}

// ClearAll 软删除指定接收者的所有通知记录。
func (c *Channel) ClearAll(ctx context.Context, notifiable any) error {
	if c.db == nil {
		return oops.Code(errs.CodeDatabaseUnavailable).Errorf("database: connection is not available")
	}
	notifiableType, notifiableID, err := resolveRecipient(notifiable)
	if err != nil {
		return err
	}

	_, err = database.ResolveDB(ctx, c.db).NewDelete().
		Model((*models.Notification)(nil)).
		Where("notifiable_type = ? AND notifiable_id = ?", notifiableType, notifiableID).
		Exec(ctx)
	if err != nil {
		return oops.Code(errs.CodeInternal).In("DatabaseChannel.ClearAll").Wrap(err)
	}
	return nil
}
