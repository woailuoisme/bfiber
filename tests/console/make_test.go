package console_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bfiber/internal/console"
	"bfiber/internal/console/core"
)

// TestNamingUtilities 测试命名转换与单复数处理。
func TestNamingUtilities(t *testing.T) {
	tests := []struct {
		input        string
		wantSnake    string
		wantPascal   string
		wantCamel    string
		wantKebab    string
		wantPlural   string
		wantSingular string
	}{
		{
			input:        "article",
			wantSnake:    "article",
			wantPascal:   "Article",
			wantCamel:    "article",
			wantKebab:    "article",
			wantPlural:   "articles",
			wantSingular: "article",
		},
		{
			input:        "category",
			wantSnake:    "category",
			wantPascal:   "Category",
			wantCamel:    "category",
			wantKebab:    "category",
			wantPlural:   "categories",
			wantSingular: "category",
		},
		{
			input:        "article_category",
			wantSnake:    "article_category",
			wantPascal:   "ArticleCategory",
			wantCamel:    "articleCategory",
			wantKebab:    "article-category",
			wantPlural:   "article_categories",
			wantSingular: "article_category",
		},
		{
			input:        "ArticleCategory",
			wantSnake:    "article_category",
			wantPascal:   "ArticleCategory",
			wantCamel:    "articleCategory",
			wantKebab:    "article-category",
			wantPlural:   "article_categories",
			wantSingular: "article_category",
		},
		{
			input:        "status",
			wantSnake:    "status",
			wantPascal:   "Status",
			wantCamel:    "status",
			wantKebab:    "status",
			wantPlural:   "statuses",
			wantSingular: "status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.wantSnake, core.ToSnakeCase(tt.input))
			assert.Equal(t, tt.wantPascal, core.ToPascalCase(tt.input))
			assert.Equal(t, tt.wantCamel, core.ToCamelCase(tt.input))
			assert.Equal(t, tt.wantKebab, core.ToKebabCase(tt.input))
			assert.Equal(t, tt.wantPlural, core.Pluralize(core.ToSnakeCase(tt.input)))
			assert.Equal(t, tt.wantSingular, core.Singularize(tt.wantPlural))
		})
	}
}

// TestGenerateCRUD_Execution 验证代码脚手架在临时目录中的全量生成闭环。
func TestGenerateCRUD_Execution(t *testing.T) {
	tempDir := t.TempDir()

	opts := core.GeneratorOptions{
		Name:        "article",
		IsAdmin:     true,
		TagName:     "文章管理",
		ChineseName: "文章",
		RootDir:     tempDir,
	}

	// 1. 初次生成（包含 5 个常规文件 + schema + factory + seeder = 8 个文件）
	results, err := core.GenerateCRUD(opts)
	require.NoError(t, err)
	require.Len(t, results, 8)

	for _, r := range results {
		assert.Equal(t, core.ActionCreated, r.Action)
		fullPath := filepath.Join(tempDir, r.Path)
		info, statErr := os.Stat(fullPath)
		require.NoError(t, statErr)
		assert.False(t, info.IsDir())

		content, readErr := os.ReadFile(fullPath)
		require.NoError(t, readErr)
		assert.NotEmpty(t, content)
	}

	// 2. 校验特定关键代码模式
	schemaPath := filepath.Join(tempDir, "internal", "database", "schemas", "01-article.sql")
	schemaContent, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Contains(t, string(schemaContent), `CREATE TABLE "articles"`)

	factoryPath := filepath.Join(tempDir, "internal", "database", "factories", "article_factory.go")
	factoryContent, err := os.ReadFile(factoryPath)
	require.NoError(t, err)
	assert.Contains(t, string(factoryContent), "type ArticleFactory struct")

	seederPath := filepath.Join(tempDir, "internal", "database", "seeders", "article_seeder.go")
	seederContent, err := os.ReadFile(seederPath)
	require.NoError(t, err)
	assert.Contains(t, string(seederContent), "type ArticleSeeder struct")

	dtoPath := filepath.Join(tempDir, "internal", "features", "admin", "article", "dto.go")
	dtoContent, err := os.ReadFile(dtoPath)
	require.NoError(t, err)
	assert.Contains(t, string(dtoContent), "ArticlePathParams")
	assert.Contains(t, string(dtoContent), "ListArticleQueryParams")
	assert.Contains(t, string(dtoContent), "ArticleResource")

	handlerPath := filepath.Join(tempDir, "internal", "features", "admin", "article", "handler.go")
	handlerContent, err := os.ReadFile(handlerPath)
	require.NoError(t, err)
	assert.Contains(t, string(handlerContent), "package adminarticle")
	assert.Contains(t, string(handlerContent), "response.Paginated(c, items, meta)")
	assert.Contains(t, string(handlerContent), "@Tags         文章管理")

	routesPath := filepath.Join(tempDir, "internal", "features", "admin", "article", "routes.go")
	routesContent, err := os.ReadFile(routesPath)
	require.NoError(t, err)
	assert.Contains(t, string(routesContent), "RegisterAdminRoutes")
	assert.Contains(t, string(routesContent), `mw.Can(authorization.Resource("articles")`)

	modelPath := filepath.Join(tempDir, "internal", "database", "models", "article.go")
	modelContent, err := os.ReadFile(modelPath)
	require.NoError(t, err)
	assert.Contains(t, string(modelContent), `bun:"table:articles,alias:a"`)
	assert.Contains(t, string(modelContent), "type Article struct")

	// 列表与计数必须共用同一规格：模板生成的取页/计数走 ScanAndCount，
	// 不再产出会与列表谓词漂移的独立 Count 查询。
	assert.Contains(t, string(modelContent), "func ListArticlesAndCount(")
	assert.Contains(t, string(modelContent), "ScanAndCount(ctx)")
	assert.NotContains(t, string(modelContent), "func CountArticles(")
	assert.Contains(t, string(modelContent), "database.EscapeLike(")

	servicePath := filepath.Join(tempDir, "internal", "features", "admin", "article", "service.go")
	serviceContent, err := os.ReadFile(servicePath)
	require.NoError(t, err)
	assert.Contains(t, string(serviceContent), "models.ListArticlesAndCount(")
	assert.Contains(t, string(serviceContent), "pagination.Normalise()")
	assert.NotContains(t, string(serviceContent), "models.CountArticles(")

	// 3. 不带 force 再次生成：
	// 此时 schema 文件已存在于磁盘，CRUD 生成管线自动激活防误触保护（SchemaExists=true），
	// 故后续生成/跳过/覆盖为 7 个代码文件
	results2, err := core.GenerateCRUD(opts)
	require.NoError(t, err)
	require.Len(t, results2, 7)
	for _, r := range results2 {
		assert.Equal(t, core.ActionSkipped, r.Action)
	}

	// 4. 携带 force 标志再次生成，应当覆盖代码文件
	opts.Force = true
	results3, err := core.GenerateCRUD(opts)
	require.NoError(t, err)
	require.Len(t, results3, 7)
	for _, r := range results3 {
		assert.Equal(t, core.ActionOverwritten, r.Action)
	}

	// 5. Dry-run 模拟运行不影响文件
	opts.DryRun = true
	results4, err := core.GenerateCRUD(opts)
	require.NoError(t, err)
	require.Len(t, results4, 7)
	for _, r := range results4 {
		assert.Equal(t, core.ActionDryRun, r.Action)
	}
}

// TestMakeCommand_CLIExecution 验证通过 Cobra 命令入口执行 dry-run。
func TestMakeCommand_CLIExecution(t *testing.T) {
	ctx := context.Background()

	// 旧空格子命令格式已被废除，必须报错：bfiber make crud article --dry-run
	err := console.Execute(ctx, []string{"make", "crud", "article", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "make"`)

	// 测试 Artisan 冒号命令：bfiber make:crud article --dry-run
	err = console.Execute(ctx, []string{"make:crud", "article", "--dry-run"})
	require.NoError(t, err)

	// 缺少必填参数时应报错
	err = console.Execute(ctx, []string{"make:crud"})
	require.Error(t, err)
}

// TestDestroyCRUD_Execution 验证代码脚手架在临时目录中的回退清理闭环。
func TestDestroyCRUD_Execution(t *testing.T) {
	tempDir := t.TempDir()

	opts := core.GeneratorOptions{
		Name:        "article",
		IsAdmin:     true,
		TagName:     "文章管理",
		ChineseName: "文章",
		RootDir:     tempDir,
	}

	// 1. 先生成文件（全新模块生成 8 个文件，含 schema DDL）
	results, err := core.GenerateCRUD(opts)
	require.NoError(t, err)
	require.Len(t, results, 8)

	// 2. Dry-run 回退删除：
	// Schema DDL 作为数据库模式事实源受到持久化保护（SchemaExists=true），
	// 不会被 CRUD 销毁管线误删，故待删除文件为 7 个代码文件。
	opts.DryRun = true
	destroyDryRun, err := core.DestroyCRUD(opts)
	require.NoError(t, err)
	require.Len(t, destroyDryRun, 7)
	for _, r := range destroyDryRun {
		assert.Equal(t, core.ActionDryRun, r.Action)
	}

	// 验证文件依然存在
	handlerPath := filepath.Join(tempDir, "internal", "features", "admin", "article", "handler.go")
	_, err = os.Stat(handlerPath)
	require.NoError(t, err)

	// 3. 真实回退删除
	opts.DryRun = false
	destroyResults, err := core.DestroyCRUD(opts)
	require.NoError(t, err)
	require.Len(t, destroyResults, 7)
	for _, r := range destroyResults {
		assert.Equal(t, core.ActionDeleted, r.Action)
	}

	// 验证生成的代码文件已被物理删除
	for _, r := range destroyResults {
		fullPath := filepath.Join(tempDir, r.Path)
		_, statErr := os.Stat(fullPath)
		assert.True(t, os.IsNotExist(statErr), "File should be deleted: "+fullPath)
	}

	// 验证 Schema DDL 作为数据库事实源被安全保留
	schemaPath := filepath.Join(tempDir, "internal", "database", "schemas", "01-article.sql")
	_, schemaStatErr := os.Stat(schemaPath)
	require.NoError(t, schemaStatErr, "Schema DDL should be preserved as source of truth")

	// 验证空的 feature 目录也被清理
	featureDir := filepath.Join(tempDir, "internal", "features", "admin", "article")
	_, statErr := os.Stat(featureDir)
	assert.True(t, os.IsNotExist(statErr), "Feature directory should be removed: "+featureDir)

	// 4. 重复执行回退删除，应安全跳过（ActionSkipped）
	destroyAgain, err := core.DestroyCRUD(opts)
	require.NoError(t, err)
	require.Len(t, destroyAgain, 7)
	for _, r := range destroyAgain {
		assert.Equal(t, core.ActionSkipped, r.Action)
	}
}

// TestMakeDestroyCommand_CLIExecution 验证通过 make:crud -d / --delete 执行回退删除 dry-run 与废弃命令拦截。
func TestMakeDestroyCommand_CLIExecution(t *testing.T) {
	ctx := context.Background()

	// 1. 旧空格子命令格式已被废除，必须报错：bfiber make destroy article --dry-run
	err := console.Execute(ctx, []string{"make", "destroy", "article", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "make"`)

	// 2. 原 make:destroy 命令已被彻底移除，必须报错：bfiber make:destroy
	err = console.Execute(ctx, []string{"make:destroy", "article", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown command "make:destroy"`)

	// 3. 测试通过 make:crud -d 进行回退删除 dry-run
	err = console.Execute(ctx, []string{"make:crud", "article", "-d", "--dry-run"})
	require.NoError(t, err)

	// 4. 测试通过 make:crud --delete 进行回退删除 dry-run
	err = console.Execute(ctx, []string{"make:crud", "article", "--delete", "--dry-run"})
	require.NoError(t, err)

	// 5. 缺少必填参数时在非 TTY 下应报错
	err = console.Execute(ctx, []string{"make:crud", "-d"})
	require.Error(t, err)
}

// TestMakeSubCommands_CLIExecution 验证 make:schema, make:factory, make:seeder 独立 CLI 命令。
func TestMakeSubCommands_CLIExecution(t *testing.T) {
	ctx := context.Background()

	// 1. 测试 make:schema
	err := console.Execute(ctx, []string{"make:schema", "product", "--dry-run"})
	require.NoError(t, err)
	err = console.Execute(ctx, []string{"make:schema"})
	require.Error(t, err)

	// 2. 测试 make:factory
	err = console.Execute(ctx, []string{"make:factory", "product", "--dry-run"})
	require.NoError(t, err)
	err = console.Execute(ctx, []string{"make:factory"})
	require.Error(t, err)

	// 3. 测试 make:seeder
	err = console.Execute(ctx, []string{"make:seeder", "product", "--dry-run"})
	require.NoError(t, err)
	err = console.Execute(ctx, []string{"make:seeder"})
	require.Error(t, err)
}

// TestGenerateSchemaFactorySeeder_Execution 验证单独生成器在临时目录下的独立创建、覆盖与跳过闭环。
func TestGenerateSchemaFactorySeeder_Execution(t *testing.T) {
	tempDir := t.TempDir()

	opts := core.GeneratorOptions{
		Name:        "order",
		ChineseName: "订单",
		RootDir:     tempDir,
	}

	// 1. 测试 GenerateSchema
	schemaRes, err := core.GenerateSchema(opts)
	require.NoError(t, err)
	require.Len(t, schemaRes, 1)
	assert.Equal(t, core.ActionCreated, schemaRes[0].Action)

	schemaPath := filepath.Join(tempDir, schemaRes[0].Path)
	schemaContent, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Contains(t, string(schemaContent), `CREATE TABLE "orders"`)

	// 再次生成未带 force 应跳过
	schemaRes2, err := core.GenerateSchema(opts)
	require.NoError(t, err)
	assert.Equal(t, core.ActionSkipped, schemaRes2[0].Action)

	// 2. 测试 GenerateFactory
	factoryRes, err := core.GenerateFactory(opts)
	require.NoError(t, err)
	require.Len(t, factoryRes, 1)
	assert.Equal(t, core.ActionCreated, factoryRes[0].Action)

	factoryPath := filepath.Join(tempDir, factoryRes[0].Path)
	factoryContent, err := os.ReadFile(factoryPath)
	require.NoError(t, err)
	assert.Contains(t, string(factoryContent), "type OrderFactory struct")
	assert.Contains(t, string(factoryContent), "package factories")

	// 3. 测试 GenerateSeeder
	seederRes, err := core.GenerateSeeder(opts)
	require.NoError(t, err)
	require.Len(t, seederRes, 1)
	assert.Equal(t, core.ActionCreated, seederRes[0].Action)

	seederPath := filepath.Join(tempDir, seederRes[0].Path)
	seederContent, err := os.ReadFile(seederPath)
	require.NoError(t, err)
	assert.Contains(t, string(seederContent), "type OrderSeeder struct")
	assert.Contains(t, string(seederContent), "package seeders")
}

// TestSeederInjector_Lifecycle 验证 Seeder 自动注入与移除的生命周期及幂等性。
func TestSeederInjector_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	seedersDir := filepath.Join(tempDir, "internal", "database", "seeders")
	require.NoError(t, os.MkdirAll(seedersDir, 0o755))

	mockSeederFile := filepath.Join(seedersDir, "database_seeder.go")
	initialContent := `package seeders

import (
	"context"
	"bfiber/internal/platform/seeder"
)

type DatabaseSeeder struct{}

func (s *DatabaseSeeder) Run(ctx context.Context, runner *seeder.Runner) error {
	return runner.Call(ctx,
		&AdminUserSeeder{},
	)
}
`
	require.NoError(t, os.WriteFile(mockSeederFile, []byte(initialContent), 0o644))

	opts := core.GeneratorOptions{
		Name:    "article",
		RootDir: tempDir,
	}

	// 1. 初次注入
	injected, err := core.InjectSeeder(tempDir, opts)
	require.NoError(t, err)
	assert.True(t, injected)

	content1, err := os.ReadFile(mockSeederFile)
	require.NoError(t, err)
	assert.Contains(t, string(content1), "&ArticleSeeder{},")

	// 2. 重复注入验证幂等性
	injected2, err := core.InjectSeeder(tempDir, opts)
	require.NoError(t, err)
	assert.False(t, injected2, "Duplicate injection should return false")

	// 3. 执行移除
	removed, err := core.RemoveSeeder(tempDir, opts)
	require.NoError(t, err)
	assert.True(t, removed)

	content2, err := os.ReadFile(mockSeederFile)
	require.NoError(t, err)
	assert.NotContains(t, string(content2), "&ArticleSeeder{},")
	assert.Contains(t, string(content2), "&AdminUserSeeder{},")

	// 4. 重复移除验证幂等性
	removed2, err := core.RemoveSeeder(tempDir, opts)
	require.NoError(t, err)
	assert.False(t, removed2, "Duplicate removal should return false")
}
