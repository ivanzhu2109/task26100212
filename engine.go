package rewardsettle

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrDuplicateRewardID 构造时奖励 ID 重复。
	ErrDuplicateRewardID = errors.New("rewardsettle: duplicate reward id")
	// ErrInvalidAmount 构造时币额非正整数。
	ErrInvalidAmount = errors.New("rewardsettle: amount must be positive")
	// ErrDuplicateCaseID 案件 ID 已存在。
	ErrDuplicateCaseID = errors.New("rewardsettle: duplicate case id")
	// ErrUnknownCase 案件不存在。
	ErrUnknownCase = errors.New("rewardsettle: unknown case")
	// ErrUnknownReward 奖励不存在。
	ErrUnknownReward = errors.New("rewardsettle: unknown reward")
	// ErrMemberFinalized 冻结组中存在已确认或已拒绝的成员，整次创建失败。
	ErrMemberFinalized = errors.New("rewardsettle: member already confirmed or rejected")
	// ErrDecisionConflict 案件已有相反结论。
	ErrDecisionConflict = errors.New("rewardsettle: conflicting decision")
	// ErrInvalidDecision 非法的复核结论。
	ErrInvalidDecision = errors.New("rewardsettle: invalid decision")
)

type rewardState struct {
	reward Reward
}

type caseState struct {
	status    CaseStatus
	rewardIDs []string
}

// Engine 是进程内结算库的核心组件，所有方法并发安全。
type Engine struct {
	mu        sync.Mutex
	rewards   map[string]*rewardState
	rewardSeq []string // 保持载入顺序
	cases     map[string]*caseState
	entries   []LedgerEntry             // 全部入账记录，按入账顺序
	byReward  map[string]*LedgerEntry   // rewardID -> 唯一入账记录
	byAccount map[string][]*LedgerEntry // accountID -> 入账记录
	balances  map[string]int64
}

// New 构造组件并载入一批已完成奖励，初始均为待确认，账户余额为零。
func New(inputs []RewardInput) (*Engine, error) {
	e := &Engine{
		rewards:   make(map[string]*rewardState),
		cases:     make(map[string]*caseState),
		byReward:  make(map[string]*LedgerEntry),
		byAccount: make(map[string][]*LedgerEntry),
		balances:  make(map[string]int64),
	}
	for _, in := range inputs {
		if _, dup := e.rewards[in.RewardID]; dup {
			return nil, ErrDuplicateRewardID
		}
		if in.Amount <= 0 {
			return nil, ErrInvalidAmount
		}
		e.rewards[in.RewardID] = &rewardState{reward: Reward{
			RewardID:    in.RewardID,
			AccountID:   in.AccountID,
			Amount:      in.Amount,
			CompletedAt: in.CompletedAt,
			Status:      StatusPending,
		}}
		e.rewardSeq = append(e.rewardSeq, in.RewardID)
	}
	return e, nil
}

// Freeze 创建一个未决案件并原子冻结整组奖励。
// 任一成员已确认或已拒绝则整次失败，不留案件或部分冻结。
func (e *Engine) Freeze(caseID string, rewardIDs []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, dup := e.cases[caseID]; dup {
		return ErrDuplicateCaseID
	}
	// 先完成全部校验再变更，保证失败时不留任何残留。
	for _, id := range rewardIDs {
		rs, ok := e.rewards[id]
		if !ok {
			return ErrUnknownReward
		}
		if rs.reward.Status != StatusPending {
			return ErrMemberFinalized
		}
	}
	e.cases[caseID] = &caseState{status: CasePending, rewardIDs: append([]string(nil), rewardIDs...)}
	for _, id := range rewardIDs {
		rs := e.rewards[id]
		rs.reward.CaseIDs = append(rs.reward.CaseIDs, caseID)
	}
	return nil
}

// Resolve 批准或拒绝案件。同一结论重试无副作用，相反结论返回冲突。
func (e *Engine) Resolve(caseID string, decision Decision) error {
	if decision != DecisionApprove && decision != DecisionReject {
		return ErrInvalidDecision
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	cs, ok := e.cases[caseID]
	if !ok {
		return ErrUnknownCase
	}
	if cs.status != CasePending {
		// 已终结：同结论幂等，相反结论冲突。
		if (cs.status == CaseApproved) == (decision == DecisionApprove) {
			return nil
		}
		return ErrDecisionConflict
	}
	if decision == DecisionApprove {
		cs.status = CaseApproved
		return nil
	}
	cs.status = CaseRejected
	// 拒绝使所有尚未终结的成员永久拒绝，整组在同一把锁内原子可见。
	for _, id := range cs.rewardIDs {
		rs := e.rewards[id]
		if rs.reward.Status == StatusPending {
			rs.reward.Status = StatusRejected
		}
	}
	return nil
}

// Settle 尝试确认指定奖励。每笔独立提交；未到期或仍受冻结的保持待确认。
// 返回本次成功确认的奖励 ID。
func (e *Engine) Settle(rewardIDs []string, now time.Time) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, id := range rewardIDs {
		if _, ok := e.rewards[id]; !ok {
			return nil, ErrUnknownReward
		}
	}
	var settled []string
	for _, id := range rewardIDs {
		rs := e.rewards[id]
		if rs.reward.Status != StatusPending {
			continue // 已确认（幂等）或已拒绝
		}
		if now.Before(rs.reward.CompletedAt.Add(Cooldown)) {
			continue // 未到期；恰满 15 分钟视为到期
		}
		if e.blockedLocked(rs.reward) {
			continue // 仍有未决或已拒绝的关联案件
		}
		// 资格检查、状态变更与记账在同一把锁内完成，是同一个原子动作。
		rs.reward.Status = StatusConfirmed
		entry := &LedgerEntry{
			RewardID:  rs.reward.RewardID,
			AccountID: rs.reward.AccountID,
			Amount:    rs.reward.Amount,
			SettledAt: now,
		}
		e.entries = append(e.entries, *entry)
		e.byReward[id] = entry
		e.byAccount[entry.AccountID] = append(e.byAccount[entry.AccountID], entry)
		e.balances[entry.AccountID] += entry.Amount
		settled = append(settled, id)
	}
	return settled, nil
}

// blockedLocked 报告奖励是否仍受冻结：存在未决或已拒绝的关联案件。
// 调用方须持有 e.mu。
func (e *Engine) blockedLocked(r Reward) bool {
	for _, cid := range r.CaseIDs {
		if cs := e.cases[cid]; cs != nil && cs.status != CaseApproved {
			return true
		}
	}
	return false
}

// QueryReward 查询单个奖励的状态；若已确认，同时返回其唯一入账记录。
func (e *Engine) QueryReward(rewardID string) (Reward, *LedgerEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rs, ok := e.rewards[rewardID]
	if !ok {
		return Reward{}, nil, ErrUnknownReward
	}
	r := rs.reward
	r.CaseIDs = append([]string(nil), r.CaseIDs...)
	var entry *LedgerEntry
	if le, ok := e.byReward[rewardID]; ok {
		cp := *le
		entry = &cp
	}
	return r, entry, nil
}

// QueryCase 查询单个案件的状态。
func (e *Engine) QueryCase(caseID string) (Case, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	cs, ok := e.cases[caseID]
	if !ok {
		return Case{}, ErrUnknownCase
	}
	return Case{
		CaseID:    caseID,
		Status:    cs.status,
		RewardIDs: append([]string(nil), cs.rewardIDs...),
	}, nil
}

// QueryAccount 查询账户余额及入账记录。
func (e *Engine) QueryAccount(accountID string) AccountView {
	e.mu.Lock()
	defer e.mu.Unlock()

	view := AccountView{AccountID: accountID, Balance: e.balances[accountID]}
	for _, le := range e.byAccount[accountID] {
		view.Entries = append(view.Entries, *le)
	}
	return view
}
