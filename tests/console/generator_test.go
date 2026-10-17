package console_test

import (
	"path/filepath"
	"strings"
	"testing"

	"bfiber/internal/console/core"
)

func TestBuildTemplateData_Fallback(t *testing.T) {
	opts := core.GeneratorOptions{
		Name:    "demo_item",
		IsAdmin: true,
	}

	data := core.BuildTemplateData(opts)

	if data.PascalName != "DemoItem" {
		t.Fatalf("expected PascalName DemoItem, got %s", data.PascalName)
	}
	if data.PackageName != "admindemoitem" {
		t.Fatalf("expected PackageName admindemoitem, got %s", data.PackageName)
	}
	if data.HasCustomFields {
		t.Fatalf("expected HasCustomFields false for missing schema, got true")
	}
	if data.HasDecimal {
		t.Fatalf("expected HasDecimal false, got true")
	}
	if data.DefaultStatus != "active" {
		t.Fatalf("expected DefaultStatus active, got %s", data.DefaultStatus)
	}
}

func TestBuildTemplateData_ProductSchema(t *testing.T) {
	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("failed to get root dir: %v", err)
	}

	opts := core.GeneratorOptions{
		Name:    "product",
		IsAdmin: true,
		RootDir: rootDir,
	}

	data := core.BuildTemplateData(opts)

	if data.PascalName != "Product" {
		t.Fatalf("expected PascalName Product, got %s", data.PascalName)
	}
	if data.TableName != "product" {
		t.Fatalf("expected TableName product, got %s", data.TableName)
	}
	if data.ChineseName != "商品" {
		t.Fatalf("expected ChineseName 商品, got %s", data.ChineseName)
	}
	if !data.HasCustomFields {
		t.Fatalf("expected HasCustomFields true, got false")
	}
	if !data.HasDecimal {
		t.Fatalf("expected HasDecimal true, got false")
	}
	if !data.IsAttachable {
		t.Fatalf("expected IsAttachable true, got false")
	}
	if !data.HasSKU {
		t.Fatalf("expected HasSKU true, got false")
	}
	if !data.HasSlug {
		t.Fatalf("expected HasSlug true, got false")
	}
	if data.DefaultStatus != "draft" {
		t.Fatalf("expected DefaultStatus draft, got %s", data.DefaultStatus)
	}
	if !strings.Contains(data.StatusEnumStr, "draft") || !strings.Contains(data.StatusEnumStr, "published") {
		t.Fatalf("expected StatusEnumStr containing draft and published, got %s", data.StatusEnumStr)
	}
	if len(data.SearchFields) < 2 {
		t.Fatalf("expected at least 2 search fields (sku, name), got %d", len(data.SearchFields))
	}
	if !strings.Contains(data.SearchSQL, "p.sku ILIKE ?") && !strings.Contains(data.SearchSQL, "p.name ILIKE ?") {
		t.Fatalf("expected SearchSQL containing sku and name, got %s", data.SearchSQL)
	}
}

func TestGenerateCRUD_DryRun(t *testing.T) {
	rootDir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("failed to get root dir: %v", err)
	}

	opts := core.GeneratorOptions{
		Name:    "product",
		IsAdmin: true,
		Force:   true,
		DryRun:  true,
		RootDir: rootDir,
	}

	results, err := core.GenerateCRUD(opts)
	if err != nil {
		t.Fatalf("GenerateCRUD failed: %v", err)
	}

	// product 的 schema 已存在，故生成 5 个 CRUD 文件 + 1 个 factory + 1 个 seeder = 7 个文件
	if len(results) != 7 {
		t.Fatalf("expected 7 generated results, got %d", len(results))
	}

	for _, r := range results {
		if r.Action != core.ActionDryRun {
			t.Errorf("expected action DRY-RUN for %s, got %s", r.Path, r.Action)
		}
	}
}
