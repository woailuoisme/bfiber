package console_test

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/console/core"
	"bfiber/internal/console/stubs"
)

func TestParseTableFromSchemas_Product(t *testing.T) {
	schemasDir := filepath.Join("..", "..", "internal", "database", "schemas")
	table, err := core.ParseTableFromSchemas(schemasDir, "product")
	require.NoError(t, err)
	require.True(t, table.Found, "product table should be found in schemas")

	assert.Equal(t, "product", table.TableName)
	assert.True(t, table.HasBase)
	assert.True(t, table.HasSoftDeletes)
	assert.True(t, table.IsAttachable)
	assert.True(t, table.HasDecimal)

	// 检查字段映射
	fieldMap := make(map[string]core.ParsedField)
	for _, f := range table.CustomFields {
		fieldMap[f.ColName] = f
	}

	// 检查 SKU
	skuField, ok := fieldMap["sku"]
	require.True(t, ok)
	assert.Equal(t, "SKU", skuField.PascalName)
	assert.Equal(t, "string", skuField.GoType)
	assert.False(t, skuField.IsNullable)

	// 检查 Price (numeric -> decimal.Decimal)
	priceField, ok := fieldMap["price"]
	require.True(t, ok)
	assert.Equal(t, "Price", priceField.PascalName)
	assert.Equal(t, "decimal.Decimal", priceField.GoType)
	assert.Equal(t, `bun:"price,notnull,default:'0.00'"`, priceField.BunTag)

	// 检查 Attributes (jsonb -> map[string]any)
	attrField, ok := fieldMap["attributes"]
	require.True(t, ok)
	assert.Equal(t, "Attributes", attrField.PascalName)
	assert.Equal(t, "map[string]any", attrField.GoType)
	assert.Equal(t, `bun:"attributes,type:jsonb,notnull,default:'{}'"`, attrField.BunTag)

	// 检查 Status CHECK 枚举
	statusField, ok := fieldMap["status"]
	require.True(t, ok)
	assert.Equal(t, []string{"draft", "published", "archived"}, statusField.EnumValues)
	assert.Equal(t, []string{"draft", "published", "archived"}, table.StatusEnumVals)
	assert.Equal(t, `bun:"status,notnull,default:'draft'"`, statusField.BunTag)
}

func TestParseTableFromSchemas_NotFound(t *testing.T) {
	schemasDir := filepath.Join("..", "..", "internal", "database", "schemas")
	table, err := core.ParseTableFromSchemas(schemasDir, "non_existent_table")
	require.NoError(t, err)
	assert.False(t, table.Found)
}

func TestParseTable_AdvancedPostgresTypes(t *testing.T) {
	sqlDDL := `
CREATE TABLE IF NOT EXISTS enterprise_ai_asset (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    path ltree NOT NULL,
    location geometry(Point, 4326) NOT NULL,
    service_area geometry(Polygon, 4326) NOT NULL,
    embedding vector(1536) NOT NULL,
    tags text[] NOT NULL,
    score_list integer[] NOT NULL,
    raw_payload bytea NOT NULL,
    client_ip inet NOT NULL,
    ratio_score double precision NOT NULL,
    sensor_temp real NOT NULL,
    event_date date NOT NULL
);
`
	candidates := map[string]bool{"enterprise_ai_asset": true}
	table, found := core.ParseTableFromSQL(sqlDDL, candidates)
	require.True(t, found)
	require.NotNil(t, table)

	// 校验表级特征检测
	assert.True(t, table.HasBase)
	assert.True(t, table.HasSoftDeletes)
	assert.True(t, table.HasVector)
	assert.True(t, table.HasGeo)
	assert.True(t, table.HasLTree)
	assert.True(t, table.HasArray)

	assert.Equal(t, "embedding", table.VectorColName)
	assert.Equal(t, "Embedding", table.VectorFieldName)
	assert.Equal(t, "location", table.GeoColName)
	assert.Equal(t, "Location", table.GeoFieldName)
	assert.Equal(t, "path", table.LTreeColName)
	assert.Equal(t, "Path", table.LTreeFieldName)

	fieldMap := make(map[string]core.ParsedField)
	for _, f := range table.CustomFields {
		fieldMap[f.ColName] = f
	}

	// 1. ltree 校验
	fPath := fieldMap["path"]
	assert.Equal(t, "nestedset.Path", fPath.GoType)
	assert.Equal(t, "string", fPath.DTOGoType)
	assert.Equal(t, "*string", fPath.DTOGoTypePtr)
	assert.Equal(t, `bun:"path,type:ltree,notnull"`, fPath.BunTag)

	// 2. geometry(Point, 4326) 校验
	fLoc := fieldMap["location"]
	assert.Equal(t, "geo.Point", fLoc.GoType)
	assert.Equal(t, "*geo.Point", fLoc.DTOGoType)
	assert.Equal(t, "*geo.Point", fLoc.DTOGoTypePtr)
	assert.Equal(t, `bun:"location,type:geometry(point, 4326),notnull"`, fLoc.BunTag)

	// 3. geometry(Polygon, 4326) 校验
	fArea := fieldMap["service_area"]
	assert.Equal(t, "geo.Polygon", fArea.GoType)
	assert.Equal(t, "*geo.Polygon", fArea.DTOGoType)
	assert.Equal(t, "*geo.Polygon", fArea.DTOGoTypePtr)
	assert.Equal(t, `bun:"service_area,type:geometry(polygon, 4326),notnull"`, fArea.BunTag)

	// 4. vector(1536) 校验
	fVec := fieldMap["embedding"]
	assert.Equal(t, "vector.Vector", fVec.GoType)
	assert.Equal(t, "[]float32", fVec.DTOGoType)
	assert.Equal(t, "[]float32", fVec.DTOGoTypePtr)
	assert.Equal(t, `bun:"embedding,type:vector(1536),notnull"`, fVec.BunTag)

	// 5. text[] 数组校验
	fTags := fieldMap["tags"]
	assert.Equal(t, "[]string", fTags.GoType)
	assert.Equal(t, "[]string", fTags.DTOGoType)
	assert.Equal(t, "[]string", fTags.DTOGoTypePtr)
	assert.Equal(t, `bun:"tags,array,notnull"`, fTags.BunTag)

	// 6. integer[] 数组校验
	fScores := fieldMap["score_list"]
	assert.Equal(t, "[]int", fScores.GoType)
	assert.Equal(t, "[]int", fScores.DTOGoType)
	assert.Equal(t, `bun:"score_list,array,notnull"`, fScores.BunTag)

	// 7. bytea 二进制校验
	fRaw := fieldMap["raw_payload"]
	assert.Equal(t, "[]byte", fRaw.GoType)
	assert.Equal(t, "[]byte", fRaw.DTOGoType)
	assert.Equal(t, `bun:"raw_payload,type:bytea,notnull"`, fRaw.BunTag)

	// 8. inet 网络类型校验
	fIP := fieldMap["client_ip"]
	assert.Equal(t, "string", fIP.GoType)
	assert.Equal(t, "string", fIP.DTOGoType)
	assert.Equal(t, `bun:"client_ip,type:inet,notnull"`, fIP.BunTag)

	// 9. double precision 校验
	fDouble := fieldMap["ratio_score"]
	assert.Equal(t, "float64", fDouble.GoType)
	assert.Equal(t, "float64", fDouble.DTOGoType)
	assert.Equal(t, `bun:"ratio_score,notnull"`, fDouble.BunTag)

	// 10. real 校验
	fReal := fieldMap["sensor_temp"]
	assert.Equal(t, "float32", fReal.GoType)
	assert.Equal(t, "float32", fReal.DTOGoType)
	assert.Equal(t, `bun:"sensor_temp,notnull"`, fReal.BunTag)

	// 11. date 校验
	fDate := fieldMap["event_date"]
	assert.Equal(t, "time.Time", fDate.GoType)
	assert.Equal(t, "time.Time", fDate.DTOGoType)
	assert.Equal(t, `bun:"event_date,type:date,notnull"`, fDate.BunTag)
}

func TestGenerateCRUD_WithAdvancedTypes_SyntaxValid(t *testing.T) {
	sqlDDL := `
CREATE TABLE IF NOT EXISTS spatial_item (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    sku VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    path ltree NOT NULL,
    location geometry(Point, 4326) NOT NULL,
    embedding vector(1536) NOT NULL,
    tags text[] NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived'))
);
`
	candidates := map[string]bool{"spatial_item": true}
	table, found := core.ParseTableFromSQL(sqlDDL, candidates)
	require.True(t, found)

	opts := core.GeneratorOptions{
		Name:    "spatial_item",
		IsAdmin: true,
	}

	data := core.BuildTemplateData(opts)
	data.TableName = table.TableName
	data.HasCustomFields = len(table.CustomFields) > 0
	data.HasBase = table.HasBase
	data.HasSoftDeletes = table.HasSoftDeletes
	data.HasVector = table.HasVector
	data.HasGeo = table.HasGeo
	data.HasLTree = table.HasLTree
	data.HasArray = table.HasArray
	data.VectorColName = table.VectorColName
	data.VectorFieldName = table.VectorFieldName
	data.GeoColName = table.GeoColName
	data.GeoFieldName = table.GeoFieldName
	data.LTreeColName = table.LTreeColName
	data.LTreeFieldName = table.LTreeFieldName
	data.CustomFields = table.CustomFields
	data.StatusEnumVals = table.StatusEnumVals
	data.DefaultStatus = table.StatusEnumVals[0]
	data.StatusEnumStr = "draft published archived"

	for _, f := range table.CustomFields {
		if f.ColName == "sku" {
			data.HasSKU = true
		}
		if f.IsNullable {
			data.HasNullableField = true
		}
		data.FakeAssigns = append(data.FakeAssigns, core.FakeAssign{
			FieldName: f.PascalName,
			Expr:      f.FakeExpr(data.PascalName),
		})
	}
	for _, val := range data.StatusEnumVals {
		methodName := core.ToPascalCase(val)
		data.StatusModifiers = append(data.StatusModifiers, core.StatusModifier{
			MethodName: methodName,
			ConstName:  fmt.Sprintf("%sStatus%s", data.PascalName, methodName),
			EnumVal:    val,
		})
	}

	plan := core.NewCRUDPlan(opts).WithData(data)

	files, err := plan.Files()
	require.NoError(t, err)
	require.Len(t, files, 8)

	for _, f := range files {
		tmplContent, err := fs.ReadFile(stubs.FS, f.Template)
		require.NoError(t, err)

		tmpl, err := template.New(f.Template).Parse(string(tmplContent))
		require.NoError(t, err)

		var buf bytes.Buffer
		err = tmpl.Execute(&buf, f.Data)
		require.NoError(t, err, "failed to render template %s", f.Template)

		if strings.HasSuffix(f.RelPath, ".go") {
			// 验证生成的 Go 代码经标准 format.Source 格式化后完全合法
			formatted, err := format.Source(buf.Bytes())
			require.NoError(t, err, "generated code from %s has invalid Go syntax:\n%s", f.Template, buf.String())
			assert.NotEmpty(t, formatted)
		} else {
			assert.Contains(t, buf.String(), "CREATE TABLE")
		}
	}
}
