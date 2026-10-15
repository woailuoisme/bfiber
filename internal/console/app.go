package console

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"bfiber/internal/console/core"
)

// AppInitOptions 封装 app:init 命令的参数与依赖容器 (SRP & DIP)。
type AppInitOptions struct {
	SkipDB        bool
	SkipStorage   bool
	SkipJWT       bool
	SkipMigrate   bool
	SkipSeed      bool
	PublicStorage bool
	Bucket        string
	MaintenanceDB string
	Factory       Factory
}

// NewAppInitOptions 构造 AppInitOptions 实例。
func NewAppInitOptions(f Factory) *AppInitOptions {
	return &AppInitOptions{
		PublicStorage: true,
		MaintenanceDB: DefaultMaintenanceDB,
		Factory:       f,
	}
}

// Complete 从命令行上下文解析并补全配置。
func (o *AppInitOptions) Complete(_ *cobra.Command, _ []string) error {
	if strings.TrimSpace(o.MaintenanceDB) == "" {
		o.MaintenanceDB = DefaultMaintenanceDB
	}
	return nil
}

// Validate 校验命令选项合法性。
func (o *AppInitOptions) Validate() error {
	if strings.TrimSpace(o.MaintenanceDB) == "" {
		return &ValidationError{Field: "maintenance-db", Message: "cannot be empty"}
	}
	return nil
}

// Run 依次编排 JWT 密钥探测、数据库创建迁移播种以及对象存储桶初始化。
func (o *AppInitOptions) Run(ctx context.Context) error {
	streams := o.Factory.IOStreams()
	core.TitleTo(streams.Out, "Initializing bfiber application environment...")

	// 1. JWT 密钥检查与自动写入
	if !o.SkipJWT {
		if err := ensureJWTSecret(ctx, o.Factory, streams); err != nil {
			return err
		}
	} else {
		core.InfoTo(streams.Out, "[1/3] Skipping JWT secret initialization (--skip-jwt).")
	}

	// 2. 数据库建库、迁移与初始数据播种
	if !o.SkipDB {
		core.TitleTo(streams.Out, "[2/3] Initializing PostgreSQL database...")
		dbOpts := NewDBInitOptions(o.Factory)
		dbOpts.SkipMigrate = o.SkipMigrate
		dbOpts.SkipSeed = o.SkipSeed
		dbOpts.MaintenanceDB = o.MaintenanceDB

		if err := dbOpts.Run(ctx); err != nil {
			return fmt.Errorf("app:init failed during database initialization: %w", err)
		}
	} else {
		core.InfoTo(streams.Out, "[2/3] Skipping database initialization (--skip-db).")
	}

	// 3. 存储桶或本地上传目录初始化
	if !o.SkipStorage {
		core.TitleTo(streams.Out, "[3/3] Initializing storage bucket / directories...")
		storageOpts := NewStorageInitOptions(o.Factory)
		storageOpts.Bucket = o.Bucket
		storageOpts.Public = o.PublicStorage

		if err := storageOpts.Run(ctx); err != nil {
			return fmt.Errorf("app:init failed during storage initialization: %w", err)
		}
	} else {
		core.InfoTo(streams.Out, "[3/3] Skipping storage initialization (--skip-storage).")
	}

	core.SuccessTo(streams.Out, "Application environment initialization finished successfully! 🎉")
	return nil
}

// ensureJWTSecret 检查 .env 文件中的 JWT_SECRET，若未配置则自动生成并持久化写入。
func ensureJWTSecret(ctx context.Context, f Factory, streams *core.IOStreams) error {
	core.TitleTo(streams.Out, "[1/3] Verifying and generating JWT_SECRET...")
	if envBytes, err := os.ReadFile(".env"); err == nil {
		match := jwtSecretRegexp.FindSubmatch(envBytes)
		if len(match) > 1 && strings.TrimSpace(string(match[1])) != "" {
			core.InfoTo(streams.Out, "• JWT_SECRET is already configured in .env.")
			return nil
		}
	}

	jwtOpts := NewJWTSecretOptions(f)
	if err := jwtOpts.Run(ctx); err != nil {
		return fmt.Errorf("app:init failed during JWT secret initialization: %w", err)
	}
	return nil
}

// newAppInitCmd 构造 app:init 命令，并挂载别名 init。
func newAppInitCmd(f Factory) *cobra.Command {
	opts := NewAppInitOptions(f)

	cmd := &cobra.Command{
		Use:     "app:init",
		Aliases: []string{"init"},
		Short:   "Initialize entire application environment (JWT secret, PostgreSQL database, and storage bucket)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Complete(cmd, args); err != nil {
				return err
			}
			if err := opts.Validate(); err != nil {
				return err
			}
			return opts.Run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&opts.SkipDB, "skip-db", false, "Skip PostgreSQL database creation, migration, and seeding")
	cmd.Flags().BoolVar(&opts.SkipStorage, "skip-storage", false, "Skip storage bucket or local directory initialization")
	cmd.Flags().BoolVar(&opts.SkipJWT, "skip-jwt", false, "Skip JWT secret key check and generation")
	cmd.Flags().BoolVar(&opts.SkipMigrate, "skip-migrate", false, "Skip applying Atlas migrations in database init")
	cmd.Flags().BoolVar(&opts.SkipSeed, "skip-seed", false, "Skip database seeding in database init")
	cmd.Flags().BoolVar(&opts.PublicStorage, "public-storage", true, "Configure anonymous public-read bucket policy")
	cmd.Flags().StringVar(&opts.Bucket, "bucket", "", "Target bucket name override")
	cmd.Flags().StringVar(&opts.MaintenanceDB, "maintenance-db", DefaultMaintenanceDB, "PostgreSQL maintenance database name for CREATE DATABASE")

	return cmd
}
