// Package rewardsettle 提供进程内的奖励冷却与风控复核结算库。
package rewardsettle

import "time"

// Cooldown 是奖励完成后到可确认入账的冷却时长。
const Cooldown = 15 * time.Minute

// RewardStatus 表示奖励的生命周期状态。
type RewardStatus string

const (
	// StatusPending 待确认：尚未入账，可能仍在冷却或被案件冻结。
	StatusPending RewardStatus = "pending"
	// StatusConfirmed 已确认：已入账，产生唯一一条入账记录。
	StatusConfirmed RewardStatus = "confirmed"
	// StatusRejected 已拒绝：被任一关联案件拒绝，永久不可入账。
	StatusRejected RewardStatus = "rejected"
)

// Decision 是案件的复核结论。
type Decision string

const (
	// DecisionApprove 批准：解除本案件对成员的阻挡。
	DecisionApprove Decision = "approve"
	// DecisionReject 拒绝：使所有尚未终结的成员永久拒绝。
	DecisionReject Decision = "reject"
)

// CaseStatus 表示案件的复核状态。
type CaseStatus string

const (
	// CasePending 案件未决。
	CasePending CaseStatus = "pending"
	// CaseApproved 案件已批准。
	CaseApproved CaseStatus = "approved"
	// CaseRejected 案件已拒绝。
	CaseRejected CaseStatus = "rejected"
)

// RewardInput 是构造组件时载入的一笔已完成奖励。
type RewardInput struct {
	RewardID    string
	AccountID   string
	Amount      int64 // 正整数币额
	CompletedAt time.Time
}

// Reward 是一笔奖励的当前状态视图。
type Reward struct {
	RewardID    string
	AccountID   string
	Amount      int64
	CompletedAt time.Time
	Status      RewardStatus
	CaseIDs     []string // 关联案件，按冻结顺序
}

// Case 是一个风控复核案件的当前状态视图。
type Case struct {
	CaseID    string
	Status    CaseStatus
	RewardIDs []string
}

// LedgerEntry 是一条入账记录，每笔确认奖励恰有一条。
type LedgerEntry struct {
	RewardID  string
	AccountID string
	Amount    int64
	SettledAt time.Time
}

// AccountView 是账户状态视图：余额恒等于全部确认奖励币额之和。
type AccountView struct {
	AccountID string
	Balance   int64
	Entries   []LedgerEntry
}
