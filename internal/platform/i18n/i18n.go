// Package i18n 装载语言包，并负责语言协商与文案渲染。
package i18n

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	contribi18n "github.com/gofiber/contrib/v3/i18n"
	"github.com/gofiber/fiber/v3"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"

	"bfiber/internal/config"
	"bfiber/lang"
)

// 语言包内的分节名，与 lang/<语言>/<分节>.toml 一一对应。
const (
	sectionValidation = "validation"
	sectionFields     = "fields"
	sectionMail       = "mail"
	sectionInvoice    = "invoice"
)

var defaultSections = []string{sectionValidation, sectionFields, sectionMail, sectionInvoice}

// Translator 统一封装 Fiber 官方扩展 github.com/gofiber/contrib/v3/i18n 与 go-i18n/v2，
// 统一提供基于 Fiber 请求上下文 (fiber.Ctx) 的现代本地化与纯内存强类型文案渲染。
type Translator struct {
	core       *contribi18n.I18n
	bundle     *goi18n.Bundle
	localizers map[string]*goi18n.Localizer
	fallback   string
	languages  []string
}

type fsBundleLoader struct {
	fsys fs.FS
}

func (l *fsBundleLoader) LoadMessage(path string) ([]byte, error) {
	langDir := strings.TrimSuffix(filepath.Base(path), ".toml")
	return loadLanguageBytes(l.fsys, langDir)
}

// loadLanguageBytes 动态遍历并读取单门语言目录下的全部 TOML 文案文件，合并为包含分节 Table 的单份数据流。
func loadLanguageBytes(fsys fs.FS, langDir string) ([]byte, error) {
	entries, err := fs.ReadDir(fsys, langDir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read language directory %s failed: %w", langDir, err)
	}

	var buf bytes.Buffer
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		section := strings.TrimSuffix(entry.Name(), ".toml")
		content, err := fs.ReadFile(fsys, langDir+"/"+entry.Name())
		if err != nil {
			return nil, err
		}
		// 将各分节包装进 TOML Table: [section]
		fmt.Fprintf(&buf, "[%s]\n", section)
		buf.Write(content)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}

// NewWithFS 遵循依赖倒置原则，从指定的文件系统装载全部语言包。
func NewWithFS(defaultLanguage string, fsys fs.FS) (*Translator, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("i18n: read language directory failed: %w", err)
	}

	var languages []string
	var tags []language.Tag
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		langName := entry.Name()
		languages = append(languages, langName)
		if tag, err := language.Parse(langName); err == nil {
			tags = append(tags, tag)
		}
	}
	if len(languages) == 0 {
		return nil, errors.New("i18n: no language bundle found")
	}

	slices.Sort(languages)

	if !slices.Contains(languages, defaultLanguage) {
		return nil, fmt.Errorf("i18n: default language %q bundle not found", defaultLanguage)
	}

	defaultTag, err := language.Parse(defaultLanguage)
	if err != nil {
		defaultTag = language.English
	}

	loader := &fsBundleLoader{fsys: fsys}
	core := contribi18n.New(&contribi18n.Config{
		RootPath:         ".",
		FormatBundleFile: "toml",
		UnmarshalFunc:    toml.Unmarshal,
		DefaultLanguage:  defaultTag,
		AcceptLanguages:  tags,
		Loader:           loader,
	})

	bundle := goi18n.NewBundle(defaultTag)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	localizers := make(map[string]*goi18n.Localizer, len(languages))
	for _, langName := range languages {
		buf, err := loadLanguageBytes(fsys, langName)
		if err != nil {
			return nil, err
		}
		if _, err := bundle.ParseMessageFileBytes(buf, langName+".toml"); err != nil {
			return nil, fmt.Errorf("i18n: parse language %s failed: %w", langName, err)
		}
		localizers[langName] = goi18n.NewLocalizer(bundle, langName, defaultLanguage)
	}

	return &Translator{
		core:       core,
		bundle:     bundle,
		localizers: localizers,
		fallback:   defaultLanguage,
		languages:  languages,
	}, nil
}

// New 装载内嵌的全部语言包，保持现有配置签名兼容。
func New(cfg *config.Config) (*Translator, error) {
	return NewWithFS(cfg.I18n.DefaultLanguage, lang.FS)
}

// Fiber 返回底层的 Fiber v3 官方 contrib/i18n 实例。
func (t *Translator) Fiber() *contribi18n.I18n {
	if t == nil {
		return nil
	}
	return t.core
}

// Bundle 返回底层的 go-i18n/v2.Bundle 实例。
func (t *Translator) Bundle() *goi18n.Bundle {
	if t == nil {
		return nil
	}
	return t.bundle
}

// Localizer 返回指定语言的 go-i18n 本地化器；语言无法直接命中时执行语言协商，最终回退默认语言。
func (t *Translator) Localizer(lang string) *goi18n.Localizer {
	if t == nil {
		return nil
	}
	if loc, ok := t.localizers[lang]; ok {
		return loc
	}
	matched := t.Language(lang)
	if loc, ok := t.localizers[matched]; ok {
		return loc
	}
	return t.localizers[t.fallback]
}

// Localize 使用 Fiber v3 官方 contrib/i18n 执行本地化翻译。
// params 支持 string（消息 ID）或 *i18n.LocalizeConfig。
func (t *Translator) Localize(c fiber.Ctx, params any) (string, error) {
	if t == nil || t.core == nil {
		return fmt.Sprintf("%v", params), nil
	}
	return t.core.Localize(c, params)
}

// MustLocalize 使用 Fiber v3 官方 contrib/i18n 执行本地化翻译。
// params 支持 string（消息 ID）或 *i18n.LocalizeConfig。
func (t *Translator) MustLocalize(c fiber.Ctx, params any) string {
	if t == nil || t.core == nil {
		return fmt.Sprintf("%v", params)
	}
	return t.core.MustLocalize(c, params)
}

var (
	defaultMu         sync.RWMutex
	defaultTranslator *Translator
)

// SetDefault 注册全局默认翻译器。
func SetDefault(translator *Translator) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultTranslator = translator
}

// Default 返回全局默认翻译器；未注册时为 nil，调用方须自行判断。
func Default() *Translator {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultTranslator
}

// Language 从 Accept-Language 协商出可用语言，无法匹配时回退默认语言。
// 按优先级逐个候选取值尝试，谁先命中就用谁。
func (t *Translator) Language(acceptLanguage string) string {
	for part := range strings.SplitSeq(acceptLanguage, ",") {
		tag, _, _ := strings.Cut(part, ";")
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if matched := t.match(tag); matched != "" {
			return matched
		}
	}
	return t.fallback
}

// match 依次尝试精确匹配、逐级去掉子标签，最后按主语言前缀匹配已有语言包。
// 因此 en-GB 命中 en，zh-Hans-CN 命中 zh-CN。
func (t *Translator) match(tag string) string {
	for candidate := tag; candidate != ""; {
		if slices.Contains(t.languages, candidate) {
			return candidate
		}

		prefix, _, found := strings.CutLast(candidate, "-")
		if !found {
			break
		}
		candidate = prefix
	}

	primary, _, _ := strings.Cut(tag, "-")
	for _, name := range t.languages {
		if name == primary || strings.HasPrefix(name, primary+"-") {
			return name
		}
	}
	return ""
}

// lookupMessage 使用共享 go-i18n/v2 本地化器检索并渲染文案，自动处理语言回退与变量占位符。
func (t *Translator) lookupMessage(language, messageID string, data ...any) string {
	if t == nil {
		return ""
	}
	loc := t.Localizer(language)
	if loc == nil {
		return ""
	}

	var templateData any
	if len(data) > 0 {
		templateData = data[0]
	}

	msg, err := loc.Localize(&goi18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: templateData,
	})
	if err != nil {
		return ""
	}
	return msg
}

// ValidationMessage 渲染一条校验失败文案；键或语言缺失时回退默认语言。
func (t *Translator) ValidationMessage(language, key, field, param string) string {
	label := cmp.Or(t.FieldLabel(language, field), humanize(field))
	return t.lookupMessage(language, sectionValidation+"."+key, map[string]string{
		"Field": label,
		"Param": param,
	})
}

// Mail 渲染一封邮件的主题与正文；共用一个 <key>_subject / <key>_body 的键对。
func (t *Translator) Mail(language, key string, vars map[string]string) (subject, body string) {
	subject = t.lookupMessage(language, sectionMail+"."+key+"_subject", vars)
	body = t.lookupMessage(language, sectionMail+"."+key+"_body", vars)
	return subject, body
}

// Invoice 渲染发票与结算凭据的展示文案；键或语言缺失时回退默认语言。
func (t *Translator) Invoice(language, key string, vars map[string]string) string {
	return t.lookupMessage(language, sectionInvoice+"."+key, vars)
}

// T 统一通用翻译契约：支持 "section.key" 路径语法（例如 "auth.login_failed" 或 "validation.required"）。
// 支持传入可选变量映射 vars 进行 {{.Key}} 占位符动态替换；缺键时返回空串。
func (t *Translator) T(language, key string, vars ...map[string]string) string {
	var v map[string]string
	if len(vars) > 0 {
		v = vars[0]
	}

	if strings.Contains(key, ".") {
		return t.lookupMessage(language, key, v)
	}

	for _, sec := range defaultSections {
		if msg := t.lookupMessage(language, sec+"."+key, v); msg != "" {
			return msg
		}
	}
	return t.lookupMessage(language, key, v)
}

// FieldLabel 返回字段路径对应的可读标签，缺键时支持末级字段名回退，仍缺失时返回空串交由调用方兜底。
func (t *Translator) FieldLabel(language, field string) string {
	if label := t.lookupMessage(language, sectionFields+"."+field); label != "" {
		return label
	}

	// 支持路径解构回退（例如 items.0.email 或 user.avatar 回退查找 email 或 avatar）
	if _, last, found := strings.CutLast(field, "."); found && last != "" {
		if _, err := strconv.Atoi(last); err != nil {
			return t.lookupMessage(language, sectionFields+"."+last)
		}
	}
	return ""
}

// humanize 把字段路径的最后一段（camelCase 或 snake_case）转成空格分隔的小写短语。
func humanize(field string) string {
	_, segment, found := strings.CutLast(field, ".")
	if !found {
		segment = field
	}
	if segment == "" {
		return field
	}

	var b strings.Builder
	b.Grow(len(segment) + 4)
	for i, r := range segment {
		if r == '_' {
			b.WriteByte(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
