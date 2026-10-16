package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"bfiber/internal/config"
	"bfiber/internal/console/core"
)

// anonymousProbeTimeout 匿名可读性探测的超时上限。
const anonymousProbeTimeout = 5 * time.Second

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
		if err := client.SetBucketPolicy(ctx, bucketName, publicReadPolicy(bucketName)); err != nil {
			core.WarnTo(streams.ErrOut, "Warning: failed to set bucket policy: %v", err)
		} else {
			core.SuccessTo(streams.Out, "✓ Public-read policy configured for [%s].", bucketName)
		}
	}

	// 无论策略是否设置成功，都实测一次「应用会产出的直连地址」是否真的可读：
	// 基址取 AWS_URL（配置了它就是 app 产出的形态），否则按 endpoint 与桶名推导。
	reportDirectURLReadiness(ctx, streams, client, s3Cfg, bucketName)

	core.SuccessTo(streams.Out, "Storage initialization completed successfully.")
	return nil
}

// publicReadPolicy 构造允许匿名读取指定桶内对象的桶策略。
func publicReadPolicy(bucketName string) string {
	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"PublicReadGetObject","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, bucketName)
}

// reportDirectURLReadiness 实测「应用会产出的直连对象 URL」当前是否真的可读，并给出确定结论。
// 判据用桶内一个真实对象的无凭据 GET：web 端点对「桶未开放」与「对象不存在」都返回 404，
// 用不存在的键探测会把「未开放」误判为「可读」，因此不能用 403/404 区分。
func reportDirectURLReadiness(
	ctx context.Context,
	streams *core.IOStreams,
	client *minio.Client,
	s3Cfg config.S3StorageConfig,
	bucketName string,
) {
	probeKey, err := firstObjectKey(ctx, client, bucketName)
	if err != nil {
		core.WarnTo(streams.ErrOut, "• Cannot verify anonymous read access on bucket [%s]: %v", bucketName, err)
		return
	}
	if probeKey == "" {
		core.InfoTo(streams.Out, "• Bucket [%s] is empty: nothing to verify anonymous read access against.", bucketName)
		return
	}

	readable, err := urlReadableAnonymously(ctx, effectivePublicBase(s3Cfg, bucketName), probeKey)
	switch {
	case err != nil:
		core.WarnTo(streams.ErrOut, "• Cannot verify anonymous read access on bucket [%s]: %v", bucketName, err)
	case readable:
		core.InfoTo(streams.Out, "• Bucket [%s] is anonymously readable: direct object URLs keep working.", bucketName)
	default:
		core.WarnTo(streams.ErrOut, "• Bucket [%s] is NOT anonymously readable: direct object URLs will fail.", bucketName)
		if strings.TrimSpace(s3Cfg.PublicURL) == "" {
			core.WarnTo(streams.ErrOut, "  Open the bucket for public read out-of-band, set AWS_URL to a fronting base, or serve objects through presigned URLs / imgproxy.")
		} else {
			core.WarnTo(streams.ErrOut, "  AWS_URL is set: make sure that fronting base (CDN / reverse proxy) authenticates to the origin itself.")
		}
	}
}

// effectivePublicBase 返回应用生成直连对象 URL 时实际使用的基础地址：
// 配置了 AWS_URL 即它（含应用自己的拼接规则：去掉尾斜杠后加 /<key>），否则按 endpoint 与桶名推导。
func effectivePublicBase(s3Cfg config.S3StorageConfig, bucketName string) string {
	if publicURL := strings.TrimSuffix(strings.TrimSpace(s3Cfg.PublicURL), "/"); publicURL != "" {
		return publicURL
	}

	scheme := "http"
	if s3Cfg.UseSSL {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, strings.Trim(strings.TrimSpace(s3Cfg.Endpoint), "/"), bucketName)
}

// firstObjectKey 返回桶内任意一个真实对象键，用于构造无歧义的匿名探测；空桶返回空串。
// 依赖 minio-go 的契约：列举错误以 Err 字段条目投递而非静默关通道，
// 否则这里的空串会把「列举失败」误报成「桶为空」。
func firstObjectKey(ctx context.Context, client *minio.Client, bucketName string) (string, error) {
	objects := client.ListObjects(ctx, bucketName, minio.ListObjectsOptions{MaxKeys: 1, Recursive: true})

	select {
	case object, ok := <-objects:
		if !ok {
			return "", nil
		}
		if object.Err != nil {
			return "", object.Err
		}
		return object.Key, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// urlReadableAnonymously 以无凭据 GET 判定给定对象地址是否可匿名读取：
// 200 即可读；403 / 404 均视为不可读；其它状态无法判定，如实上报。
// 探测不缓冲响应体（对象可能很大，只看状态码）。
func urlReadableAnonymously(ctx context.Context, base, objectKey string) (bool, error) {
	// 键内的「/」是路径分隔符，不能整体转义：走 URL 的 Path 编码规则，
	// 只转义空格、?、# 等保留字符，保留分隔符（PathEscape 会把 / 也转义掉）。
	target := url.URL{Path: "/" + objectKey}

	resp, err := resty.New().SetTimeout(anonymousProbeTimeout).
		SetDoNotParseResponse(true).
		R().
		SetContext(ctx).
		Get(base + target.EscapedPath())
	if err != nil {
		return false, err
	}

	// 不解析响应体时由调用方负责关闭，避免连接悬挂。
	if resp.RawResponse != nil && resp.RawResponse.Body != nil {
		_ = resp.RawResponse.Body.Close()
	}

	switch resp.StatusCode() {
	case http.StatusOK:
		return true, nil
	case http.StatusForbidden, http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status %d from anonymous probe", resp.StatusCode())
	}
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
