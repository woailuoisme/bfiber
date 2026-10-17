package factories

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/uptrace/bun"

	"bfiber/internal/database"
)

// ErrDatabaseRequired 在缺少数据库连接且调用持久化方法时返回。
var ErrDatabaseRequired = errors.New("factory: database is required for Create or CreateMany")

// DefinitionFunc 定义单个实体的默认属性生成函数。
type DefinitionFunc[T any] func(f *gofakeit.Faker) *T

// StateFunc 定义对实体状态进行修改的函数。
type StateFunc[T any] func(item *T)

// SequenceFunc 定义根据索引动态计算字段的函数。
type SequenceFunc[T any] func(item *T, index int)

// HookFunc 定义生成后或持久化后的生命周期钩子函数。
type HookFunc[T any] func(ctx context.Context, item *T) error

// ProgressFunc 定义批量持久化每批写入完成后的进度通知函数。
type ProgressFunc func(current, total int)

// Factory 是泛型模型工厂基座，支持不可变链式调用、内存快速构建与分块事务持久化。
type Factory[T any] struct {
	db            bun.IDB
	faker         *gofakeit.Faker
	count         int
	chunkSize     int
	definition    DefinitionFunc[T]
	states        []StateFunc[T]
	sequences     []SequenceFunc[T]
	afterMaking   []HookFunc[T]
	afterCreating []HookFunc[T]
	onProgress    ProgressFunc
}

// New 构造一个新的泛型模型工厂基座。
func New[T any](db bun.IDB, def DefinitionFunc[T]) *Factory[T] {
	var seed uint64
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err == nil {
		seed = binary.LittleEndian.Uint64(b[:])
	}

	return &Factory[T]{
		db:            db,
		faker:         gofakeit.New(seed),
		count:         1,
		chunkSize:     1000,
		definition:    def,
		states:        make([]StateFunc[T], 0),
		sequences:     make([]SequenceFunc[T], 0),
		afterMaking:   make([]HookFunc[T], 0),
		afterCreating: make([]HookFunc[T], 0),
	}
}

// clone 产生当前工厂实例的浅拷贝副本，保持状态与切片的不可变隔离。
func (f *Factory[T]) clone() *Factory[T] {
	copied := *f
	copied.states = append([]StateFunc[T](nil), f.states...)
	copied.sequences = append([]SequenceFunc[T](nil), f.sequences...)
	copied.afterMaking = append([]HookFunc[T](nil), f.afterMaking...)
	copied.afterCreating = append([]HookFunc[T](nil), f.afterCreating...)
	return &copied
}

// Count 设置批量生成的数量，返回工厂副本。
func (f *Factory[T]) Count(count int) *Factory[T] {
	cp := f.clone()
	cp.count = max(count, 1)
	return cp
}

// ChunkSize 设置批量持久化时的分块大小，返回工厂副本。
func (f *Factory[T]) ChunkSize(size int) *Factory[T] {
	cp := f.clone()
	if size < 1 {
		size = 1000
	}
	cp.chunkSize = size
	return cp
}

// OnProgress 注册批量持久化分批写入时的进度回调钩子，返回工厂副本。
func (f *Factory[T]) OnProgress(fn ProgressFunc) *Factory[T] {
	cp := f.clone()
	cp.onProgress = fn
	return cp
}

// Seed 设置确定性随机数种子，返回工厂副本。
func (f *Factory[T]) Seed(seed uint64) *Factory[T] {
	cp := f.clone()
	cp.faker = gofakeit.New(seed)
	return cp
}

// State 附加状态转换函数，返回工厂副本。
func (f *Factory[T]) State(fn StateFunc[T]) *Factory[T] {
	if fn == nil {
		return f
	}
	cp := f.clone()
	cp.states = append(cp.states, fn)
	return cp
}

// Sequence 附加基于下标的序列计算函数，返回工厂副本。
func (f *Factory[T]) Sequence(fn SequenceFunc[T]) *Factory[T] {
	if fn == nil {
		return f
	}
	cp := f.clone()
	cp.sequences = append(cp.sequences, fn)
	return cp
}

// AfterMaking 注册实体在内存构建完成后的回调钩子，返回工厂副本。
func (f *Factory[T]) AfterMaking(fn HookFunc[T]) *Factory[T] {
	if fn == nil {
		return f
	}
	cp := f.clone()
	cp.afterMaking = append(cp.afterMaking, fn)
	return cp
}

// AfterCreating 注册实体在持久化成功后的回调钩子，返回工厂副本。
func (f *Factory[T]) AfterCreating(fn HookFunc[T]) *Factory[T] {
	if fn == nil {
		return f
	}
	cp := f.clone()
	cp.afterCreating = append(cp.afterCreating, fn)
	return cp
}

// DB 返回工厂关联的数据库连接。
func (f *Factory[T]) DB() bun.IDB {
	return f.db
}

// WithDB 绑定或切换工厂关联的数据库连接句柄（支持传入事务句柄），返回工厂副本。
func (f *Factory[T]) WithDB(db bun.IDB) *Factory[T] {
	cp := f.clone()
	cp.db = db
	return cp
}

// Faker 返回工厂当前的随机生成器。
func (f *Factory[T]) Faker() *gofakeit.Faker {
	return f.faker
}

// Make 在内存中生成单个实体（不写数据库），并依次应用状态与 AfterMaking 钩子。
func (f *Factory[T]) Make(ctx context.Context) (*T, error) {
	item := f.makeOne(0)
	for _, hook := range f.afterMaking {
		if err := hook(ctx, item); err != nil {
			return nil, fmt.Errorf("factory after-making hook failed: %w", err)
		}
	}
	return item, nil
}

// MakeMany 在内存中生成指定数量的实体切片。
func (f *Factory[T]) MakeMany(ctx context.Context, counts ...int) ([]*T, error) {
	total := f.count
	if len(counts) > 0 && counts[0] > 0 {
		total = counts[0]
	}

	items := make([]*T, 0, total)
	for i := range total {
		item := f.makeOne(i)
		for _, hook := range f.afterMaking {
			if err := hook(ctx, item); err != nil {
				return nil, fmt.Errorf("factory after-making hook failed at index %d: %w", i, err)
			}
		}
		items = append(items, item)
	}
	return items, nil
}

type ctxPersistingKey struct{}

func withPersisting(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxPersistingKey{}, true)
}

func isPersisting(ctx context.Context) bool {
	v, _ := ctx.Value(ctxPersistingKey{}).(bool)
	return v
}

// Create 在内存生成实体并持久化保存至数据库，同时触发所有钩子。
func (f *Factory[T]) Create(ctx context.Context) (*T, error) {
	if f.db == nil {
		return nil, ErrDatabaseRequired
	}

	persistCtx := withPersisting(ctx)
	item, err := f.Make(persistCtx)
	if err != nil {
		return nil, err
	}

	db := database.ResolveDB(ctx, f.db)
	if _, err := db.NewInsert().Model(item).Exec(ctx); err != nil {
		return nil, fmt.Errorf("factory create failed to insert model: %w", err)
	}

	for _, hook := range f.afterCreating {
		if err := hook(persistCtx, item); err != nil {
			return nil, fmt.Errorf("factory after-creating hook failed: %w", err)
		}
	}
	return item, nil
}

// CreateMany 批量创建并持久化保存实体切片。
// 会在单一事务中以 ChunkSize 为单位分批写入数据库，最大化写入吞吐。
func (f *Factory[T]) CreateMany(ctx context.Context, counts ...int) ([]*T, error) {
	if f.db == nil {
		return nil, ErrDatabaseRequired
	}

	total := f.count
	if len(counts) > 0 && counts[0] > 0 {
		total = counts[0]
	}

	persistCtx := withPersisting(ctx)
	items, err := f.MakeMany(persistCtx, total)
	if err != nil {
		return nil, err
	}

	if total == 0 {
		return items, nil
	}

	runInTx := func(txCtx context.Context, tx bun.IDB) error {
		chunkSize := f.chunkSize
		for i := 0; i < len(items); i += chunkSize {
			end := min(i+chunkSize, len(items))
			chunk := items[i:end]
			if _, err := tx.NewInsert().Model(&chunk).Exec(txCtx); err != nil {
				return fmt.Errorf("factory create-many insert chunk [%d:%d] failed: %w", i, end, err)
			}
			if f.onProgress != nil {
				f.onProgress(end, len(items))
			}
		}

		for idx, item := range items {
			for _, hook := range f.afterCreating {
				if err := hook(txCtx, item); err != nil {
					return fmt.Errorf("factory after-creating hook failed at index %d: %w", idx, err)
				}
			}
		}
		return nil
	}

	// 统一走 database.RunInTx：f.db 已是活跃事务时天然复用，无需自行分类型处理。
	if err := database.RunInTx(ctx, f.db, func(txCtx context.Context) error {
		return runInTx(txCtx, database.ResolveDB(txCtx, f.db))
	}); err != nil {
		return nil, err
	}

	return items, nil
}

// makeOne 内部构建单个实体并应用 State 与 Sequence。
func (f *Factory[T]) makeOne(index int) *T {
	var item *T
	if f.definition != nil {
		item = f.definition(f.faker)
	} else {
		item = new(T)
	}

	for _, seq := range f.sequences {
		seq(item, index)
	}
	for _, state := range f.states {
		state(item)
	}
	return item
}

// Has 将子实体工厂关联到父工厂（一对多级联）。
// 当父实体在 Create/CreateMany 持久化写入数据库后，自动为每个父实体触发 child.CreateMany(ctx)，
// 并通过 setFK 回调将父级的主键或关联字段赋给子级实体。
// 该级联完全在父实体的同一事务上下文中递归执行。
func Has[T any, R any](parent *Factory[T], child *Factory[R], setFK func(child *R, parent *T)) *Factory[T] {
	if parent == nil || child == nil || setFK == nil {
		return parent
	}
	return parent.AfterCreating(func(ctx context.Context, p *T) error {
		subFactory := child.clone().State(func(c *R) {
			setFK(c, p)
		})
		if subFactory.db == nil {
			subFactory.db = parent.db
		}
		_, err := subFactory.CreateMany(ctx)
		return err
	})
}

// For 将父实体工厂关联到子工厂（反向属于关联）。
// 在子实体生成/持久化前，确保父实体已生成或持久化，并通过 setFK 回调将父实体字段回填至子实体。
func For[T any, P any](child *Factory[T], parent *Factory[P], setFK func(child *T, parent *P)) *Factory[T] {
	if child == nil || parent == nil || setFK == nil {
		return child
	}
	return child.AfterMaking(func(ctx context.Context, c *T) error {
		parentFactory := parent.clone()
		if parentFactory.db == nil {
			parentFactory.db = child.db
		}

		if isPersisting(ctx) && child.db != nil {
			p, err := parentFactory.Create(ctx)
			if err != nil {
				return fmt.Errorf("factory for: failed to create parent entity: %w", err)
			}
			setFK(c, p)
		} else {
			p, err := parentFactory.Make(ctx)
			if err != nil {
				return fmt.Errorf("factory for: failed to make parent entity: %w", err)
			}
			setFK(c, p)
		}
		return nil
	})
}
