package model

import "time"

// LinkApplication 是友链申请。
// type: 0=新增 1=修改；status: pending / approved / rejected。
// createdAt / decidedAt 在库里是 Date（要参与 RFC3339 输出与 TTL 清理），
// 是全服务仅有的两个 time.Time 字段，其余时间都是 unix 秒。
type LinkApplication struct {
	ID          string       `bson:"_id" json:"id"`
	Type        int          `bson:"type" json:"type"`
	Name        string       `bson:"name" json:"name"`
	Link        string       `bson:"link" json:"link"`
	Avatar      string       `bson:"avatar" json:"avatar"`
	Descr       string       `bson:"descr" json:"descr"`
	Color       string       `bson:"color" json:"color"`
	Email       string       `bson:"email" json:"email"`
	TargetURL   *string      `bson:"targetUrl,omitempty" json:"targetUrl,omitempty"`
	TargetGroup *string      `bson:"targetGroup,omitempty" json:"targetGroup,omitempty"`
	TargetID    *string      `bson:"targetId,omitempty" json:"targetId,omitempty"`
	Status      string       `bson:"status" json:"status"`
	Reason      string       `bson:"reason" json:"reason"`
	AIScreening *AIScreening `bson:"aiScreening,omitempty" json:"aiScreening,omitempty"`
	CreatedAt   time.Time    `bson:"createdAt" json:"createdAt"`
	DecidedAt   *time.Time   `bson:"decidedAt,omitempty" json:"decidedAt,omitempty"`
	// IP 只落库不外露（旧后端存了但 status 接口不返回）；限流与滥用排查用
	IP string `bson:"ip,omitempty" json:"-"`
}

// AIScreening 是 GitHub Actions 初筛回写的结论。
type AIScreening struct {
	Verdict    string    `bson:"verdict" json:"verdict"`
	Backlink   string    `bson:"backlink" json:"backlink"`
	Summary    string    `bson:"summary" json:"summary"`
	Reason     string    `bson:"reason" json:"reason"`
	Model      string    `bson:"model" json:"model"`
	ScreenedAt time.Time `bson:"screenedAt" json:"screenedAt"`
}

// 申请状态常量
const (
	ApplicationStatusPending  = "pending"
	ApplicationStatusApproved = "approved"
	ApplicationStatusRejected = "rejected"
)
