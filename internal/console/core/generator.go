package core

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"bfiber/internal/console/stubs"
)

// GeneratorOptions 封装了代码生成器的入参配置。
type GeneratorOptions struct {
	// Name 是模块名或实体名（支持 CamelCase, snake_case, kebab-case 等格式）
	Name string
	// IsAdmin 标识是否生成管理端模块（默认为 true，路径为 internal/features/admin/<name>/）
	IsAdmin bool
	// TagName 自定义 Swagger 标签名（若留空则自动推导）
	TagName string
	// ChineseName 中文业务名称描述（若留空则默认为 PascalName）
	ChineseName string
	// Force 是否强制覆盖已存在的文件
	Force bool
	// DryRun 是否仅做生成预览而不实际写入文件系统
	DryRun bool
	// RootDir 项目根目录路径（默认为当前工作目录）
	RootDir string
	// NoSchema 是否跳过数据库 Schema DDL 文件生成
	NoSchema bool
	// NoFactory 是否跳过数据工厂 Factory 文件生成
	NoFactory bool
	// NoSeeder 是否跳过数据播种器 Seeder 文件生成
	NoSeeder bool
}

// StatusModifier 封装状态枚举的链式修饰方法元数据。
type StatusModifier struct {
	MethodName string // 如 Draft, Published, Archived
	ConstName  string // 如 ProductStatusDraft
	EnumVal    string // 如 draft
}

// FakeAssign 封装 Factory 生成时每个字段的代码赋值。
type FakeAssign struct {
	FieldName string
	Expr      string
}

// TemplateData 注入到模板渲染上下文的元数据对象。
type TemplateData struct {
	PackageName  string
	PascalName   string
	CamelName    string
	SnakeName    string
	KebabName    string
	PluralSnake  string
	PluralPascal string
	PluralKebab  string
	TableName    string
	TableAlias   string
	TagName      string
	ChineseName  string
	RoutePath    string
	IsAdmin      bool

	// DDL 表结构动态推导特性
	HasCustomFields bool
	HasBase         bool
	HasSoftDeletes  bool
	IsAttachable    bool
	HasDecimal      bool
	HasTime         bool
	HasSKU          bool
	HasSlug         bool
	HasVector       bool
	HasGeo          bool
	HasLTree        bool
	HasArray        bool
	VectorColName   string
	VectorFieldName string
	GeoColName      string
	GeoFieldName    string
	LTreeColName    string
	LTreeFieldName  string
	CustomFields    []ParsedField
	SearchFields    []ParsedField
	SearchSQL       string
	SearchArgs      string
	StatusEnumVals  []string
	DefaultStatus   string
	StatusEnumStr   string

	// 数据层扩展特性（Schema / Factory / Seeder）
	StatusModifiers  []StatusModifier
	FakeAssigns      []FakeAssign
	HasNullableField bool
	SchemaFilename   string
	SchemaExists     bool
}

// FileAction 标识单个生成文件的处理结果动作。
type FileAction string

const (
	ActionCreated     FileAction = "CREATED"
	ActionOverwritten FileAction = "OVERWRITTEN"
	ActionSkipped     FileAction = "SKIPPED"
	ActionDryRun      FileAction = "DRY-RUN"
	ActionDeleted     FileAction = "DELETED"
)

// GeneratedResult 记录单个文件的生成详情。
type GeneratedResult struct {
	Path     string
	Template string
	Action   FileAction
	Message  string
}

// BuildTemplateData 根据输入选项计算全部命名衍生变量。
func BuildTemplateData(opts GeneratorOptions) TemplateData {
	raw := strings.TrimSpace(opts.Name)
	snake := ToSnakeCase(raw)
	singularSnake := Singularize(snake)
	pluralSnake := Pluralize(snake)

	pascal := ToPascalCase(singularSnake)
	camel := ToCamelCase(singularSnake)
	kebab := ToKebabCase(singularSnake)
	pluralPascal := ToPascalCase(pluralSnake)
	pluralKebab := ToKebabCase(pluralSnake)

	pkgName := strings.ReplaceAll(singularSnake, "_", "")
	if opts.IsAdmin {
		pkgName = "admin" + pkgName
	}

	chinese := opts.ChineseName
	if chinese == "" {
		chinese = resolveChineseName(singularSnake, pascal)
	}

	tag := opts.TagName
	if tag == "" {
		if opts.IsAdmin {
			tag = chinese + "管理"
		} else {
			tag = chinese
		}
	}

	routePath := "/" + pluralKebab
	if opts.IsAdmin {
		routePath = "/admin/" + pluralKebab
	}

	tableAlias := "t"
	if len(singularSnake) > 0 {
		tableAlias = singularSnake[:1]
	}

	data := TemplateData{
		PackageName:    pkgName,
		PascalName:     pascal,
		CamelName:      camel,
		SnakeName:      singularSnake,
		KebabName:      kebab,
		PluralSnake:    pluralSnake,
		PluralPascal:   pluralPascal,
		PluralKebab:    pluralKebab,
		TableName:      pluralSnake,
		TableAlias:     tableAlias,
		TagName:        tag,
		ChineseName:    chinese,
		RoutePath:      routePath,
		IsAdmin:        opts.IsAdmin,
		HasBase:        true,
		HasSoftDeletes: true,
		DefaultStatus:  "active",
		StatusEnumVals: []string{"active", "inactive"},
		StatusEnumStr:  "active inactive",
	}

	schemasDir := filepath.Join(opts.RootDir, "internal", "database", "schemas")
	if opts.RootDir == "" {
		schemasDir = filepath.Join("internal", "database", "schemas")
	}

	enrichDataFromDDL(&data, schemasDir, singularSnake)
	return data
}

// resolveChineseName 根据实体模型英文名称推断默认中文名称。
func resolveChineseName(singularSnake, pascal string) string {
	switch singularSnake {
	case "product", "goods":
		return "商品"
	case "order":
		return "订单"
	case "category":
		return "分类"
	case "article", "post":
		return "文章"
	case "user":
		return "用户"
	default:
		return pascal
	}
}

// enrichDataFromDDL 从数据库 schemas/ 目录中检索 DDL 并补全脚手架模板数据。
func enrichDataFromDDL(data *TemplateData, schemasDir, singularSnake string) {
	filename, exists := ResolveSchemaFilename(schemasDir, singularSnake)
	data.SchemaFilename = filename
	data.SchemaExists = exists

	parsedTable, err := ParseTableFromSchemas(schemasDir, singularSnake)
	if err != nil || parsedTable == nil || !parsedTable.Found {
		// 未找到已有 DDL 时，提供通用规范的默认假数据赋值
		data.FakeAssigns = []FakeAssign{
			{FieldName: "Name", Expr: "f.ProductName()"},
			{FieldName: "Code", Expr: `strings.ToUpper(f.LetterN(3)) + "-" + f.DigitN(6)`},
			{FieldName: "Status", Expr: fmt.Sprintf("models.%sStatus%s", data.PascalName, ToPascalCase(data.DefaultStatus))},
			{FieldName: "Description", Expr: `lo.ToPtr(f.Paragraph(2, 3, 5, "\n"))`},
		}
		data.HasNullableField = true

		var modifiers []StatusModifier
		for _, val := range data.StatusEnumVals {
			methodName := ToPascalCase(val)
			constName := fmt.Sprintf("%sStatus%s", data.PascalName, methodName)
			modifiers = append(modifiers, StatusModifier{
				MethodName: methodName,
				ConstName:  constName,
				EnumVal:    val,
			})
		}
		data.StatusModifiers = modifiers
		return
	}

	data.TableName = parsedTable.TableName
	data.HasCustomFields = len(parsedTable.CustomFields) > 0
	data.HasBase = parsedTable.HasBase
	data.HasSoftDeletes = parsedTable.HasSoftDeletes
	data.IsAttachable = parsedTable.IsAttachable
	data.HasDecimal = parsedTable.HasDecimal
	data.HasTime = parsedTable.HasTime
	data.HasVector = parsedTable.HasVector
	data.HasGeo = parsedTable.HasGeo
	data.HasLTree = parsedTable.HasLTree
	data.HasArray = parsedTable.HasArray
	data.VectorColName = parsedTable.VectorColName
	data.VectorFieldName = parsedTable.VectorFieldName
	data.GeoColName = parsedTable.GeoColName
	data.GeoFieldName = parsedTable.GeoFieldName
	data.LTreeColName = parsedTable.LTreeColName
	data.LTreeFieldName = parsedTable.LTreeFieldName
	data.CustomFields = parsedTable.CustomFields

	if len(parsedTable.StatusEnumVals) > 0 {
		data.StatusEnumVals = parsedTable.StatusEnumVals
		data.StatusEnumStr = strings.Join(parsedTable.StatusEnumVals, " ")
		data.DefaultStatus = parsedTable.StatusEnumVals[0]
	}

	for _, f := range parsedTable.CustomFields {
		if strings.EqualFold(f.ColName, colSKU) {
			data.HasSKU = true
		}
		if strings.EqualFold(f.ColName, "slug") {
			data.HasSlug = true
		}
	}

	configureSearchFields(data, parsedTable.CustomFields)

	// 根据 DDL 字段与类型动态组装 gofakeit/v7 生成表达式
	var assigns []FakeAssign
	for _, f := range parsedTable.CustomFields {
		if f.IsNullable {
			data.HasNullableField = true
		}
		assigns = append(assigns, FakeAssign{
			FieldName: f.PascalName,
			Expr:      f.FakeExpr(data.PascalName),
		})
	}
	data.FakeAssigns = assigns

	// 组装状态枚举链式修饰方法元数据
	var modifiers []StatusModifier
	for _, val := range data.StatusEnumVals {
		methodName := ToPascalCase(val)
		constName := fmt.Sprintf("%sStatus%s", data.PascalName, methodName)
		modifiers = append(modifiers, StatusModifier{
			MethodName: methodName,
			ConstName:  constName,
			EnumVal:    val,
		})
	}
	data.StatusModifiers = modifiers
}

// ResolveSchemaFilename 检测指定模块在 schemas 目录下的 SQL 文件。
// 若已存在建表声明或命名匹配，则返回对应文件名且 exists=true；
// 若不存在，则自增计算下一个两位数字序号（如 "04-order.sql"），exists=false。
func ResolveSchemaFilename(schemasDir, singularSnake string) (string, bool) {
	entries, err := os.ReadDir(schemasDir)
	if err != nil {
		return fmt.Sprintf("01-%s.sql", singularSnake), false
	}

	maxSeq := 0
	targetSuffix := fmt.Sprintf("-%s.sql", singularSnake)
	exactMatch := singularSnake + ".sql"

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		name := entry.Name()
		if strings.EqualFold(name, exactMatch) || strings.HasSuffix(strings.ToLower(name), targetSuffix) {
			return name, true
		}

		fullPath := filepath.Join(schemasDir, name)
		if content, err := os.ReadFile(fullPath); err == nil {
			tablePattern := fmt.Sprintf(`(?i)CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?["']?%s["']?`, singularSnake)
			if matched, _ := regexp.Match(tablePattern, content); matched {
				return name, true
			}
		}

		if len(name) >= 2 {
			var seq int
			if n, _ := fmt.Sscanf(name[:2], "%02d", &seq); n == 1 {
				if seq > maxSeq {
					maxSeq = seq
				}
			}
		}
	}

	nextSeq := maxSeq + 1
	return fmt.Sprintf("%02d-%s.sql", nextSeq, singularSnake), false
}

// configureSearchFields 提取多字段检索条件并组装 SQL 片段。
func configureSearchFields(data *TemplateData, fields []ParsedField) {
	var searchFields []ParsedField
	for _, f := range fields {
		lower := strings.ToLower(f.ColName)
		if lower == colSKU || lower == "name" || lower == "title" || lower == "code" {
			searchFields = append(searchFields, f)
		}
	}
	if len(searchFields) == 0 {
		for _, f := range fields {
			if f.GoType == goTypeString && !strings.Contains(f.ColName, "id") && f.ColName != "description" && f.ColName != "status" && f.ColName != colImage {
				searchFields = append(searchFields, f)
				break
			}
		}
	}
	data.SearchFields = searchFields

	if len(searchFields) > 0 {
		var sqlParts []string
		var argParts []string
		for _, f := range searchFields {
			sqlParts = append(sqlParts, fmt.Sprintf("%s.%s ILIKE ?", data.TableAlias, f.ColName))
			argParts = append(argParts, "pattern")
		}
		data.SearchSQL = strings.Join(sqlParts, " OR ")
		data.SearchArgs = strings.Join(argParts, ", ")
	}
}

// PlannedFile 描述待生成文件的路径、模板名与渲染上下文数据。
type PlannedFile struct {
	RelPath  string
	Template string
	Data     any
}

// Plan 描述任意脚手架生成规划契约 (OCP & ISP)。
type Plan interface {
	Name() string
	Files() ([]PlannedFile, error)
	CleanupDirs() []string
}

// CRUDPlan 实现针对 Domain-First CRUD 模块的生成规划契约。
type CRUDPlan struct {
	opts GeneratorOptions
	data TemplateData
}

// NewCRUDPlan 构造针对 CRUD 模块的生成规划。
func NewCRUDPlan(opts GeneratorOptions) *CRUDPlan {
	return &CRUDPlan{
		opts: opts,
		data: BuildTemplateData(opts),
	}
}

// Name 返回当前规划的模块名称。
func (p *CRUDPlan) Name() string {
	return p.opts.Name
}

// Data 返回模板上下文数据。
func (p *CRUDPlan) Data() TemplateData {
	return p.data
}

// WithData 设置自定义模板上下文数据（用于定制装配与单测注入）。
func (p *CRUDPlan) WithData(data TemplateData) *CRUDPlan {
	p.data = data
	return p
}

func (p *CRUDPlan) featureDir() string {
	scope := "client"
	if p.opts.IsAdmin {
		scope = "admin"
	}
	return filepath.Join("internal", "features", scope, p.data.KebabName)
}

// Files 返回 CRUD 规划中全部目标文件及渲染模板。
func (p *CRUDPlan) Files() ([]PlannedFile, error) {
	featureDir := p.featureDir()
	files := []PlannedFile{
		{RelPath: filepath.Join("internal", "database", "models", p.data.SnakeName+".go"), Template: "model.tmpl", Data: p.data},
		{RelPath: filepath.Join(featureDir, "dto.go"), Template: "dto.tmpl", Data: p.data},
		{RelPath: filepath.Join(featureDir, "service.go"), Template: "service.tmpl", Data: p.data},
		{RelPath: filepath.Join(featureDir, "handler.go"), Template: "handler.tmpl", Data: p.data},
		{RelPath: filepath.Join(featureDir, "routes.go"), Template: "routes.tmpl", Data: p.data},
	}

	if !p.opts.NoSchema && !p.data.SchemaExists {
		files = append(files, PlannedFile{
			RelPath:  filepath.Join("internal", "database", "schemas", p.data.SchemaFilename),
			Template: "schema.tmpl",
			Data:     p.data,
		})
	}

	if !p.opts.NoFactory {
		files = append(files, PlannedFile{
			RelPath:  filepath.Join("internal", "database", "factories", p.data.SnakeName+"_factory.go"),
			Template: "factory.tmpl",
			Data:     p.data,
		})
	}

	if !p.opts.NoSeeder {
		files = append(files, PlannedFile{
			RelPath:  filepath.Join("internal", "database", "seeders", p.data.SnakeName+"_seeder.go"),
			Template: "seeder.tmpl",
			Data:     p.data,
		})
	}

	return files, nil
}

// CleanupDirs 返回该模块在销毁回退时需要尝试清理的目录列表。
func (p *CRUDPlan) CleanupDirs() []string {
	return []string{p.featureDir()}
}

// SchemaPlan 封装单独生成 Atlas PostgreSQL 18 DDL 模式的规划。
type SchemaPlan struct {
	opts GeneratorOptions
	data TemplateData
}

// NewSchemaPlan 构造针对单个数据表 Schema 的生成规划。
func NewSchemaPlan(opts GeneratorOptions) *SchemaPlan {
	return &SchemaPlan{
		opts: opts,
		data: BuildTemplateData(opts),
	}
}

// Name 返回规划标识。
func (p *SchemaPlan) Name() string { return p.opts.Name }

// Files 返回 Schema 规划目标文件。
func (p *SchemaPlan) Files() ([]PlannedFile, error) {
	return []PlannedFile{
		{
			RelPath:  filepath.Join("internal", "database", "schemas", p.data.SchemaFilename),
			Template: "schema.tmpl",
			Data:     p.data,
		},
	}, nil
}

// CleanupDirs 返回清理目录。
func (p *SchemaPlan) CleanupDirs() []string { return nil }

// FactoryPlan 封装单独生成数据工厂 Factory 的规划。
type FactoryPlan struct {
	opts GeneratorOptions
	data TemplateData
}

// NewFactoryPlan 构造针对单个数据工厂的生成规划。
func NewFactoryPlan(opts GeneratorOptions) *FactoryPlan {
	return &FactoryPlan{
		opts: opts,
		data: BuildTemplateData(opts),
	}
}

// Name 返回规划标识。
func (p *FactoryPlan) Name() string { return p.opts.Name }

// Files 返回 Factory 规划目标文件。
func (p *FactoryPlan) Files() ([]PlannedFile, error) {
	return []PlannedFile{
		{
			RelPath:  filepath.Join("internal", "database", "factories", p.data.SnakeName+"_factory.go"),
			Template: "factory.tmpl",
			Data:     p.data,
		},
	}, nil
}

// CleanupDirs 返回清理目录。
func (p *FactoryPlan) CleanupDirs() []string { return nil }

// SeederPlan 封装单独生成数据播种器 Seeder 的规划。
type SeederPlan struct {
	opts GeneratorOptions
	data TemplateData
}

// NewSeederPlan 构造针对单个数据播种器的生成规划。
func NewSeederPlan(opts GeneratorOptions) *SeederPlan {
	return &SeederPlan{
		opts: opts,
		data: BuildTemplateData(opts),
	}
}

// Name 返回规划标识。
func (p *SeederPlan) Name() string { return p.opts.Name }

// Files 返回 Seeder 规划目标文件。
func (p *SeederPlan) Files() ([]PlannedFile, error) {
	return []PlannedFile{
		{
			RelPath:  filepath.Join("internal", "database", "seeders", p.data.SnakeName+"_seeder.go"),
			Template: "seeder.tmpl",
			Data:     p.data,
		},
	}, nil
}

// CleanupDirs 返回清理目录。
func (p *SeederPlan) CleanupDirs() []string { return nil }

// Pipeline 提供可插拔的代码生成与回退执行管线引擎 (SRP & OCP)。
type Pipeline struct {
	TemplateFS fs.FS
	RootDir    string
	Force      bool
	DryRun     bool
}

// NewPipeline 构造带有默认配置的生成管线实例。
func NewPipeline(templateFS fs.FS, rootDir string, force, dryRun bool) *Pipeline {
	if templateFS == nil {
		templateFS = stubs.FS
	}
	if rootDir == "" {
		rootDir = "."
	}
	return &Pipeline{
		TemplateFS: templateFS,
		RootDir:    rootDir,
		Force:      force,
		DryRun:     dryRun,
	}
}

// Execute 调度执行指定的生成规划，完成模板读取、渲染、Go格式化、冲突检测与文件写入。
func (p *Pipeline) Execute(plan Plan) ([]GeneratedResult, error) {
	files, err := plan.Files()
	if err != nil {
		return nil, err
	}

	var results []GeneratedResult

	for _, f := range files {
		absPath := filepath.Join(p.RootDir, f.RelPath)
		_, statErr := os.Stat(absPath)
		exists := statErr == nil

		if exists && !p.Force {
			results = append(results, GeneratedResult{
				Path:     f.RelPath,
				Template: f.Template,
				Action:   ActionSkipped,
				Message:  "file already exists, skipped (use --force to overwrite)",
			})
			continue
		}
		tmpl, err := template.ParseFS(p.TemplateFS, f.Template)
		if err != nil {
			return results, fmt.Errorf("failed to parse template %s: %w", f.Template, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, f.Data); err != nil {
			return results, fmt.Errorf("failed to render template %s: %w", f.Template, err)
		}

		formatted := buf.Bytes()
		if strings.HasSuffix(f.RelPath, ".go") {
			src, err := format.Source(formatted)
			if err != nil {
				return results, fmt.Errorf("generated file %s has invalid Go syntax: %w", f.RelPath, err)
			}
			formatted = src
		}

		if p.DryRun {
			results = append(results, GeneratedResult{
				Path:     f.RelPath,
				Template: f.Template,
				Action:   ActionDryRun,
				Message:  fmt.Sprintf("would write %d bytes", len(formatted)),
			})
			continue
		}

		parentDir := filepath.Dir(absPath)
		if err := os.MkdirAll(parentDir, 0o755); err != nil { //nolint:gosec // 生成物目录需对协作者与 CI 可读
			return results, fmt.Errorf("failed to create directory %s: %w", parentDir, err)
		}

		if err := os.WriteFile(absPath, formatted, 0o644); err != nil { //nolint:gosec // 生成物源码需对协作者与 CI 可读
			return results, fmt.Errorf("failed to write file %s: %w", absPath, err)
		}

		action := ActionCreated
		msg := "successfully generated"
		if exists {
			action = ActionOverwritten
			msg = "overwritten existing file"
		}

		results = append(results, GeneratedResult{
			Path:     f.RelPath,
			Template: f.Template,
			Action:   action,
			Message:  msg,
		})
	}

	return results, nil
}

// Destroy 调度执行指定的规划回退，安全删除生成文件与空目录。
func (p *Pipeline) Destroy(plan Plan) ([]GeneratedResult, error) {
	files, err := plan.Files()
	if err != nil {
		return nil, err
	}

	var results []GeneratedResult

	for _, f := range files {
		absPath := filepath.Join(p.RootDir, f.RelPath)

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			results = append(results, GeneratedResult{
				Path:     f.RelPath,
				Template: f.Template,
				Action:   ActionSkipped,
				Message:  "file does not exist, skipped",
			})
			continue
		}

		if p.DryRun {
			results = append(results, GeneratedResult{
				Path:     f.RelPath,
				Template: f.Template,
				Action:   ActionDryRun,
				Message:  "would delete file",
			})
			continue
		}

		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return results, fmt.Errorf("failed to delete file %s: %w", absPath, err)
		}

		results = append(results, GeneratedResult{
			Path:     f.RelPath,
			Template: f.Template,
			Action:   ActionDeleted,
			Message:  "successfully deleted",
		})
	}

	if !p.DryRun {
		for _, dir := range plan.CleanupDirs() {
			absDir := filepath.Join(p.RootDir, dir)
			_ = os.Remove(absDir)
		}
	}

	return results, nil
}

// GenerateCRUD 执行完整的 CRUD 模块代码脚手架生成。
func GenerateCRUD(opts GeneratorOptions) ([]GeneratedResult, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("module name cannot be empty")
	}

	plan := NewCRUDPlan(opts)
	pipeline := NewPipeline(stubs.FS, opts.RootDir, opts.Force, opts.DryRun)
	return pipeline.Execute(plan)
}

// DestroyCRUD 回退并安全清理指定 CRUD 模块的生成文件与空目录。
func DestroyCRUD(opts GeneratorOptions) ([]GeneratedResult, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("module name cannot be empty")
	}

	plan := NewCRUDPlan(opts)
	pipeline := NewPipeline(stubs.FS, opts.RootDir, opts.Force, opts.DryRun)
	return pipeline.Destroy(plan)
}

// GenerateSchema 执行单独生成数据表 Schema SQL 文件的操作。
func GenerateSchema(opts GeneratorOptions) ([]GeneratedResult, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("table/module name cannot be empty")
	}

	plan := NewSchemaPlan(opts)
	pipeline := NewPipeline(stubs.FS, opts.RootDir, opts.Force, opts.DryRun)
	return pipeline.Execute(plan)
}

// GenerateFactory 执行单独生成数据工厂 Factory Go 代码文件的操作。
func GenerateFactory(opts GeneratorOptions) ([]GeneratedResult, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("model/module name cannot be empty")
	}

	plan := NewFactoryPlan(opts)
	pipeline := NewPipeline(stubs.FS, opts.RootDir, opts.Force, opts.DryRun)
	return pipeline.Execute(plan)
}

// GenerateSeeder 执行单独生成数据播种器 Seeder Go 代码文件的操作。
func GenerateSeeder(opts GeneratorOptions) ([]GeneratedResult, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("model/module name cannot be empty")
	}

	plan := NewSeederPlan(opts)
	pipeline := NewPipeline(stubs.FS, opts.RootDir, opts.Force, opts.DryRun)
	return pipeline.Execute(plan)
}
