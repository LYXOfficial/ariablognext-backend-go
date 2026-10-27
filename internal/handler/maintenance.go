package handler

import (
	"encoding/json"

	"github.com/LYXOfficial/ariablognext-backend-go/internal/app"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/apperr"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/config"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/service"
	"github.com/LYXOfficial/ariablognext-backend-go/internal/store"
)

// maintenanceExport：POST /maintenance/export（内部 token，必须保留）。
// 调用方是私有仓库 ariablognext-autobackup：把 data.collections 拆成
// exported_collections/<集合名>.json（每个文件是文档数组，_id 是字符串）。
// 走 store.RawExporter 可选能力；实现方不支持时 501（SQL 适配器未来可能没有）。
func maintenanceExport(st store.Store, cfg *config.Config) app.Handler {
	return func(c *app.Ctx) (any, error) {
		if !cfg.Features.Export {
			return nil, apperr.New(apperr.UpstreamFailed, "全库导出功能未启用", nil)
		}
		exporter, ok := st.(store.RawExporter)
		if !ok {
			return nil, apperr.New(apperr.SearchNotImplemented, "当前存储不支持整库导出", nil)
		}
		collections, err := exporter.ExportAll(c.Context())
		if err != nil {
			return nil, err
		}
		// collections 的每个值已是文档数组的原始 JSON；包进 map[string]json.RawMessage
		// 避免再解一遍（2500 文档二次序列化很浪费）
		out := make(map[string]json.RawMessage, len(collections))
		for name, docs := range collections {
			raw, err := json.Marshal(docs)
			if err != nil {
				return nil, apperr.Wrap(apperr.InternalError, "导出序列化失败", err)
			}
			out[name] = json.RawMessage(raw)
		}
		return map[string]any{"collections": out}, nil
	}
}

// linksLatency：POST /links/latency（内部 token）——check-flink 上报延迟。
// 旧实现把 secret 放 body 明文，这里改成 X-Internal-Token 头（契约 §3.4）。
// 兼容旧 payload {secret, data:{linkStatus:[{id,latency}]}}：胶水过渡期会翻译。
func linksLatency(svc *service.Set) app.Handler {
	return func(c *app.Ctx) (any, error) {
		var body struct {
			Items []struct {
				ID      string  `json:"id"`
				Latency float64 `json:"latency"`
			} `json:"items"`
		}
		if err := c.Bind(&body); err != nil {
			return nil, err
		}
		if len(body.Items) == 0 {
			return nil, apperr.New(apperr.ValidationMissingField, "items 不能为空", nil)
		}
		updates := make([]store.LatencyUpdate, 0, len(body.Items))
		for _, it := range body.Items {
			if it.ID == "" {
				continue
			}
			updates = append(updates, store.LatencyUpdate{ID: it.ID, Latency: it.Latency})
		}
		n, err := svc.Links.SetLatency(c.Context(), updates)
		if err != nil {
			return nil, err
		}
		return map[string]any{"updated": n}, nil
	}
}
