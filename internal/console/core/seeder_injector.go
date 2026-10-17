package core

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

// ResolveDatabaseSeederPath 返回 database_seeder.go 的绝对或相对路径。
func ResolveDatabaseSeederPath(rootDir string) string {
	return filepath.Join(rootDir, "internal", "database", "seeders", "database_seeder.go")
}

// InjectSeeder 向 internal/database/seeders/database_seeder.go 幂等注入对应 Seeder 调度声明。
func InjectSeeder(rootDir string, opts GeneratorOptions) (bool, error) {
	filePath := ResolveDatabaseSeederPath(rootDir)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read database_seeder.go failed: %w", err)
	}

	content := string(data)
	pascal := ToPascalCase(Singularize(ToSnakeCase(strings.TrimSpace(opts.Name))))
	targetSeeder := fmt.Sprintf("&%sSeeder{},", pascal)

	if strings.Contains(content, targetSeeder) {
		return false, nil // 幂等跳过
	}

	callIdx := strings.Index(content, "runner.Call(ctx,")
	if callIdx == -1 {
		callIdx = strings.Index(content, "runner.CallInTx(ctx,")
	}
	if callIdx == -1 {
		return false, nil
	}

	closingParenIdx := strings.Index(content[callIdx:], ")")
	if closingParenIdx == -1 {
		return false, nil
	}
	closingParenIdx += callIdx

	callBlock := content[callIdx:closingParenIdx]
	lastNewline := strings.LastIndex(callBlock, "\n")
	if lastNewline == -1 {
		return false, nil
	}

	insertPos := callIdx + lastNewline + 1
	var sb strings.Builder
	sb.WriteString(content[:insertPos])
	sb.WriteString("\t\t")
	sb.WriteString(targetSeeder)
	sb.WriteString("\n")
	sb.WriteString(content[insertPos:])

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		formatted = []byte(sb.String())
	}

	if err := os.WriteFile(filePath, formatted, 0o600); err != nil {
		return false, fmt.Errorf("write database_seeder.go failed: %w", err)
	}

	return true, nil
}

// RemoveSeeder 从 internal/database/seeders/database_seeder.go 中安全注销并移除指定的 Seeder 调度声明。
func RemoveSeeder(rootDir string, opts GeneratorOptions) (bool, error) {
	filePath := ResolveDatabaseSeederPath(rootDir)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read database_seeder.go failed: %w", err)
	}

	content := string(data)
	pascal := ToPascalCase(Singularize(ToSnakeCase(strings.TrimSpace(opts.Name))))
	targetSeeder := fmt.Sprintf("&%sSeeder{},", pascal)

	if !strings.Contains(content, targetSeeder) {
		return false, nil
	}

	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	changed := false

	for _, line := range lines {
		if strings.Contains(line, targetSeeder) {
			changed = true
			continue
		}
		filtered = append(filtered, line)
	}

	if !changed {
		return false, nil
	}

	resultText := strings.Join(filtered, "\n")
	formatted, err := format.Source([]byte(resultText))
	if err != nil {
		formatted = []byte(resultText)
	}

	if err := os.WriteFile(filePath, formatted, 0o600); err != nil {
		return false, fmt.Errorf("write database_seeder.go failed: %w", err)
	}

	return true, nil
}
