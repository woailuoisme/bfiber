package admin_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"bfiber/internal/database"
	"bfiber/internal/database/models"
	admin "bfiber/internal/features/admin/user"
)

// chunkFetcher 把内存切片包装成 UserChunkFetcher，模拟按偏移量分块拉取。
func chunkFetcher(users []models.User) admin.UserChunkFetcher {
	return func(_ context.Context, window database.Window) ([]models.User, error) {
		if window.Offset >= len(users) {
			return nil, nil
		}
		return users[window.Offset:min(window.Offset+window.Limit, len(users))], nil
	}
}

// TestUsersExportStreamContract 校验分块流式导出这一条生产路径：
// 表头、逐列映射与分块推进的最终产物必须是可解码的完整 XLSX。
func TestUsersExportStreamContract(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	var dataset []models.User
	for i := 1; i <= 5; i++ {
		dataset = append(dataset, models.User{
			Base:   database.Base{ID: fmt.Sprintf("usr_%03d", i), CreatedAt: now},
			Name:   fmt.Sprintf("User%d", i),
			Email:  fmt.Sprintf("user%d@example.com", i),
			Role:   models.RoleUser,
			Status: models.StatusActive,
		})
	}

	var buf bytes.Buffer
	require.NoError(t, admin.ExportChunkedStream(t.Context(), &buf, chunkFetcher(dataset)))
	assert.Positive(t, buf.Len())

	// 解码生成的 Excel 文件：列序即 mapUserRow 的契约，逐列断言而非只看行数
	f, err := excelize.OpenReader(&buf)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	rows, err := f.GetRows("Users")
	require.NoError(t, err)
	// 表头 1 行 + 数据 5 行 = 6 行
	require.Len(t, rows, 6)
	assert.Equal(t, []string{"ID", "Name", "Email", "Role", "Status", "Created At"}, rows[0])
	assert.Equal(t, []string{
		"usr_001", "User1", "user1@example.com",
		string(models.RoleUser), string(models.StatusActive), now.Format(time.RFC3339),
	}, rows[1])
	assert.Equal(t, "usr_005", rows[5][0])
	assert.Equal(t, "User5", rows[5][1])
}

func TestUsersImportContract(t *testing.T) {
	t.Parallel()

	imp := admin.NewUsersImport()
	assert.Equal(t, 1, imp.HeadingRow())
	assert.Equal(t, 100, imp.BatchSize())
	assert.Equal(t, 0, imp.ImportedCount())

	// 测试数据列数不足
	_, err := imp.ToModel([]string{"OnlyName"})
	require.Error(t, err)

	// 测试空数据校验失败
	_, err = imp.ToModel([]string{"", "test@example.com"})
	require.Error(t, err)

	// 测试正常行解析映射
	modelAny, err := imp.ToModel([]string{"Bob Builder", "bob@example.com"})
	require.NoError(t, err)

	u, ok := modelAny.(*models.User)
	require.True(t, ok)
	assert.Equal(t, "Bob Builder", u.Name)
	assert.Equal(t, "bob@example.com", u.Email)
	assert.Equal(t, models.RoleUser, u.Role)
	assert.Equal(t, 1, imp.ImportedCount())
}
