package mongo

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ExportAll 实现 store.RawExporter：整库导出（备份 Action 用）。
// 与旧 /get/export/collections 完全同构：
//   - 动态列举集合（不写死清单，Keep 这种历史集合删了就自然消失）；
//   - 每个集合一个文档数组；
//   - `_id` 统一转成字符串（ObjectID → 24 位 hex，twikoo 的 32 位 hex 原样保留）。
func (s *Store) ExportAll(ctx context.Context) (map[string][]json.RawMessage, error) {
	names, err := s.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, wrapMongo(err)
	}
	sort.Strings(names)
	out := map[string][]json.RawMessage{}
	for _, name := range names {
		// 跳过系统集合（system.views 等）与空名字
		if name == "" || strings.HasPrefix(name, "system.") {
			continue
		}
		coll := s.db.Collection(name)
		cursor, err := coll.Find(ctx, bson.M{}, options.Find())
		if err != nil {
			return nil, wrapMongo(err)
		}
		docs := []bson.M{}
		if err := cursor.All(ctx, &docs); err != nil {
			return nil, wrapMongo(err)
		}
		// 按 _id 稳定排序：Mongo 的自然顺序不保证跨查询一致，两次导出可能出现
		// 「内容相同但顺序不同」，备份仓库做校验和/逐行 diff 时会得到满屏假差异。
		// 先取出 _id 字符串再排，保证同一份数据每次导出字节级一致。
		for _, doc := range docs {
			if id, ok := doc["_id"]; ok {
				doc["_id"] = idString(id)
			}
		}
		sort.SliceStable(docs, func(i, j int) bool {
			a, _ := docs[i]["_id"].(string)
			b, _ := docs[j]["_id"].(string)
			return a < b
		})
		items := make([]json.RawMessage, 0, len(docs))
		for _, doc := range docs {
			// ⚠️ 不能用 bson.MarshalExtJSON：它会把 Date 输出成 `{"$date":"…"}`、
			// 把 int64 输出成 `{"$numberLong":"…"}`，而旧接口（FastAPI json）给的是
			// ISO 字符串与原样数字。备份仓库按旧格式解析，形状变了就坏。
			raw, err := json.Marshal(toPlainJSON(doc))
			if err != nil {
				return nil, err
			}
			items = append(items, json.RawMessage(raw))
		}
		out[name] = items
	}
	return out, nil
}

// toPlainJSON 把 BSON 值递归转成「与旧 FastAPI json 编码一致」的普通 JSON 值：
//   - Date(bson.DateTime) → ISO 8601 字符串（带微秒与 +00:00 偏移，Python isoformat 的形态）
//   - ObjectID → 24 位 hex 字符串
//   - 内嵌文档 / 数组 → 递归
//   - 数字/字符串/布尔/null → 原样（int64 保持数字，不变成 {"$numberLong":…}）
func toPlainJSON(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bson.M:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = toPlainJSON(vv)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = toPlainJSON(vv)
		}
		return out
	case bson.D:
		// ⚠️ 内嵌文档解码成的是有序文档 bson.D（不是 bson.M）；漏了这个分支，
		// 嵌套的 Date 会原样交给 json.Marshal，变成 Go 的 "...Z" 而不是旧接口的 naive 形态。
		out := make(map[string]any, len(x))
		for _, e := range x {
			out[e.Key] = toPlainJSON(e.Value)
		}
		return out
	case bson.A:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = toPlainJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = toPlainJSON(vv)
		}
		return out
	case time.Time:
		// 有的路径（内嵌文档）会把 Date 解成 time.Time，同样按旧格式输出（naive UTC、微秒）
		return x.UTC().Format("2006-01-02T15:04:05.000000")
	case bson.DateTime:
		// 与旧接口逐字节一致：库里存的是 naive UTC datetime，Python isoformat()
		// 输出不带时区偏移 → `2026-10-17T12:25:03.650000`（不要自作主张加 +00:00）。
		return time.UnixMilli(int64(x)).UTC().Format("2006-01-02T15:04:05.000000")
	case bson.ObjectID:
		return x.Hex()
	case bson.Timestamp:
		return x.T
	case bson.Binary:
		return string(x.Data)
	case bson.Decimal128:
		return x.String()
	case bson.Null:
		return nil
	default:
		return v
	}
}

// idString 把各种 _id 形态转成字符串。
// twikoo 的 comment/config 用 32 位 hex 字符串主键，ObjectID 是 24 位 hex——
// 两种都要原样保留，否则备份仓库的对账逻辑会全部错位。
func idString(id any) string {
	switch v := id.(type) {
	case bson.ObjectID:
		return v.Hex()
	case string:
		return v
	default:
		return toString(v)
	}
}

func toString(v any) string {
	b, err := bson.MarshalExtJSON(bson.M{"v": v}, false, false)
	if err != nil {
		return ""
	}
	var wrap struct {
		V any `json:"v"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return ""
	}
	if s, ok := wrap.V.(string); ok {
		return s
	}
	if f, ok := wrap.V.(float64); ok {
		return trimFloat(f)
	}
	return ""
}

func trimFloat(f float64) string {
	// 整数就去掉小数点（_id 是数字时保持原样字符串化）
	if f == float64(int64(f)) {
		return itoa64(int64(f))
	}
	return strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
}

func formatFloat(f float64) string {
	// 1e21 之类极端值几乎不可能出现在 _id 上；用最简实现
	if f < 0 {
		return "-" + formatFloat(-f)
	}
	intPart := int64(f)
	frac := f - float64(intPart)
	s := itoa64(intPart)
	if frac != 0 {
		s += "."
		for frac > 0 && len(s) < 24 {
			frac *= 10
			d := int(frac)
			s += itoa64(int64(d))
			frac -= float64(d)
		}
	}
	return s
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

var _ store.Store = (*Store)(nil)
