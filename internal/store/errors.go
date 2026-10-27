// Package store 定义存储层接口。
//
// 铁律（docs/store-interfaces.md §0）：
//   - 本包顶层只允许 context/time/encoding/json/internal/model，
//     绝不出现 bson/primitive/mongo-driver——那是适配器内部的事。
//   - 所有方法第一个参数是 ctx，并且必须真的传给驱动（取消传播）。
//   - 切片返回值没有数据也返回空切片，不返回 nil（JSON 出 null 会炸前端 .map）。
//   - ObjectID 在接口层一律是 string，转换只在 mongo 适配器内部发生。
package store

import "errors"

// 错误哨兵：service 层只认这三个，不许出现 mongo.ErrNoDocuments 之类的驱动错误。
var (
	ErrNotFound = errors.New("store: not found")

	ErrDuplicate = errors.New("store: duplicate key")

	// ErrReadOnly 是「连生产库做只读验证」的强制闸门：
	// database.readOnly=true 时适配器里每个写方法的第一行就返回它，
	// 由 apperr 统一映射成 503 storage.read_only。不是约定，是代码。
	ErrReadOnly = errors.New("store: read-only mode")
)
