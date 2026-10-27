package fake

import (
	"context"
	"encoding/json"
)

// ExportAll 实现 store.RawExporter：把内存态导出成「集合名 → 文档数组」。
// 供 /maintenance/export 的 handler 单测使用，结构必须与 mongo 版一致
// （每个集合是文档数组、_id 是字符串），否则测试过了线上还是崩。
func (s *Store) ExportAll(ctx context.Context) (map[string][]json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := map[string][]json.RawMessage{}
	// 接口形状是「集合名 → 文档数组」：先把整个集合 marshal 再拆成逐文档 RawMessage，
	// 保证 mongo 版导出 _id 字符串的语义在 fake 里也被复现。
	put := func(name string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var docs []json.RawMessage
		if err := json.Unmarshal(raw, &docs); err != nil {
			return err
		}
		out[name] = docs
		return nil
	}
	if len(s.posts) > 0 {
		if err := put("Posts", s.posts); err != nil {
			return nil, err
		}
	}
	if len(s.drafts) > 0 {
		if err := put("Drafts", s.drafts); err != nil {
			return nil, err
		}
	}
	if len(s.speaks) > 0 {
		if err := put("Speaks", s.speaks); err != nil {
			return nil, err
		}
	}
	if len(s.groups) > 0 {
		if err := put("FLinks", s.groups); err != nil {
			return nil, err
		}
	}
	if len(s.applications) > 0 {
		if err := put("FLinkApplications", s.applications); err != nil {
			return nil, err
		}
	}
	if len(s.siteInfo) > 0 {
		if err := put("webSiteInfo", s.siteInfo); err != nil {
			return nil, err
		}
	}
	return out, nil
}
