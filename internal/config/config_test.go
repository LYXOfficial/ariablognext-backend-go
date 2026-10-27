package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 基准配置：覆盖所有字段，避免 KnownFields 把缺的键报出来干扰用例焦点。
const baseYAML = `
server:
  listen: ""
  timezone: Asia/Shanghai
  requestTimeout: 8s
  timeoutOverrides:
    /maintenance/export: 120s
  dbTimeout: 5s
  bodyLimit:
    default: 1MB
    markdown: 2MB
    image: 4MB
  cors:
    allowOrigins: ["*"]
    allowCredentials: true
    allowHeaders: ["*"]
    allowMethods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
list:
  defaultPageSize: 16
  maxPageSize: 100
  maxFullItems: 300
rateLimit:
  global: {requests: 300, window: 1m}
  login: {requests: 10, window: 10m}
  linkApply: {requests: 5, window: 30m}
  themeColor: {requests: 60, window: 1h}
database:
  type: mongo
  uri: "{{ env.TEST_MONGODB_URI }}"
  name: test_db
  pool: {max: 20, min: 0, idleTimeout: 5m}
  readOnly: false
auth:
  jwtSecret: "test-secret"
  jwtAlgorithm: HS256
  jwtTTL: 168h
  internalToken: "test-secret"
site:
  name: 测试站
  url: https://example.com
  refreshUrl: https://example.com/refreshCache
  adminPanelUrl: https://example.com/admin
search:
  maxKeywordLength: 64
  contextRadius: 50
  titleBoost: true
media:
  enabled: false
  s3:
    endpoint: ""
    accessKeyId: ""
    secretAccessKey: ""
    region: ""
    bucket: ""
    publicUrl: ""
  upload:
    maxBytes: 4MB
    allowedTypes: ["image/png"]
  import:
    maxBytes: 3MB
    timeout: 20s
mail:
  enabled: false
  host: ""
  port: ""
  secure: ""
  user: ""
  pass: ""
  from: ""
  fromName: 友链
flink:
  maxName: 50
  maxDescription: 200
  maxUrl: 300
  pendingLimit: 20
  retentionDays: 14
  adminEmail: ""
  screening: {enabled: false, repo: "", token: ""}
  latencyCheck: {enabled: false, repo: r, workflow: w.yml, token: ""}
ai:
  summary: {enabled: false, endpoint: https://e, model: m, apiKey: "", timeout: 60s, cooldown: 10m}
  embedding: {enabled: false, provider: "", model: "", apiKey: ""}
features:
  export: true
  semanticSearch: false
`

// loadForTest 把 yaml 写进临时文件并指给 CONFIG_FILE，
// 返回 Load 的结果。env 由用例自己用 t.Setenv 铺。
func loadForTest(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	t.Setenv("TEST_MONGODB_URI", "mongodb://127.0.0.1:27017")
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	return Load()
}

func TestLoadOK(t *testing.T) {
	cfg, err := loadForTest(t, baseYAML)
	if err != nil {
		t.Fatalf("正常配置不该报错：%v", err)
	}
	if cfg.Database.Name != "test_db" {
		t.Errorf("database.name = %q", cfg.Database.Name)
	}
	if cfg.Server.RequestTimeout.Std() != 8*time.Second {
		t.Errorf("requestTimeout = %v", cfg.Server.RequestTimeout.Std())
	}
	if cfg.Server.BodyLimit.Image.Int() != 4*1024*1024 {
		t.Errorf("bodyLimit.image = %d", cfg.Server.BodyLimit.Image)
	}
	if cfg.Server.TimeoutOverrides["/maintenance/export"].Std() != 120*time.Second {
		t.Errorf("timeoutOverrides 未生效")
	}
}

func TestPlaceholderReplaced(t *testing.T) {
	cfg, err := loadForTest(t, baseYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.URI != "mongodb://127.0.0.1:27017" {
		t.Errorf("占位符没有被替换：%q", cfg.Database.URI)
	}
}

func TestMissingEnvReported(t *testing.T) {
	y := strings.Replace(baseYAML, `uri: "{{ env.TEST_MONGODB_URI }}"`, `uri: "{{ env.TEST_NOT_SET_A }}"`, 1)
	_, err := loadForTest(t, y)
	if err == nil {
		t.Fatal("缺 env 必须报错")
	}
	if !strings.Contains(err.Error(), "TEST_NOT_SET_A") {
		t.Errorf("错误信息里要有变量名：%v", err)
	}
}

func TestMultipleMissingEnvReported(t *testing.T) {
	// 缺多个变量必须一次报全，报一个改一轮太折磨人
	y := strings.Replace(baseYAML, `uri: "{{ env.TEST_MONGODB_URI }}"`,
		`uri: "{{ env.TEST_NOT_SET_A }}"`, 1)
	y = strings.Replace(y, `adminEmail: ""`, `adminEmail: "{{ env.TEST_NOT_SET_B }}"`, 1)
	_, err := loadForTest(t, y)
	if err == nil {
		t.Fatal("缺 env 必须报错")
	}
	if !strings.Contains(err.Error(), "TEST_NOT_SET_A") || !strings.Contains(err.Error(), "TEST_NOT_SET_B") {
		t.Errorf("两个缺失都要报出来：%v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	y := baseYAML + "\nfoo: 1\n"
	if _, err := loadForTest(t, y); err == nil {
		t.Fatal("未知字段必须报错（KnownFields）")
	}
}

func TestEnabledSubsystemWithEmptyKeyFails(t *testing.T) {
	// enabled=true 但密钥为空 = 配置矛盾，必须启动失败，且要说清是哪个开关缺哪个 key
	y := strings.Replace(baseYAML, "media:\n  enabled: false", "media:\n  enabled: true", 1)
	_, err := loadForTest(t, y)
	if err == nil {
		t.Fatal("media.enabled=true 但 S3 全空，必须报错")
	}
	if !strings.Contains(err.Error(), "media.enabled") || !strings.Contains(err.Error(), "S3") {
		t.Errorf("错误信息要写明开关与缺失项：%v", err)
	}
}

func TestDefinedButEmptyEnvAllowed(t *testing.T) {
	// 「定义为空」与「没定义」是两回事：定义为空要能通过（enabled=false 时无所谓）
	y := strings.Replace(baseYAML, `uri: "{{ env.TEST_MONGODB_URI }}"`, `uri: "{{ env.TEST_EMPTY_URI }}"`, 1)
	t.Setenv("TEST_EMPTY_URI", "")
	// uri 为空会被必填校验拦住，但错误必须是「uri 为空」而不是「TEST_EMPTY_URI 未定义」
	_, err := loadForTest(t, y)
	if err == nil {
		t.Fatal("uri 为空该被必填校验拦下")
	}
	if strings.Contains(err.Error(), "TEST_EMPTY_URI") {
		t.Errorf("定义为空不该按「未定义」报错：%v", err)
	}
	if !strings.Contains(err.Error(), "database.uri") {
		t.Errorf("应该按必填缺失报错：%v", err)
	}
}

func TestRedacted(t *testing.T) {
	y := strings.Replace(baseYAML, `jwtSecret: "test-secret"`, `jwtSecret: "{{ env.TEST_SECRET }}"`, 1)
	t.Setenv("TEST_SECRET", "super-secret-value")
	cfg, err := loadForTest(t, y)
	if err != nil {
		t.Fatal(err)
	}
	out := cfg.Redacted()
	blob := mustJSON(t, out)
	if strings.Contains(blob, "super-secret-value") {
		t.Errorf("脱敏后的配置里泄漏了密钥：%s", blob)
	}
	if !strings.Contains(blob, `"jwtSecret":"***"`) {
		t.Errorf("密钥位置应是 ***：%s", blob)
	}
	if !strings.Contains(blob, `"uri":"***"`) {
		t.Errorf("连接串也算敏感：%s", blob)
	}
	// 非敏感字段要原样保留
	if !strings.Contains(blob, `"name":"test_db"`) {
		t.Errorf("非敏感字段不应被打码：%s", blob)
	}
}

func TestLocationIsExplicit(t *testing.T) {
	cfg, err := loadForTest(t, baseYAML)
	if err != nil {
		t.Fatal(err)
	}
	loc := cfg.Location()
	if loc.String() != "Asia/Shanghai" {
		t.Errorf("Location = %v", loc)
	}
	// 绝对不允许回落到 time.Local：Vercel 上它是 UTC，会造成归档月界漂移
	if loc == time.Local {
		t.Error("Location 不能是 time.Local")
	}
}

// 未定义的可选环境变量按空处理、不阻止启动（EMBEDDING_API_KEY 类问题的回归测试）：
// embedding 关着、apiKey 引用一个从未定义的变量 → Load 必须成功。
func TestUndefinedOptionalEnvBootsFine(t *testing.T) {
	y := strings.Replace(baseYAML,
		`embedding: {enabled: false, provider: "", model: "", apiKey: ""}`,
		`embedding: {enabled: false, provider: "", model: "", apiKey: "{{ env.TEST_NEVER_DEFINED }}"}`, 1)
	cfg, err := loadForTest(t, y)
	if err != nil {
		t.Fatalf("未定义的可选变量不应阻止启动：%v", err)
	}
	if cfg.AI.Embedding.APIKey != "" {
		t.Errorf("应为空：%q", cfg.AI.Embedding.APIKey)
	}
}
