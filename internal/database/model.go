// Package database 负责 PostgreSQL 连接池管理、事务工作单元与 Base 实体基座。
package database

import (
	"context"
	"time"

	"github.com/dromara/carbon/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ============================================================================
// 1. 实体基座与钩子 (Base Model & Lifecycle Hooks)
// ============================================================================

// Base 提供三张表共用的主键与时间戳，并在写入前统一补齐。
type Base struct {
	ID        string    `bun:"id,pk"`
	CreatedAt time.Time `bun:"created_at"`
	UpdatedAt time.Time `bun:"updated_at"`
}

func toCarbon(t time.Time) *carbon.Carbon {
	if t.IsZero() {
		return carbon.NewCarbon()
	}
	return carbon.CreateFromStdTime(t)
}

func toCarbonPtr(t *time.Time) *carbon.Carbon {
	if t == nil {
		return carbon.NewCarbon()
	}
	return toCarbon(*t)
}

// CreatedAtCarbon 返回创建时间的 *carbon.Carbon 包装实例，便于业务层进行链式时间计算与相对时间展示。
func (b *Base) CreatedAtCarbon() *carbon.Carbon {
	if b == nil {
		return carbon.NewCarbon()
	}
	return toCarbon(b.CreatedAt)
}

// UpdatedAtCarbon 返回更新时间的 *carbon.Carbon 包装实例。
func (b *Base) UpdatedAtCarbon() *carbon.Carbon {
	if b == nil {
		return carbon.NewCarbon()
	}
	return toCarbon(b.UpdatedAt)
}

// UTCPtr 返回时间指针的 UTC 形式，nil 时返回 nil。
func UTCPtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}

// GetID 返回模型的主键 ID（满足 Model 契约）。
func (b *Base) GetID() string {
	if b == nil {
		return ""
	}
	return b.ID
}

// SetID 设置模型的主键 ID（满足 Model 契约）。
func (b *Base) SetID(id string) {
	if b != nil {
		b.ID = id
	}
}

// GetCreatedAt 返回模型的创建时间（满足 Timestamped 契约）。
func (b *Base) GetCreatedAt() time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.CreatedAt
}

// GetUpdatedAt 返回模型的更新时间。
func (b *Base) GetUpdatedAt() time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.UpdatedAt
}

// BeforeAppendModel 在写入前生成 UUIDv7 并刷新时间戳，数据库默认值仅作兜底。
func (b *Base) BeforeAppendModel(_ context.Context, query bun.Query) error {
	now := time.Now().UTC()

	switch query.(type) {
	case *bun.InsertQuery:
		if b.ID == "" {
			b.ID = uuid.Must(uuid.NewV7()).String()
		}
		b.CreatedAt = now
		b.UpdatedAt = now
	case *bun.UpdateQuery:
		b.UpdatedAt = now
	}
	return nil
}

// SoftDeletes 嵌入基座，为模型提供统一的软删除字段与便捷方法。
type SoftDeletes struct {
	DeletedAt *time.Time `bun:"deleted_at,soft_delete"`
}

// GetDeletedAt 返回软删除时间指针。
func (s *SoftDeletes) GetDeletedAt() *time.Time {
	if s == nil {
		return nil
	}
	return s.DeletedAt
}

// SetDeletedAt 设置软删除时间指针。
func (s *SoftDeletes) SetDeletedAt(t *time.Time) {
	if s != nil {
		s.DeletedAt = t
	}
}

// DeletedAtCarbon 返回软删除时间的 *carbon.Carbon 包装实例。
func (s *SoftDeletes) DeletedAtCarbon() *carbon.Carbon {
	if s == nil {
		return carbon.NewCarbon()
	}
	return toCarbonPtr(s.DeletedAt)
}

// ============================================================================
// 2. 核心抽象接口 (Core Interfaces)
// ============================================================================

// Model 约束挂载主键 ID 的实体模型。
type Model interface {
	GetID() string
	SetID(id string)
}

// Timestamped 约束拥有标准时间戳的实体模型。
type Timestamped interface {
	Model
	GetCreatedAt() time.Time
}

// SoftDeletable 约束支持软删除的实体模型。
type SoftDeletable interface {
	Model
	GetDeletedAt() *time.Time
	SetDeletedAt(t *time.Time)
}

// IsTrashed 判断软删除实体当前是否处于被软删除状态。
func IsTrashed(m SoftDeletable) bool {
	return m != nil && m.GetDeletedAt() != nil
}

// DeletedAtCarbon 从任意实现了 SoftDeletable 契约的模型中提取软删除时间的 *carbon.Carbon 包装实例。
func DeletedAtCarbon(m SoftDeletable) *carbon.Carbon {
	if m == nil {
		return carbon.NewCarbon()
	}
	return toCarbonPtr(m.GetDeletedAt())
}

// ============================================================================
// 3. 上下文数据库与事务感知 (Context-Aware DB Seam)
// ============================================================================

type ctxDBKey struct{}

// WithDB 将活跃数据库连接或事务实例注入 Context。
func WithDB(ctx context.Context, db bun.IDB) context.Context {
	return context.WithValue(ctx, ctxDBKey{}, db)
}

// DBFromContext 从 Context 提取活跃数据库或事务连接；若未绑定则返回 nil。
func DBFromContext(ctx context.Context) bun.IDB {
	if db, ok := ctx.Value(ctxDBKey{}).(bun.IDB); ok && db != nil {
		return db
	}
	return nil
}

// ResolveDB 优先从 Context 提取活跃事务或连接，无活跃连接时回退至 fallback。
func ResolveDB(ctx context.Context, fallback bun.IDB) bun.IDB {
	if active := DBFromContext(ctx); active != nil {
		return active
	}
	return fallback
}

// UnwrapBunDB 尝试从 bun.IDB 实例中提取底层的 *bun.DB 实例（支持直接断言与 BunDB/Unwrap 接口解包）。
func UnwrapBunDB(idb bun.IDB) *bun.DB {
	switch v := idb.(type) {
	case *bun.DB:
		return v
	case interface{ BunDB() *bun.DB }:
		return v.BunDB()
	case interface{ Unwrap() *bun.DB }:
		return v.Unwrap()
	default:
		return nil
	}
}

// RunInTx 在事务闭包中执行业务逻辑，自动将活跃事务实例注入上下文 ctx 中。
// 闭包内部调用的任何 Model、Repository 或 Service 只要传入 ctx，即可通过 database.ResolveDB(ctx, fallback)
// 透明复用此事务，彻底杜绝显式多层穿透传递 &tx 的样板代码。
// 若当前上下文已处于事务中，则直接复用当前事务，提供天然幂等安全的事务边界。
func RunInTx(ctx context.Context, db bun.IDB, fn func(ctx context.Context) error) error {
	if active := DBFromContext(ctx); active != nil {
		return fn(ctx)
	}
	bunDB := UnwrapBunDB(db)
	if bunDB == nil {
		return fn(WithDB(ctx, db))
	}
	return bunDB.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
		return fn(WithDB(txCtx, &tx))
	})
}
