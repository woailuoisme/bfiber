// Package response 实现统一 HTTP 成功响应、Offset 分页信封与流式传输封装。
//
// 分页只提供 Offset 一种形态（api.md 6.1 的契约）：游标分页不在契约内，
// 待真有"深翻页性能不可接受"的端点时再按需引入，避免多出一套无人调用的响应形状。
package response

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/gofiber/fiber/v3"
)

// Meta 是传统偏移分页列表响应的元信息。
type Meta struct {
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

// Page 是传统偏移分页列表的响应信封；Items 恒为数组，不会序列化成 null。
type Page[T any] struct {
	Items []T  `json:"items"`
	Meta  Meta `json:"meta"`
}

// NewMeta 计算传统分页元信息，totalPages 向上取整。
func NewMeta(page, pageSize, total int) Meta {
	var totalPages int
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return Meta{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// OK 返回 200 与业务对象本身，不添加任何外层包装。
func OK[T any](c fiber.Ctx, data T) error {
	return c.Status(fiber.StatusOK).JSON(data)
}

// Created 返回 201 与新建对象。
func Created[T any](c fiber.Ctx, data T) error {
	return c.Status(fiber.StatusCreated).JSON(data)
}

// NoContent 返回 204，响应体为 0 字节。
func NoContent(c fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// Paginated 返回传统偏移分页信封，空列表同样保持结构完整。
func Paginated[T any](c fiber.Ctx, items []T, meta Meta) error {
	// 为保持 RESTful JSON 契约一致性，将 nil 切片强制初始化为空切片 []T{}，避免在 JSON 序列化中产出 null。
	if items == nil {
		items = []T{}
	}
	return c.Status(fiber.StatusOK).JSON(Page[T]{Items: items, Meta: meta})
}

// Collection 返回无信封的数组集合，确保 nil 切片恒定序列化为 [] 而非 null。
func Collection[T any](c fiber.Ctx, items []T) error {
	// 为保持 RESTful JSON 契约一致性，将 nil 切片强制初始化为空切片 []T{}，避免在 JSON 序列化中产出 null。
	if items == nil {
		items = []T{}
	}
	return c.Status(fiber.StatusOK).JSON(items)
}

// Attachment 将已知长度的流作为下载附件写回客户端，委托 Fiber v3 官方 Attachment 处理 RFC 6266 / RFC 8187 文件名。
// 长度未知的流式产出（如边写边生成的报表）请直接设置响应头后写 c.Response().BodyWriter()。
func Attachment(c fiber.Ctx, filename string, r io.Reader, size int64, contentType ...string) error {
	c.Attachment(filename)
	if len(contentType) > 0 && contentType[0] != "" {
		c.Type(contentType[0])
	}
	c.Status(fiber.StatusOK)
	if size > 0 {
		return c.SendStream(r, int(size))
	}
	return c.SendStream(r)
}

// Inline 将已知长度的流作为内联展示（如 PDF/图片）写回客户端。
func Inline(c fiber.Ctx, filename string, r io.Reader, size int64, contentType ...string) error {
	fname := filepath.Base(filename)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("inline; filename=%q", fname))
	if len(contentType) > 0 && contentType[0] != "" {
		c.Type(contentType[0])
	} else {
		c.Type(filepath.Ext(fname))
	}
	c.Status(fiber.StatusOK)
	if size > 0 {
		return c.SendStream(r, int(size))
	}
	return c.SendStream(r)
}
