package console_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/config"
	"bfiber/internal/console"
	"bfiber/internal/console/core"
)

// listOneObjectXML 模拟桶内存在一个真实对象：探测需要真实键，
// 因为 web 端点对「桶未开放」与「对象不存在」都返回 404，不存在的键无法区分两者。
const listOneObjectXML = `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>default</Name><KeyCount>1</KeyCount><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated><Contents><Key>probe/real-object.png</Key><Size>1</Size></Contents></ListBucketResult>`

// listEmptyXML 模拟空桶：此时无法构造有歧义的探测，应如实上报「无法验证」。
const listEmptyXML = `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>default</Name><KeyCount>0</KeyCount><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated></ListBucketResult>`

// fakeS3Options 描述 fake S3 端点的行为。
type fakeS3Options struct {
	// policyUnsupported 为 true 时 SetBucketPolicy 返回未实现（模拟 Garage 的行为）。
	policyUnsupported bool
	// listXML 是对象列举请求的响应体。
	listXML string
	// probeStatus 是对真实对象键匿名 GET 的响应状态码（200 可读 / 403 或 404 不可读）。
	probeStatus int
}

// newFakeS3Server 起一个最小的 S3 兼容端点，并返回「收到的 GET 路径」读取器，
// 便于用例断言探测命中的地址形态（AWS_URL 基址 vs endpoint 推导基址）。
func newFakeS3Server(t *testing.T, opts fakeS3Options) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var getPaths []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			getPaths = append(getPaths, r.URL.Path)
			mu.Unlock()
		}

		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && opts.policyUnsupported:
			w.WriteHeader(http.StatusNotImplemented)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(opts.listXML))
		case r.Method == http.MethodGet:
			w.WriteHeader(opts.probeStatus)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), getPaths...)
	}
}

// runStorageInit 以指定 S3 端点执行一次 storage:init 并返回合并后的输出；
// mutate 允许用例在执行前微调 S3 配置（如注入 AWS_URL）。
func runStorageInit(t *testing.T, endpoint string, mutate func(*config.S3StorageConfig)) (string, error) {
	t.Helper()

	var outBuf bytes.Buffer
	streams := &core.IOStreams{Out: &outBuf, ErrOut: &outBuf}
	s3 := config.S3StorageConfig{
		Endpoint:        strings.TrimPrefix(endpoint, "http://"),
		Bucket:          "default",
		Region:          "garage",
		AccessKeyID:     "probe-access-key",
		SecretAccessKey: "probe-secret-key",
	}
	if mutate != nil {
		mutate(&s3)
	}

	testCfg := &config.Config{
		Storage: config.StorageConfig{
			Driver: config.StorageDriverS3,
			S3:     s3,
		},
	}

	f := &console.TestFactory{StreamsValue: streams, ConfigValue: testCfg}
	err := console.NewStorageInitOptions(f).Run(context.Background())

	return outBuf.String(), err
}

// TestStorageInit_S3PolicyUnsupported 覆盖服务端不支持 SetBucketPolicy 且桶不可匿名读的场景：
// 初始化仍应成功，但必须给出「直连对象 URL 会失败」的确定结论，而不是一句猜测性的警告。
func TestStorageInit_S3PolicyUnsupported(t *testing.T) {
	server, _ := newFakeS3Server(t, fakeS3Options{
		policyUnsupported: true,
		listXML:           listOneObjectXML,
		probeStatus:       http.StatusForbidden,
	})

	out, err := runStorageInit(t, server.URL, nil)
	require.NoError(t, err, "策略设置失败不应让存储初始化整体失败")

	assert.Contains(t, out, "failed to set bucket policy", "应如实上报策略设置失败的原因")
	assert.Contains(t, out, "NOT anonymously readable", "应给出实测结论")
	assert.Contains(t, out, "direct object URLs will fail", "应说明确定后果")
	assert.Contains(t, out, "Storage initialization completed successfully.")
}

// TestStorageInit_S3AnonymouslyReadable 覆盖桶本身已可匿名读的场景：
// 此时不应报出失败警告，且策略设置成功应被确认。
func TestStorageInit_S3AnonymouslyReadable(t *testing.T) {
	server, _ := newFakeS3Server(t, fakeS3Options{
		listXML:     listOneObjectXML,
		probeStatus: http.StatusOK,
	})

	out, err := runStorageInit(t, server.URL, nil)
	require.NoError(t, err)

	assert.Contains(t, out, "Public-read policy configured")
	assert.Contains(t, out, "is anonymously readable", "实测为可读时不应给出失败警告")
	assert.NotContains(t, out, "will fail")
}

// TestStorageInit_S3EmptyBucketSkipsVerification 覆盖空桶场景：没有真实对象就无法构造无歧义的探测，
// 应如实上报「空桶、无对照物」，而不是编造「可读/不可读」的结论。
func TestStorageInit_S3EmptyBucketSkipsVerification(t *testing.T) {
	server, _ := newFakeS3Server(t, fakeS3Options{
		policyUnsupported: true,
		listXML:           listEmptyXML,
		probeStatus:       http.StatusForbidden,
	})

	out, err := runStorageInit(t, server.URL, nil)
	require.NoError(t, err)

	assert.Contains(t, out, "is empty: nothing to verify anonymous read access")
	assert.NotContains(t, out, "is anonymously readable")
	assert.NotContains(t, out, "NOT anonymously readable")
	assert.NotContains(t, out, "Cannot verify")
}

// TestStorageInit_S3ProbeUsesPublicBase 验证配置了 AWS_URL 时探测打在应用实际产出的基址上：
// PublicURL 形态是 <PublicURL>/<key>（不含桶名段），与 endpoint 形态的 <endpoint>/<bucket>/<key> 不同；
// 两条路径一旦漂移，探测验证的就是一个应用根本不会产出的地址。
func TestStorageInit_S3ProbeUsesPublicBase(t *testing.T) {
	server, getPaths := newFakeS3Server(t, fakeS3Options{
		listXML:     listOneObjectXML,
		probeStatus: http.StatusOK,
	})

	out, err := runStorageInit(t, server.URL, func(s3 *config.S3StorageConfig) {
		s3.PublicURL = server.URL + "/cdn"
	})
	require.NoError(t, err)
	assert.Contains(t, out, "is anonymously readable")

	paths := getPaths()
	require.NotEmpty(t, paths)
	last := paths[len(paths)-1]
	assert.Equal(t, "/cdn/probe/real-object.png", last, "探测应命中 AWS_URL 基址（/cdn/<key>）")
	assert.NotContains(t, last, "/default/", "不得退回 endpoint 推导形态（/<bucket>/<key>）")
}
