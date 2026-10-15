package console

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"bfiber/internal/config"
	"bfiber/internal/console/core"
)

// StorageInitOptions 封装 storage:init 命令的参数与依赖容器 (SRP & DIP)。
type StorageInitOptions struct {
	Bucket  string
	Public  bool
	Factory Factory
}

// NewStorageInitOptions 构造 StorageInitOptions 实例。
func NewStorageInitOptions(f Factory) *StorageInitOptions {
	return &StorageInitOptions{
		Public:  true,
		Factory: f,
	}
}

// Complete 从命令行上下文解析并补全配置。
func (o *StorageInitOptions) Complete(_ *cobra.Command, _ []string) error {
	return nil
}

// Validate 校验命令选项合法性。
func (o *StorageInitOptions) Validate() error {
	return nil
}

// Run 执行对象存储桶或本地文件目录初始化。
func (o *StorageInitOptions) Run(ctx context.Context) error {
	cfg, err := o.Factory.Config()
	if err != nil {
		return fmt.Errorf("storage: load config failed: %w", err)
	}

	streams := o.Factory.IOStreams()

	switch cfg.Storage.Driver {
	case config.StorageDriverLocal:
		return initLocalDirectory(streams, cfg.Storage.Local.Root)
	case config.StorageDriverS3:
		bucket := strings.TrimSpace(o.Bucket)
		if bucket == "" {
			bucket = strings.TrimSpace(cfg.Storage.S3.Bucket)
		}
		return initS3Bucket(ctx, streams, cfg.Storage.S3, bucket, o.Public)
	default:
		return fmt.Errorf("storage: unsupported storage driver %q", cfg.Storage.Driver)
	}
}

// initLocalDirectory 初始化本地上传存储目录。
func initLocalDirectory(streams *core.IOStreams, rawRoot string) error {
	root := strings.TrimSpace(rawRoot)
	if root == "" {
		return errors.New("storage: STORAGE_LOCAL_ROOT cannot be empty when FILESYSTEM_DISK=local")
	}

	core.TitleTo(streams.Out, fmt.Sprintf("Initializing local storage directory [%s]...", root))
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("storage: failed to create local storage directory %q: %w", root, err)
	}
	core.SuccessTo(streams.Out, "Local storage directory [%s] is ready.", root)
	return nil
}

// initS3Bucket 探测并初始化 Garage S3 / MinIO 存储桶及访问策略。
func initS3Bucket(ctx context.Context, streams *core.IOStreams, s3Cfg config.S3StorageConfig, bucketName string, public bool) error {
	if bucketName == "" {
		return errors.New("storage: AWS_BUCKET cannot be empty (specify via .env or --bucket flag)")
	}
	if strings.TrimSpace(s3Cfg.Endpoint) == "" {
		return errors.New("storage: AWS_ENDPOINT cannot be empty when FILESYSTEM_DISK=s3")
	}
	if strings.TrimSpace(s3Cfg.AccessKeyID) == "" || strings.TrimSpace(s3Cfg.SecretAccessKey) == "" {
		return errors.New("storage: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY cannot be empty")
	}

	region := strings.TrimSpace(s3Cfg.Region)
	if region == "" {
		region = "us-east-1"
	}

	core.TitleTo(streams.Out, fmt.Sprintf("Connecting to S3/Garage endpoint [%s]...", s3Cfg.Endpoint))
	client, err := minio.New(s3Cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3Cfg.AccessKeyID, s3Cfg.SecretAccessKey, ""),
		Secure: s3Cfg.UseSSL,
		Region: region,
	})
	if err != nil {
		return fmt.Errorf("storage: failed to initialize S3 client: %w", err)
	}

	core.TitleTo(streams.Out, fmt.Sprintf("Probing bucket [%s]...", bucketName))
	exists, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("storage: failed to probe bucket [%s]: %w", bucketName, err)
	}

	if !exists {
		core.InfoTo(streams.Out, "• Bucket [%s] does not exist, creating...", bucketName)
		if err := client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{Region: region}); err != nil {
			return fmt.Errorf("storage: failed to create bucket [%s]: %w", bucketName, err)
		}
		core.SuccessTo(streams.Out, "✓ Bucket [%s] created successfully.", bucketName)
	} else {
		core.InfoTo(streams.Out, "• Bucket [%s] already exists.", bucketName)
	}

	if public {
		core.TitleTo(streams.Out, fmt.Sprintf("Applying public-read policy to bucket [%s]...", bucketName))
		policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadGetObject","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, bucketName)
		if err := client.SetBucketPolicy(ctx, bucketName, policy); err != nil {
			core.WarnTo(streams.ErrOut, "Warning: failed to set bucket policy (server might manage policy out-of-band): %v", err)
		} else {
			core.SuccessTo(streams.Out, "✓ Public-read policy configured for [%s].", bucketName)
		}
	}

	core.SuccessTo(streams.Out, "Storage initialization completed successfully.")
	return nil
}

// newStorageInitCmd 构造 storage:init 命令。
func newStorageInitCmd(f Factory) *cobra.Command {
	opts := NewStorageInitOptions(f)

	cmd := &cobra.Command{
		Use:   "storage:init",
		Short: "Initialize storage bucket (Garage S3 / MinIO) or local upload directory",
		Args:  cobra.NoArgs,
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

	cmd.Flags().StringVar(&opts.Bucket, "bucket", "", "Target bucket name (defaults to AWS_BUCKET from config)")
	cmd.Flags().BoolVar(&opts.Public, "public", true, "Configure anonymous public-read bucket policy")

	return cmd
}
