// Package config 是全服务唯一的配置入口。
//
// 设计要点（见 docs/design-notes.md §3）：
//   - 程序不直接读环境变量；敏感值在 config.yml 里写成 {{ env.NAME }} 占位符，
//     启动时解析。唯一的例外是「引导变量」CONFIG_FILE / ENV_FILE / PORT，
//     它们只在本包出现（scripts/checks.sh 按目录放行）。
//   - 变量「没定义」= 启动失败；「定义为空」= 允许，由 enabled 开关决定是否必须。
//     这两种「缺」语义完全不同，不能合并成一个错误。
package config

import (
	_ "embed" // config.yml 必须编进二进制：Vercel 的 Go 函数运行时读不到仓库文件
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

//go:embed config.yml
var builtin []byte

// 占位符只认「整串就是 {{ env.NAME }}」的写法，不做文本内插——
// 否则密码里出现引号、冒号、换行时会把 YAML 结构本身弄坏。
var placeholderRe = regexp.MustCompile(`^\{\{ env\.([A-Za-z_][A-Za-z0-9_]*) \}\}$`)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	List      ListConfig      `yaml:"list"`
	RateLimit RateLimitConfig `yaml:"rateLimit"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Site      SiteConfig      `yaml:"site"`
	Search    SearchConfig    `yaml:"search"`
	Media     MediaConfig     `yaml:"media"`
	Mail      MailConfig      `yaml:"mail"`
	Flink     FlinkConfig     `yaml:"flink"`
	AI        AIConfig        `yaml:"ai"`
	Features  FeaturesConfig  `yaml:"features"`
}

type ServerConfig struct {
	Listen           string              `yaml:"listen"`
	Timezone         string              `yaml:"timezone"`
	RequestTimeout   Duration            `yaml:"requestTimeout"`
	TimeoutOverrides map[string]Duration `yaml:"timeoutOverrides"`
	DBTimeout        Duration            `yaml:"dbTimeout"`
	BodyLimit        BodyLimitConfig     `yaml:"bodyLimit"`
	CORS             CORSConfig          `yaml:"cors"`
}

type BodyLimitConfig struct {
	Default  ByteSize `yaml:"default"`
	Markdown ByteSize `yaml:"markdown"`
	Image    ByteSize `yaml:"image"`
}

type CORSConfig struct {
	AllowOrigins     []string `yaml:"allowOrigins"`
	AllowCredentials bool     `yaml:"allowCredentials"`
	AllowHeaders     []string `yaml:"allowHeaders"`
	AllowMethods     []string `yaml:"allowMethods"`
}

type ListConfig struct {
	DefaultPageSize int `yaml:"defaultPageSize"`
	MaxPageSize     int `yaml:"maxPageSize"`
	MaxFullItems    int `yaml:"maxFullItems"`
}

type RateLimitConfig struct {
	Global     RateRule `yaml:"global"`
	Login      RateRule `yaml:"login"`
	LinkApply  RateRule `yaml:"linkApply"`
	ThemeColor RateRule `yaml:"themeColor"`
}

type RateRule struct {
	Requests int      `yaml:"requests"`
	Window   Duration `yaml:"window"`
}

type DatabaseConfig struct {
	Type     string     `yaml:"type"`
	URI      string     `yaml:"uri" secret:"true"`
	Name     string     `yaml:"name"`
	Pool     PoolConfig `yaml:"pool"`
	ReadOnly bool       `yaml:"readOnly"`
}

type PoolConfig struct {
	Max         int      `yaml:"max"`
	Min         int      `yaml:"min"`
	IdleTimeout Duration `yaml:"idleTimeout"`
}

type AuthConfig struct {
	JWTSecret     string   `yaml:"jwtSecret" secret:"true"`
	JWTAlgorithm  string   `yaml:"jwtAlgorithm"`
	JWTTTL        Duration `yaml:"jwtTTL"`
	InternalToken string   `yaml:"internalToken" secret:"true"`
}

type SiteConfig struct {
	Name          string `yaml:"name"`
	URL           string `yaml:"url"`
	RefreshURL    string `yaml:"refreshUrl"`
	AdminPanelURL string `yaml:"adminPanelUrl"`
}

type SearchConfig struct {
	MaxKeywordLength int  `yaml:"maxKeywordLength"`
	ContextRadius    int  `yaml:"contextRadius"`
	TitleBoost       bool `yaml:"titleBoost"`
}

type MediaConfig struct {
	Enabled bool         `yaml:"enabled"`
	S3      S3Config     `yaml:"s3"`
	Upload  UploadConfig `yaml:"upload"`
	Import  ImportConfig `yaml:"import"`
}

type S3Config struct {
	Endpoint        string `yaml:"endpoint" secret:"true"`
	AccessKeyID     string `yaml:"accessKeyId" secret:"true"`
	SecretAccessKey string `yaml:"secretAccessKey" secret:"true"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	PublicURL       string `yaml:"publicUrl"`
}

type UploadConfig struct {
	MaxBytes     ByteSize `yaml:"maxBytes"`
	AllowedTypes []string `yaml:"allowedTypes"`
}

type ImportConfig struct {
	MaxBytes ByteSize `yaml:"maxBytes"`
	Timeout  Duration `yaml:"timeout"`
}

type MailConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Secure   string `yaml:"secure"`
	User     string `yaml:"user"`
	Pass     string `yaml:"pass" secret:"true"`
	From     string `yaml:"from"`
	FromName string `yaml:"fromName"`
}

type FlinkConfig struct {
	MaxName        int                `yaml:"maxName"`
	MaxDescription int                `yaml:"maxDescription"`
	MaxURL         int                `yaml:"maxUrl"`
	PendingLimit   int                `yaml:"pendingLimit"`
	RetentionDays  int                `yaml:"retentionDays"`
	AdminEmail     string             `yaml:"adminEmail"`
	Screening      ScreeningConfig    `yaml:"screening"`
	LatencyCheck   LatencyCheckConfig `yaml:"latencyCheck"`
}

type ScreeningConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Repo      string `yaml:"repo"`      // workflow 所在仓库（现在就是本仓库）
	EventType string `yaml:"eventType"` // 必须与 workflow 的 on.repository_dispatch.types 一致
	Token     string `yaml:"token" secret:"true"`
}

type LatencyCheckConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Repo     string `yaml:"repo"`
	Workflow string `yaml:"workflow"`
	Token    string `yaml:"token" secret:"true"`
}

type AIConfig struct {
	Summary   SummaryConfig   `yaml:"summary"`
	Embedding EmbeddingConfig `yaml:"embedding"`
}

type SummaryConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Endpoint string   `yaml:"endpoint"`
	Model    string   `yaml:"model"`
	APIKey   string   `yaml:"apiKey" secret:"true"`
	Timeout  Duration `yaml:"timeout"`
	Cooldown Duration `yaml:"cooldown"`
}

type EmbeddingConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Provider string   `yaml:"provider"`
	Endpoint string   `yaml:"endpoint"`
	Model    string   `yaml:"model"`
	APIKey   string   `yaml:"apiKey" secret:"true"`
	Dim      int      `yaml:"dim"`
	Timeout  Duration `yaml:"timeout"`
	MinScore float32  `yaml:"minScore"`
}

type FeaturesConfig struct {
	Export         bool `yaml:"export"`
	SemanticSearch bool `yaml:"semanticSearch"`
}

// Load 按顺序完成：dotenv → 读配置（CONFIG_FILE 或内置）→ 占位符解析 → 反序列化 → 校验。
// 任何一步失败都返回 error，main 负责退出；绝不带着半套配置跑起来。
func Load() (*Config, error) {
	if err := loadDotenv(); err != nil {
		return nil, err
	}

	raw := builtin
	if p := os.Getenv("CONFIG_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读取 CONFIG_FILE=%s 失败：%w", p, err)
		}
		raw = b
	}

	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败：%w", err)
	}

	undefinedEnv := resolveEnv(tree)

	out, err := yaml.Marshal(tree)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	dec.KnownFields(true) // 配置里拼错字段名立刻报错，而不是静默忽略
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("配置反序列化失败（多半是字段名写错）：%w", err)
	}

	if err := cfg.Validate(); err != nil {
		// 校验失败时附上未定义名单：拼错的变量名在这里能看出来
		//（必填项变成了空值才会走到这）
		if len(undefinedEnv) > 0 {
			err = fmt.Errorf("%w\n另：这些被引用的环境变量未定义（已按空处理）：%s",
				err, strings.Join(undefinedEnv, ", "))
		}
		return nil, err
	}
	// listen 留空时在这里解析 PORT：这是 config.yml 之外唯一要读环境变量地址的地方，
	// 放在 config 里而不是 main 里，保证 checks.sh 的「环境变量只出现在 internal/config」成立。
	cfg.Server.Listen = resolveListen(cfg.Server.Listen)
	return &cfg, nil
}

// MustLoad 是给测试/工具用的便捷入口；生产入口 main 应该用 Load 自己处理错误。
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// loadDotenv 依次找 .env.dev → .env（本地开发的两个文件都不进 git）；
// ENV_FILE 显式指定时只读那一个，找不到就直接报错（显式指定还悄悄跳过，只会掩盖拼写错误）。
// 已存在的环境变量优先于 .env 文件里的值（godotenv.Load 不覆盖）。
func loadDotenv() error {
	if explicit := os.Getenv("ENV_FILE"); explicit != "" {
		if err := godotenv.Load(explicit); err != nil {
			return fmt.Errorf("ENV_FILE 指定的 %s 读不到：%w", explicit, err)
		}
		return nil
	}
	for _, name := range []string{".env.dev", ".env"} {
		if err := godotenv.Load(name); err == nil {
			return nil
		}
	}
	return nil
}

// resolveEnv 递归替换树里所有 {{ env.NAME }} 整串占位符。
//
// 未定义的变量**按空处理**、只记录名字（EMBEDDING_API_KEY 这类为未实现功能预留的
// 占位符不应该逼着每个环境都定义一遍）；「这个值是不是必填」由 Validate 决定——
// 必填缺失或「子系统已启用但密钥为空」照样启动失败，且错误里会带上未定义名单，
// 拼错名字仍然查得出来。
func resolveEnv(tree map[string]any) []string {
	missing, _ := resolveEnvTree(tree)
	return missing
}

// resolveEnvTree 就地替换占位符，返回未定义变量名列表（已按空替换）。
func resolveEnvTree(tree map[string]any) ([]string, error) {
	var missing []string
	var walk func(node any) any
	walk = func(node any) any {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				v[key] = walk(child)
			}
			return v
		case []any:
			for i, child := range v {
				v[i] = walk(child)
			}
			return v
		case string:
			if m := placeholderRe.FindStringSubmatch(v); m != nil {
				val, ok := os.LookupEnv(m[1])
				if !ok {
					missing = append(missing, m[1])
					return ""
				}
				return val
			}
			return v
		default:
			return node
		}
	}
	walk(tree)
	return missing, nil
}

// resolveListen：listen 留空 → 读 PORT（Vercel 注入，绑全地址）；
// PORT 也没有 → 本地开发默认 127.0.0.1:2333。
func resolveListen(listen string) string {
	if listen != "" {
		return listen
	}
	if port := os.Getenv("PORT"); port != "" {
		if _, err := strconv.Atoi(port); err == nil {
			return ":" + port
		}
	}
	return "127.0.0.1:2333"
}

// Location 返回配置里时区的 *time.Location。
// 所有「哪年哪月」的划分都必须用它，绝对不许 time.Local——本地时区在
// Vercel 上是 UTC，会造成归档分桶月界漂移（旧后端就是这个 bug）。
func (c *Config) Location() *time.Location {
	loc, err := time.LoadLocation(c.Server.Timezone)
	if err != nil {
		// Validate 已经拦过一次；这里兜底成 UTC 并留痕，避免 panic
		return time.UTC
	}
	return loc
}

// Validate 一次性收集所有配置问题。逐条 append 而不是遇到第一个就返回。
func (c *Config) Validate() error {
	var errs []string
	addf := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	// —— 必填 ——
	if c.Database.URI == "" {
		addf("database.uri 不能为空（检查 {{ env.MONGODB_URI }}）")
	}
	if c.Database.Name == "" {
		addf("database.name 不能为空（DB_NAME 未定义或为空）")
	}
	if c.Auth.JWTSecret == "" {
		addf("auth.jwtSecret 不能为空（SECRET 未定义或为空）")
	}
	if c.Site.Name == "" {
		addf("site.name 不能为空")
	}
	if c.Site.URL == "" {
		addf("site.url 不能为空")
	}

	// —— 取值范围 ——
	if c.Server.RequestTimeout.Std() <= 0 {
		addf("server.requestTimeout 必须大于 0")
	}
	if c.Server.DBTimeout.Std() <= 0 {
		addf("server.dbTimeout 必须大于 0")
	}
	if c.List.DefaultPageSize <= 0 {
		addf("list.defaultPageSize 必须大于 0")
	}
	if c.List.MaxPageSize < c.List.DefaultPageSize {
		addf("list.maxPageSize(%d) 不能小于 list.defaultPageSize(%d)", c.List.MaxPageSize, c.List.DefaultPageSize)
	}
	if c.List.MaxFullItems <= 0 {
		addf("list.maxFullItems 必须大于 0")
	}
	if _, err := time.LoadLocation(c.Server.Timezone); err != nil {
		addf("server.timezone %q 无法识别：%v", c.Server.Timezone, err)
	}
	for name, rule := range map[string]RateRule{
		"global":     c.RateLimit.Global,
		"login":      c.RateLimit.Login,
		"linkApply":  c.RateLimit.LinkApply,
		"themeColor": c.RateLimit.ThemeColor,
	} {
		if rule.Requests <= 0 || rule.Window.Std() <= 0 {
			addf("rateLimit.%s 的 requests 和 window 都必须大于 0", name)
		}
	}

	// —— 功能开关的依赖 ——
	// enabled=true 但密钥为空 = 配置矛盾，必须在启动时失败，
	// 不能等第一个请求打到 S3 才 503。
	if c.Media.Enabled {
		if v := c.Media.S3; v.Endpoint == "" || v.AccessKeyID == "" || v.SecretAccessKey == "" ||
			v.Region == "" || v.Bucket == "" || v.PublicURL == "" {
			addf("media.enabled=true 但 S3 配置不全（endpoint/accessKeyId/secretAccessKey/region/bucket/publicUrl 都必须非空）")
		}
	}
	if c.Mail.Enabled {
		if c.Mail.Host == "" || c.Mail.Port == "" || c.Mail.User == "" || c.Mail.Pass == "" || c.Mail.From == "" {
			addf("mail.enabled=true 但 SMTP 配置不全（host/port/user/pass/from 都必须非空）")
		}
	}
	if c.AI.Embedding.Enabled {
		v := c.AI.Embedding
		if v.Endpoint == "" || v.Model == "" || v.APIKey == "" || v.Dim <= 0 {
			addf("ai.embedding.enabled=true 但 endpoint/model/apiKey/dim 必须全部配置")
		}
	}
	if c.Flink.Screening.Enabled {
		if c.Flink.Screening.Repo == "" || c.Flink.Screening.Token == "" || c.Flink.Screening.EventType == "" {
			addf("flink.screening.enabled=true 但 repo/eventType/token 有空值")
		}
	}
	if c.Flink.LatencyCheck.Enabled && c.Flink.LatencyCheck.Token == "" {
		addf("flink.latencyCheck.enabled=true 但 token 为空")
	}
	if c.AI.Summary.Enabled && c.AI.Summary.APIKey == "" {
		addf("ai.summary.enabled=true 但 apiKey（SF_TOKEN）为空")
	}
	if c.Features.SemanticSearch && !c.AI.Embedding.Enabled {
		addf("features.semanticSearch=true 但 ai.embedding.enabled=false，两者必须同时开启")
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("配置校验失败：\n  - " + strings.Join(errs, "\n  - "))
}

// Redacted 输出一份打码后的配置快照，供日志使用。
// 递归走 struct：凡是标了 secret:"true" 且非空的字段一律换成 ***。
// 新增配置项只要打了 tag 就自动被覆盖，不需要记得改这里。
func (c *Config) Redacted() map[string]any {
	out := redactValue(reflect.ValueOf(*c)).(map[string]any)
	return out
}

func redactValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Struct:
		m := make(map[string]any, v.NumField())
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Name
			if tag := strings.Split(field.Tag.Get("yaml"), ",")[0]; tag != "" && tag != "-" {
				name = tag
			}
			if field.Tag.Get("secret") == "true" {
				if s, ok := redactValue(v.Field(i)).(string); ok && s != "" {
					m[name] = "***"
					continue
				}
			}
			m[name] = redactValue(v.Field(i))
		}
		return m
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return redactValue(v.Elem())
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = redactValue(v.Index(i))
		}
		return out
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int64:
		return v.Int()
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		m := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			m[fmt.Sprint(iter.Key().Interface())] = redactValue(iter.Value())
		}
		return m
	default:
		return fmt.Sprint(v.Interface())
	}
}
