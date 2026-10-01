package rewardsettle

import "time"

// RewardStatus 奖励的生命周期状态。
type RewardStatus int

const (
	StatusPending   RewardStatus = iota // 待确认
	StatusConfirmed                     // 已确认入账
	StatusRejected                      // 已永久拒绝
)

func (s RewardStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusConfirmed:
		return "confirmed"
	case StatusRejected:
		return "rejected"
	}
	return "unknown"
}

// Decision 案件复核结论。
type Decision int

const (
	DecisionApprove Decision = iota // 批准
	DecisionReject                  // 拒绝
)

func (d Decision) String() string {
	switch d {
	case DecisionApprove:
		return "approve"
	case DecisionReject:
		return "reject"
	}
	return "unknown"
}

// CaseStatus 案件状态。
type CaseStatus int

const (
	CasePending  CaseStatus = iota // 未决
	CaseApproved                   // 已批准
	CaseRejected                   // 已拒绝
)

func (s CaseStatus) String() string {
	switch s {
	case CasePending:
		return "pending"
	case CaseApproved:
		return "approved"
	case CaseRejected:
		return "rejected"
	}
	return "unknown"
}

// RewardInput 构造组件时载入的一笔已完成奖励。
type RewardInput struct {
	RewardID    string
	AccountID   string
	Amount      int64 // 正整数币额
	CompletedAt time.Time
}

// RewardView 单个奖励的查询视图。
type RewardView struct {
	RewardID    string
	AccountID   string
	Amount      int64
	CompletedAt time.Time
	Status      RewardStatus
	CaseIDs     []string // 关联案件
}

// CaseView 单个案件的查询视图。
type CaseView struct {
	CaseID    string
	Status    CaseStatus
	RewardIDs []string
}

// LedgerEntry 入账记录，每笔确认奖励恰好一条。
type LedgerEntry struct {
	RewardID  string
	AccountID string
	Amount    int64
	PostedAt  time.Time
}

// AccountView 账户查询视图。
type AccountView struct {
	AccountID string
	Balance   int64
	Entries   []LedgerEntry
}
