package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ParsedField 描述从 SQL DDL 中提取出的单个字段规范及其在 Go/Bun/DTO 中的对应映射。
type ParsedField struct {
	ColName      string   // SQL 原始列名（如 compare_at_price）
	PascalName   string   // Go 结构体字段名（如 CompareAtPrice）
	CamelName    string   // JSON/Swagger 字段名（如 compareAtPrice）
	SQLType      string   // SQL 原始类型（如 numeric(12, 2)）
	GoType       string   // Go 实体类型（如 decimal.Decimal, string, int）
	GoTypePtr    string   // Go 指针类型，用于更新 DTO（如 *decimal.Decimal, *string）
	DTOGoType    string   // DTO 传输层类型（如 []float32 用于 vector, string 用于 ltree）
	DTOGoTypePtr string   // DTO 指针类型，用于更新请求（如 []float32, *string）
	IsNullable   bool     // 是否允许为空
	IsPrimaryKey bool     // 是否为主键
	IsBaseField  bool     // 是否属于 database.Base（id, created_at, updated_at）
	IsSoftDelete bool     // 是否属于 database.SoftDeletes（deleted_at）
	DefaultVal   string   // SQL 默认值表达式（如 0.00, 'draft'）
	Comment      string   // 行内注释提取出的字段业务描述
	EnumValues   []string // CHECK 约束推导出的枚举值列表（如 ["draft", "published", "archived"]）
	BunTag       string   // Bun ORM 标签（如 `bun:"price,notnull,default:0.00"`）
	JSONTag      string   // JSON 序列化标签（如 `json:"price"`）
	ValidateTag  string   // Validator 校验标签（如 `validate:"required,gte=0"`）
	ExampleValue string   // Swagger 文档示例数据（如 "99.90"）
}

const (
	goTypeString     = "string"
	goTypeDecimal    = "decimal.Decimal"
	goTypeInt        = "int"
	goTypeInt64      = "int64"
	goTypeFloat32    = "float32"
	goTypeFloat64    = "float64"
	goTypeBool       = "bool"
	goTypeVector     = "vector.Vector"
	goTypeGeoPoint   = "geo.Point"
	goTypeGeoPolygon = "geo.Polygon"
	goTypeLTree      = "nestedset.Path"
	goTypeByteSlice  = "[]byte"
	colSKU           = "sku"
	colImage         = "image"
)

// IsComplexType 标识是否为复合引用类型（Map、Slice、Vector、Geo），此类字段在局部更新中不需要解引用且在 Swagger 资源中不输出标量 example。
func (f ParsedField) IsComplexType() bool {
	return f.IsMap() || f.IsSlice() || f.IsVector() || f.IsGeo()
}

// IsMap 标识字段是否为 Map 键值对类型。
func (f ParsedField) IsMap() bool {
	return strings.HasPrefix(f.GoType, "map[")
}

// IsSlice 标识字段是否为 Slice 切片类型。
func (f ParsedField) IsSlice() bool {
	return strings.HasPrefix(f.GoType, "[]")
}

// IsString 标识字段是否为 string 字符串类型。
func (f ParsedField) IsString() bool {
	return f.GoType == goTypeString
}

// IsDecimal 标识字段是否为 decimal.Decimal 高精度货币类型。
func (f ParsedField) IsDecimal() bool {
	return f.GoType == goTypeDecimal
}

// IsVector 标识字段是否为 pgvector 特征向量类型。
func (f ParsedField) IsVector() bool {
	return f.GoType == goTypeVector
}

// IsGeo 标识字段是否为 PostGIS 空间几何类型（Point 或 Polygon）。
func (f ParsedField) IsGeo() bool {
	return f.GoType == goTypeGeoPoint || f.GoType == goTypeGeoPolygon
}

// IsLTree 标识字段是否为 PostgreSQL ltree 点分路径类型。
func (f ParsedField) IsLTree() bool {
	return f.GoType == goTypeLTree
}

// IsArray 标识字段是否为 PostgreSQL 原生数组类型。
func (f ParsedField) IsArray() bool {
	return strings.HasPrefix(f.GoType, "[]") && f.GoType != goTypeByteSlice
}

// ParsedTable 描述从 SQL DDL 中解析出的完整表模型。
type ParsedTable struct {
	TableName       string        // SQL 真实表名（如 product 或 products）
	Found           bool          // 是否成功检索到表定义
	HasBase         bool          // 是否包含标准 Base 字段（id, created_at, updated_at）
	HasSoftDeletes  bool          // 是否包含 SoftDeletes 字段（deleted_at）
	IsAttachable    bool          // 是否推荐挂载 media.Attachable 契约
	HasDecimal      bool          // 是否需要引入 shopspring/decimal
	HasTime         bool          // 是否在业务字段中包含原生 time.Time
	HasVector       bool          // 是否包含 pgvector 向量字段
	HasGeo          bool          // 是否包含 PostGIS 空间几何字段
	HasLTree        bool          // 是否包含 ltree 层级路径字段
	HasArray        bool          // 是否包含 PostgreSQL 原生数组字段
	VectorColName   string        // 第一个向量列名（如 embedding）
	VectorFieldName string        // 对应的 Go 字段名（如 Embedding）
	GeoColName      string        // 第一个地理空间列名（如 location）
	GeoFieldName    string        // 对应的 Go 字段名（如 Location）
	LTreeColName    string        // 第一个 LTree 列名（如 path）
	LTreeFieldName  string        // 对应的 Go 字段名（如 Path）
	Fields          []ParsedField // 全部解析字段
	CustomFields    []ParsedField // 剔除 Base 与 SoftDeletes 后的业务自定义字段
	StatusEnumVals  []string      // 状态机枚举值列表
}

var (
	createTableRegexp = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["']?([a-zA-Z0-9_]+)["']?\s*\(([\s\S]*?)\);`)
	checkConstraintRe = regexp.MustCompile(`(?i)CHECK\s*\(\s*([a-zA-Z0-9_]+)\s+IN\s*\(([^)]+)\)\)`)
)

// ParseTableFromSchemas 在指定的 schemas 目录中遍历搜索匹配表名的 DDL 定义并解析。
func ParseTableFromSchemas(schemasDir, tableName string) (*ParsedTable, error) {
	if schemasDir == "" {
		schemasDir = filepath.Join("internal", "database", "schemas")
	}

	entries, err := os.ReadDir(schemasDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &ParsedTable{Found: false}, nil
		}
		return nil, err
	}

	candidateNames := map[string]bool{
		strings.ToLower(tableName):              true,
		strings.ToLower(Singularize(tableName)): true,
		strings.ToLower(Pluralize(tableName)):   true,
		strings.ToLower(ToSnakeCase(tableName)): true,
		strings.ToLower(ToKebabCase(tableName)): true,
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		filePath := filepath.Join(schemasDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		parsed, found := ParseTableFromSQL(string(content), candidateNames)
		if found {
			return parsed, nil
		}
	}

	return &ParsedTable{Found: false}, nil
}

// ParseTableFromSQL 从一段 SQL 文本中匹配候选表名并解析其字段定义。
func ParseTableFromSQL(sqlContent string, candidateNames map[string]bool) (*ParsedTable, bool) {
	matches := createTableRegexp.FindAllStringSubmatch(sqlContent, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		rawTableName := strings.ToLower(match[1])
		if !candidateNames[rawTableName] {
			continue
		}

		tableBody := match[2]
		parsed := parseTableBody(rawTableName, tableBody)
		parsed.Found = true
		return parsed, true
	}

	return nil, false
}

// parseTableBody 逐行解析 CREATE TABLE 内部的列定义与约束。
func parseTableBody(tableName, body string) *ParsedTable {
	table := &ParsedTable{
		TableName: tableName,
	}

	columnEnums := extractColumnEnums(body)
	table.Fields = parseTableFields(body, columnEnums)
	classifyTableProperties(table)

	return table
}

// extractColumnEnums 提取全局 CHECK 约束中的枚举值列表。
func extractColumnEnums(body string) map[string][]string {
	columnEnums := make(map[string][]string)
	checkMatches := checkConstraintRe.FindAllStringSubmatch(body, -1)
	for _, cm := range checkMatches {
		if len(cm) < 3 {
			continue
		}
		col := strings.ToLower(strings.TrimSpace(cm[1]))
		rawVals := strings.Split(cm[2], ",")
		var vals []string
		for _, v := range rawVals {
			trimmed := strings.Trim(strings.TrimSpace(v), "'\" ")
			if trimmed != "" {
				vals = append(vals, trimmed)
			}
		}
		if len(vals) > 0 {
			columnEnums[col] = vals
		}
	}
	return columnEnums
}

// parseTableFields 逐行解析列定义。
func parseTableFields(body string, columnEnums map[string][]string) []ParsedField {
	var fields []ParsedField
	for rawLine := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}

		lineWithoutComma := strings.TrimSuffix(line, ",")
		upperLine := strings.ToUpper(lineWithoutComma)
		if isTableConstraint(upperLine) {
			continue
		}

		if field, ok := parseColumnLine(lineWithoutComma, columnEnums); ok {
			fields = append(fields, field)
		}
	}
	return fields
}

// isTableConstraint 过滤表级主键、外键或约束。
func isTableConstraint(upperLine string) bool {
	return strings.HasPrefix(upperLine, "PRIMARY KEY") ||
		strings.HasPrefix(upperLine, "CONSTRAINT") ||
		strings.HasPrefix(upperLine, "FOREIGN KEY") ||
		strings.HasPrefix(upperLine, "CHECK") ||
		strings.HasPrefix(upperLine, "UNIQUE")
}

// classifyTableProperties 识别标准 Base、SoftDeletes、Attachable 及自定义业务字段。
func classifyTableProperties(table *ParsedTable) {
	var hasID, hasCreatedAt, hasUpdatedAt bool

	for i := range table.Fields {
		f := &table.Fields[i]
		switch f.ColName {
		case "id":
			hasID = true
			f.IsBaseField = true
		case "created_at":
			hasCreatedAt = true
			f.IsBaseField = true
		case "updated_at":
			hasUpdatedAt = true
			f.IsBaseField = true
		case "deleted_at":
			table.HasSoftDeletes = true
			f.IsSoftDelete = true
		case colImage, "images", "media":
			table.IsAttachable = true
		}
		processCustomField(f, table)
	}

	table.HasBase = hasID && hasCreatedAt && hasUpdatedAt

	switch Singularize(table.TableName) {
	case "product", "article", "post", "good":
		table.IsAttachable = true
	}
}

// processCustomField 归类业务自定义字段并统计类型特征。
func processCustomField(f *ParsedField, table *ParsedTable) {
	if f.ColName == "status" && len(f.EnumValues) > 0 {
		table.StatusEnumVals = f.EnumValues
	}

	if f.IsBaseField || f.IsSoftDelete {
		return
	}

	table.CustomFields = append(table.CustomFields, *f)
	collectFieldTypeFeatures(f, table)
}

// collectFieldTypeFeatures 提取业务字段的高级 PostgreSQL 类型特征并记录到表中。
func collectFieldTypeFeatures(f *ParsedField, table *ParsedTable) {
	if f.GoType == goTypeDecimal {
		table.HasDecimal = true
	}
	if f.GoType == "time.Time" {
		table.HasTime = true
	}
	if f.IsVector() {
		table.HasVector = true
		if table.VectorColName == "" {
			table.VectorColName = f.ColName
			table.VectorFieldName = f.PascalName
		}
	}
	if f.IsGeo() {
		table.HasGeo = true
		if table.GeoColName == "" {
			table.GeoColName = f.ColName
			table.GeoFieldName = f.PascalName
		}
	}
	if f.IsLTree() {
		table.HasLTree = true
		if table.LTreeColName == "" {
			table.LTreeColName = f.ColName
			table.LTreeFieldName = f.PascalName
		}
	}
	if f.IsArray() {
		table.HasArray = true
	}
}

const maxParenthesisTokens = 16

// balanceParentheses 累加解析括号平衡的词元序列（支持 numeric(12, 2), geometry(Point, 4326), vector(1536) 等）。
func balanceParentheses(acc string, remaining []string) (string, int) {
	consumed := 0
	for strings.Count(acc, "(") > strings.Count(acc, ")") && consumed < len(remaining) && consumed < maxParenthesisTokens {
		acc += " " + remaining[consumed]
		consumed++
	}
	return acc, consumed
}

// extractSQLTypePart 从词元切片中智能提取完整的 SQL 类型（处理括号参数如 geometry(Point, 4326) 及多词类型如 double precision）。
func extractSQLTypePart(parts []string) (string, int) {
	if len(parts) == 0 {
		return "", 0
	}
	p0 := parts[0]
	lower0 := strings.ToLower(p0)

	// 多词关键字预先匹配
	if lower0 == "double" && len(parts) > 1 && strings.EqualFold(parts[1], "precision") {
		return "double precision", 2
	}
	if lower0 == "character" && len(parts) > 1 && strings.EqualFold(parts[1], "varying") {
		if len(parts) > 2 && strings.HasPrefix(parts[2], "(") {
			acc, consumed := balanceParentheses("character varying "+parts[2], parts[3:])
			return acc, 3 + consumed
		}
		return "character varying", 2
	}

	// 括号累加解析（支持 numeric(12, 2), geometry(Point, 4326), vector(1536) 等）
	acc, consumed := balanceParentheses(p0, parts[1:])
	return acc, 1 + consumed
}

// parseColumnLine 解析单行列定义。
func parseColumnLine(line string, columnEnums map[string][]string) (ParsedField, bool) {
	// 提取行内注释
	comment := ""
	if idx := strings.Index(line, "--"); idx != -1 {
		comment = strings.TrimSpace(line[idx+2:])
		line = strings.TrimSpace(line[:idx])
	}
	line = strings.TrimSuffix(strings.TrimSpace(line), ",")

	if line == "" {
		return ParsedField{}, false
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ParsedField{}, false
	}

	colName := strings.Trim(parts[0], `"`)
	sqlTypePart, consumed := extractSQLTypePart(parts[1:])
	restIdx := 1 + consumed

	originalRest := ""
	if restIdx < len(parts) {
		originalRest = strings.Join(parts[restIdx:], " ")
	}
	upperRest := strings.ToUpper(originalRest)
	isNullable := !strings.Contains(upperRest, "NOT NULL")
	isPrimaryKey := strings.Contains(upperRest, "PRIMARY KEY")

	// 提取默认值（保留原始大小写，并剥离类型转换后缀）
	defaultVal := ""
	if dIdx := strings.Index(upperRest, "DEFAULT "); dIdx != -1 {
		defSub := strings.TrimSpace(originalRest[dIdx+8:])
		defVal, _, _ := strings.Cut(defSub, " ")
		// 剥离 PostgreSQL 类型转换如 'draft'::text 或 '{}'::jsonb
		if castIdx := strings.Index(defVal, "::"); castIdx != -1 {
			defVal = defVal[:castIdx]
		}
		defaultVal = strings.Trim(defVal, "'\" ")
	}

	pascalName := formatColumnPascalName(colName)
	camelName := ToCamelCase(colName)
	goType, dtoGoType, dtoGoTypePtr, goTypePtr := resolveTypeMapping(sqlTypePart)

	// 枚举值
	var enumVals []string
	if ev, ok := columnEnums[strings.ToLower(colName)]; ok {
		enumVals = ev
	}

	// 构建标签
	bunTag := buildBunTag(colName, sqlTypePart, isPrimaryKey, isNullable, defaultVal)
	jsonTag := fmt.Sprintf(`json:"%s"`, camelName)
	validateTag := buildValidateTag(colName, goType, isNullable, enumVals)
	exampleVal := buildExampleValue(colName, goType, defaultVal, enumVals)

	return ParsedField{
		ColName:      colName,
		PascalName:   pascalName,
		CamelName:    camelName,
		SQLType:      sqlTypePart,
		GoType:       goType,
		GoTypePtr:    goTypePtr,
		DTOGoType:    dtoGoType,
		DTOGoTypePtr: dtoGoTypePtr,
		IsNullable:   isNullable,
		IsPrimaryKey: isPrimaryKey,
		DefaultVal:   defaultVal,
		Comment:      comment,
		EnumValues:   enumVals,
		BunTag:       bunTag,
		JSONTag:      jsonTag,
		ValidateTag:  validateTag,
		ExampleValue: exampleVal,
	}, true
}

// formatColumnPascalName 将数据库列名格式化为符合 Go 规范的 PascalCase（保留通用缩写）。
func formatColumnPascalName(col string) string {
	lower := strings.ToLower(col)
	switch lower {
	case "id":
		return "ID"
	case colSKU:
		return "SKU"
	case "url":
		return "URL"
	case "ip":
		return "IP"
	case "uuid":
		return "UUID"
	}

	// 包含特定后缀或前缀的特殊处理
	if strings.HasSuffix(lower, "_id") {
		prefix := lower[:len(lower)-3]
		return ToPascalCase(prefix) + "ID"
	}
	if strings.HasSuffix(lower, "_sku") {
		prefix := lower[:len(lower)-4]
		return ToPascalCase(prefix) + "SKU"
	}
	if strings.HasSuffix(lower, "_url") {
		prefix := lower[:len(lower)-4]
		return ToPascalCase(prefix) + "URL"
	}

	return ToPascalCase(col)
}

// resolveTypeMapping 将 SQL 类型解析为 GoType，并派生出 DTO 类型和指针。
func resolveTypeMapping(sqlType string) (goType, dtoGoType, dtoGoTypePtr, goTypePtr string) {
	st := strings.ToLower(strings.TrimSpace(sqlType))
	if arrType, ok := resolveArrayType(st); ok {
		goType = arrType
	} else if advType, ok := resolveAdvancedPGType(st); ok {
		goType = advType
	} else {
		goType = resolveStandardPGType(st)
	}

	dtoGoType, dtoGoTypePtr, goTypePtr = deriveDTOTypes(goType)
	return
}

// resolveArrayType 解析 PostgreSQL 原生一维数组类型。
func resolveArrayType(st string) (string, bool) {
	if !strings.HasSuffix(st, "[]") {
		return "", false
	}
	base := strings.TrimSuffix(st, "[]")
	switch {
	case strings.HasPrefix(base, "int8"), base == "bigint":
		return "[]int64", true
	case strings.HasPrefix(base, "int4"), strings.HasPrefix(base, "int2"), base == "int", base == "integer", base == "smallint":
		return "[]int", true
	case strings.HasPrefix(base, "bool"):
		return "[]bool", true
	default:
		// text[], varchar[], uuid[] 等一律映射为 []string
		return "[]string", true
	}
}

// resolveAdvancedPGType 解析 PostgreSQL 高级扩展类型（ltree, postgis, pgvector, network, binary）。
func resolveAdvancedPGType(st string) (string, bool) {
	switch {
	case st == "ltree":
		return goTypeLTree, true
	case strings.HasPrefix(st, "vector"):
		return goTypeVector, true
	case strings.Contains(st, "polygon"):
		return goTypeGeoPolygon, true
	case strings.HasPrefix(st, "geometry"), strings.HasPrefix(st, "geography"), st == "point":
		return goTypeGeoPoint, true
	case st == "bytea":
		return goTypeByteSlice, true
	case st == "inet" || st == "cidr" || st == "macaddr":
		return goTypeString, true
	case st == "real" || st == "float4":
		return goTypeFloat32, true
	case st == "double precision" || st == "float8":
		return goTypeFloat64, true
	default:
		return "", false
	}
}

// resolveStandardPGType 解析标准 SQL / PostgreSQL 内置基础类型。
func resolveStandardPGType(st string) string {
	switch {
	case strings.HasPrefix(st, "numeric"), strings.HasPrefix(st, "decimal"):
		return goTypeDecimal
	case strings.HasPrefix(st, "int8"), st == "bigint":
		return goTypeInt64
	case strings.HasPrefix(st, "int4"), strings.HasPrefix(st, "int2"), st == "integer", st == goTypeInt, st == "smallint":
		return goTypeInt
	case strings.HasPrefix(st, "bool"):
		return goTypeBool
	case strings.HasPrefix(st, "timestamptz"), strings.HasPrefix(st, "timestamp"), st == "date":
		return "time.Time"
	case strings.HasPrefix(st, "jsonb"), strings.HasPrefix(st, "json"):
		return "map[string]any"
	default:
		return goTypeString
	}
}

// deriveDTOTypes 根据 GoType 派生 DTO 传输层类型与指针表现形式。
func deriveDTOTypes(goType string) (dtoType, dtoTypePtr, goTypePtr string) {
	switch goType {
	case goTypeVector:
		dtoType = "[]float32"
		dtoTypePtr = "[]float32"
		goTypePtr = goTypeVector
	case goTypeLTree:
		dtoType = goTypeString
		dtoTypePtr = "*" + goTypeString
		goTypePtr = "*" + goTypeLTree
	case goTypeGeoPoint:
		dtoType = "*geo.Point"
		dtoTypePtr = "*geo.Point"
		goTypePtr = "*geo.Point"
	case goTypeGeoPolygon:
		dtoType = "*geo.Polygon"
		dtoTypePtr = "*geo.Polygon"
		goTypePtr = "*geo.Polygon"
	case goTypeByteSlice:
		dtoType = goTypeByteSlice
		dtoTypePtr = goTypeByteSlice
		goTypePtr = goTypeByteSlice
	default:
		dtoType = goType
		if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") {
			dtoTypePtr = goType
			goTypePtr = goType
		} else {
			dtoTypePtr = "*" + goType
			goTypePtr = "*" + goType
		}
	}
	return
}

// resolveBunTypeTag 提取 Bun ORM 所需的类型修饰符（如 array, type:ltree, type:geometry 等）。
func resolveBunTypeTag(st string) string {
	switch {
	case strings.HasSuffix(st, "[]"):
		return "array"
	case st == "ltree":
		return "type:ltree"
	case strings.HasPrefix(st, "vector"):
		return "type:" + st
	case strings.HasPrefix(st, "geometry") || strings.HasPrefix(st, "geography"):
		return "type:" + st
	case st == "bytea":
		return "type:bytea"
	case st == "inet" || st == "cidr" || st == "macaddr":
		return "type:" + st
	case st == "date":
		return "type:date"
	case strings.HasPrefix(st, "jsonb"):
		return "type:jsonb"
	case strings.HasPrefix(st, "json"):
		return "type:json"
	default:
		return ""
	}
}

// buildBunTag 组装 Bun ORM 标签。
func buildBunTag(colName, sqlType string, isPK, isNullable bool, defaultVal string) string {
	parts := []string{colName}
	if isPK {
		parts = append(parts, "pk")
	}

	st := strings.ToLower(strings.TrimSpace(sqlType))
	if typeTag := resolveBunTypeTag(st); typeTag != "" {
		parts = append(parts, typeTag)
	}

	if !isNullable && !isPK {
		parts = append(parts, "notnull")
	}

	if defaultVal != "" && !strings.Contains(defaultVal, "(") {
		cleanDef := strings.Trim(defaultVal, "'\" ")
		if cleanDef != "" {
			parts = append(parts, fmt.Sprintf("default:'%s'", cleanDef))
		}
	}

	return fmt.Sprintf(`bun:"%s"`, strings.Join(parts, ","))
}

// buildValidateTag 生成 Go-Playground Validator 标签。
func buildValidateTag(colName, goType string, isNullable bool, enums []string) string {
	if len(enums) > 0 {
		return fmt.Sprintf(`validate:"required,oneof=%s"`, strings.Join(enums, " "))
	}

	if isNullable {
		return `validate:"omitempty"`
	}

	switch goType {
	case goTypeString:
		if colName == "email" {
			return `validate:"required,email,max=255"`
		}
		if colName == colSKU {
			return `validate:"required,min=2,max=64"`
		}
		if colName == "slug" {
			return `validate:"required,min=2,max=128"`
		}
		return `validate:"required,min=1,max=255"`
	case goTypeDecimal:
		return `validate:"required"`
	case goTypeInt, "int64":
		return `validate:"required,gte=0"`
	case "float32", "float64":
		return `validate:"required"`
	case "bool":
		return `validate:"boolean"`
	default:
		return `validate:"required"`
	}
}

// buildExampleValue 针对 Swagger 自动生成合理的示例值。
func buildExampleValue(colName, goType, defaultVal string, enums []string) string {
	if len(enums) > 0 {
		return enums[0]
	}
	if defaultVal != "" && !strings.Contains(defaultVal, "(") {
		return strings.Trim(defaultVal, "'\" ")
	}
	if val, ok := exampleBySemantic(strings.ToLower(colName)); ok {
		return val
	}
	return exampleByType(goType)
}

// exampleBySemantic 根据列名语义推导典型的业务字段示例数据。
func exampleBySemantic(lower string) (string, bool) {
	switch {
	case lower == colSKU:
		return "PROD-2026-001", true
	case lower == "name":
		return "优质示例商品", true
	case lower == "slug":
		return "sample-product", true
	case lower == "brand":
		return "Apple", true
	case lower == "category_id":
		return "019550b7-3b2d-7041-8c43-26154388e36e", true
	case lower == "path":
		return "1.2.3", true
	case strings.Contains(lower, "price"):
		return "99.90", true
	case strings.Contains(lower, "stock"):
		return "100", true
	case lower == "image":
		return "https://example.com/images/product.jpg", true
	case lower == "description":
		return "商品详细图文介绍内容", true
	default:
		return "", false
	}
}

// exampleByType 根据 Go 数据类型推导通用标量示例数据。
func exampleByType(goType string) string {
	switch goType {
	case goTypeLTree:
		return "1.2.3"
	case "bool":
		return "true"
	case goTypeInt, "int64":
		return "10"
	case "float32", "float64":
		return "3.14"
	case goTypeDecimal:
		return "0.00"
	default:
		return "example"
	}
}

func (f ParsedField) wrapPtr(expr string) string {
	if f.IsNullable {
		return "lo.ToPtr(" + expr + ")"
	}
	return expr
}

// FakeExpr 根据字段名、Go类型、可空性与枚举值推导面向 gofakeit/v7 的拟真数据生成表达式。
func (f ParsedField) FakeExpr(pascalName string) string {
	lower := strings.ToLower(f.ColName)

	// 1. 枚举与状态字段
	if len(f.EnumValues) > 0 {
		return fmt.Sprintf("models.%sStatus%s", pascalName, ToPascalCase(f.EnumValues[0]))
	}
	if lower == "status" {
		return fmt.Sprintf("models.%sStatusDraft", pascalName)
	}

	// 2. 语义列名匹配
	if expr, ok := f.fakeExprBySemantic(lower, pascalName); ok {
		return expr
	}

	// 3. 按 Go 类型兜底推导
	return f.fakeExprByGoType(lower)
}

// fakeExprBySemantic 统一调度身份识别、媒体内容与商务地理的语义字段匹配。
func (f ParsedField) fakeExprBySemantic(lower, pascalName string) (string, bool) {
	if expr, ok := f.fakeExprByIdentity(lower, pascalName); ok {
		return expr, true
	}
	if expr, ok := f.fakeExprByMediaAndContent(lower); ok {
		return expr, true
	}
	return f.fakeExprByCommerceAndLocation(lower)
}

// fakeExprByIdentity 匹配编码、标识、姓名与联系方式字段。
func (f ParsedField) fakeExprByIdentity(lower, pascalName string) (string, bool) {
	switch lower {
	case colSKU, "code":
		return `strings.ToUpper(f.LetterN(3)) + "-" + f.DigitN(6)`, true
	case "slug":
		return `strings.ToLower(f.Word()) + "-" + f.DigitN(4)`, true
	case "name", "title":
		if strings.Contains(strings.ToLower(pascalName), "product") || strings.Contains(strings.ToLower(pascalName), "goods") {
			return `f.ProductName()`, true
		}
		if lower == "title" {
			return `f.BookTitle()`, true
		}
		return `f.Name()`, true
	case "first_name":
		return `f.FirstName()`, true
	case "last_name":
		return `f.LastName()`, true
	case "username":
		return `strings.ToLower(f.Username())`, true
	case "email":
		return `strings.ToLower(f.Email())`, true
	case "phone", "mobile", "tel":
		return `f.Phone()`, true
	case "url", "website":
		return `f.URL()`, true
	default:
		return "", false
	}
}

// fakeExprByMediaAndContent 匹配图片媒体、图文介绍与外键关联字段。
func (f ParsedField) fakeExprByMediaAndContent(lower string) (string, bool) {
	switch {
	case lower == colImage || lower == "avatar" || lower == "thumbnail" || lower == "cover":
		return f.wrapPtr(`fmt.Sprintf("https://picsum.photos/seed/%s/400/400", f.UUID())`), true
	case lower == "description" || lower == "content" || lower == "intro" || lower == "summary" || lower == "remark":
		return f.wrapPtr(`f.Paragraph(2, 3, 5, "\n")`), true
	case strings.HasSuffix(lower, "_id") || lower == "parent_id" || lower == "category_id" || lower == "user_id":
		return f.wrapPtr(`newUUID().String()`), true
	default:
		return "", false
	}
}

// fakeExprByCommerceAndLocation 匹配电商交易、库存、品牌与地址定位字段。
func (f ParsedField) fakeExprByCommerceAndLocation(lower string) (string, bool) {
	switch {
	case lower == "brand" || lower == "company":
		return f.wrapPtr(`f.Company()`), true
	case strings.Contains(lower, "stock") || lower == "inventory" || lower == "quantity":
		return `f.Number(10, 500)`, true
	case strings.Contains(lower, "price") || strings.Contains(lower, "amount") || strings.Contains(lower, "cost"):
		if f.IsDecimal() {
			return f.wrapPtr(`decimal.NewFromFloat(f.Price(10, 1000))`), true
		}
		return "", false
	case lower == "ip" || lower == "ip_address":
		return `f.IPv4Address()`, true
	case lower == "country":
		return `f.Country()`, true
	case lower == "city":
		return `f.City()`, true
	case lower == "address":
		return `f.Address().Address`, true
	case lower == "zip" || lower == "postal_code":
		return `f.Zip()`, true
	default:
		return "", false
	}
}

// fakeExprByGoType 基于 Go 静态类型提供拟真数据兜底表达式。
func (f ParsedField) fakeExprByGoType(lower string) string {
	switch f.GoType {
	case goTypeDecimal:
		return f.wrapPtr(`decimal.NewFromFloat(f.Price(10, 1000))`)
	case goTypeString:
		return f.wrapPtr(`f.Word()`)
	case goTypeBool:
		if strings.HasPrefix(lower, "is_") || lower == "enabled" || lower == "active" {
			return "true"
		}
		return "f.Bool()"
	case goTypeInt, goTypeInt64, "int32":
		return "f.Number(1, 100)"
	case "uint", "uint64", "uint32":
		return "uint64(f.Number(1, 100))"
	case goTypeFloat64, goTypeFloat32:
		return "f.Float64Range(1.0, 100.0)"
	case "time.Time":
		return f.wrapPtr(`time.Now().UTC()`)
	case goTypeVector:
		return `vector.NewVector([]float32{float32(f.Float64Range(-1, 1)), float32(f.Float64Range(-1, 1)), float32(f.Float64Range(-1, 1))})`
	case goTypeGeoPoint:
		return `geo.NewPoint(f.Latitude(), f.Longitude())`
	case goTypeLTree:
		return `nestedset.Path("1.1")`
	default:
		if f.IsMap() {
			return `map[string]any{"color": f.Color(), "specs": f.Word()}`
		}
		if f.IsSlice() {
			return `[]string{f.Word(), f.Word()}`
		}
		return `f.Word()`
	}
}
