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

// ctxCommitHooksKey 是 context 中存放事务提交后回调的私有键类型。
type ctxCommitHooksKey struct{}

// commitHooks 承载同一次事务内登记的提交后回调。
type commitHooks struct {
	fns []func()
}

// OnCommit 登记一个仅在本层事务成功提交后执行的回调。
//
// 嵌套事务共用最外层事务的提交点：本函数只做登记，真正的执行交给打开事务的那一次
// RunInTx，因此回调绝不会早于提交执行。当前不在事务中时立即执行——此时不存在事务边界，
// 立即执行与"提交后"等价。
//
// 用途：数据库之外的副作用（如删除被替换的物理文件）无法随事务回滚。若在闭包内直接执行，
// 外层事务一旦回滚就会留下指向已删文件的悬空记录；登记为提交后回调才是安全的顺序。
func OnCommit(ctx context.Context, fn func()) {
	if fn == nil {
		return
	}
	if hooks, ok := ctx.Value(ctxCommitHooksKey{}).(*commitHooks); ok && hooks != nil {
		hooks.fns = append(hooks.fns, fn)
		return
	}
	fn()
}

// RunInTx 在事务闭包中执行业务逻辑，自动将活跃事务实例注入上下文 ctx 中。
// 闭包内部调用的任何 Model、Repository 或 Service 只要传入 ctx，即可通过 database.ResolveDB(ctx, fallback)
// 透明复用此事务，彻底杜绝显式多层穿透传递 &tx 的样板代码。
// 若当前上下文已处于事务中，则直接复用当前事务，提供天然幂等安全的事务边界。
//
// 经 OnCommit 登记的回调只在真正打开并提交事务的这一层执行；回滚时一律丢弃。
func RunInTx(ctx context.Context, db bun.IDB, fn func(ctx context.Context) error) error {
	if active := DBFromContext(ctx); active != nil {
		return fn(ctx)
	}
	bunDB := UnwrapBunDB(db)
	if bunDB == nil {
		return fn(WithDB(ctx, db))
	}

	hooks := &commitHooks{}
	err := bunDB.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
		return fn(context.WithValue(WithDB(txCtx, &tx), ctxCommitHooksKey{}, hooks))
	})
	if err != nil {
		// 事务已回滚，提交后回调必须作废。
		return err
	}

	// 事务已提交，此刻执行数据库之外的副作用才是安全的。
	for _, hook := range hooks.fns {
		hook()
	}
	return nil
}
