// Package response_test 以真实 fiber 引擎验证成功响应契约（api.md 5.1）。
package response_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/platform/response"
)

// run 用真实 Fiber 引擎执行 handler。
func run(handler fiber.Handler) (*http.Response, string) {
	app := fiber.New()
	app.Get("/probe", handler)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		panic(err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(bodyBytes)
}

// TestNewMeta 校验 totalPages 向上取整，且 pageSize 为 0 时不除零。
func TestNewMeta(t *testing.T) {
	assert.Equal(t, response.Meta{Page: 2, PageSize: 20, Total: 41, TotalPages: 3}, response.NewMeta(2, 20, 41))
	assert.Equal(t, 0, response.NewMeta(1, 20, 0).TotalPages)
	assert.Equal(t, 0, response.NewMeta(1, 0, 10).TotalPages)
	assert.Equal(t, 5, response.NewMeta(1, 20, 100).TotalPages)
}

// TestPaginatedSerialisesEmptyAsArray 校验空列表序列化为 [] 而不是 null（api.md 2.3）。
func TestPaginatedSerialisesEmptyAsArray(t *testing.T) {
	resp, body := run(func(c fiber.Ctx) error {
		return response.Paginated[string](c, nil, response.NewMeta(1, 20, 0))
	})

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.JSONEq(t,
		`{"items":[],"meta":{"page":1,"pageSize":20,"total":0,"totalPages":0}}`,
		body,
	)
}

// TestPaginatedKeepsItems 校验非空列表原样输出并保持信封结构。
func TestPaginatedKeepsItems(t *testing.T) {
	resp, body := run(func(c fiber.Ctx) error {
		return response.Paginated(c, []int{1, 2}, response.NewMeta(1, 2, 2))
	})

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	var page response.Page[int]
	require.NoError(t, json.Unmarshal([]byte(body), &page))
	assert.Equal(t, []int{1, 2}, page.Items)
	assert.Equal(t, 1, page.Meta.Page)
}

// TestOKReturnsBareObject 校验单体成功响应直接返回业务对象，不带任何外层包装。
func TestOKReturnsBareObject(t *testing.T) {
	resp, body := run(func(c fiber.Ctx) error {
		return response.OK(c, map[string]string{"id": "u_1"})
	})

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"id":"u_1"}`, body)
	assert.NotContains(t, body, "items")
	assert.NotContains(t, body, "success")
}

// TestCreated 校验创建成功返回 201 与新建对象。
func TestCreated(t *testing.T) {
	resp, body := run(func(c fiber.Ctx) error {
		return response.Created(c, map[string]string{"id": "u_1"})
	})

	assert.Equal(t, fiber.StatusCreated, resp.StatusCode)
	assert.JSONEq(t, `{"id":"u_1"}`, body)
}

// TestNoContent 校验 204 且响应体为 0 字节（api.md 5.1.4）。
func TestNoContent(t *testing.T) {
	resp, body := run(response.NoContent)

	assert.Equal(t, fiber.StatusNoContent, resp.StatusCode)
	assert.Empty(t, body)
}
