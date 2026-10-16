// Package response 实现统一 HTTP 成功响应、Offset 分页信封与流式传输封装。
//
// 分页只提供 Offset 一种形态（api.md 6.1 的契约）：游标分页不在契约内，
// 待真有"深翻页性能不可接受"的端点时再按需引入，避免多出一套无人调用的响应形状。
package response

import (
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
