package rewardsettle

import (
	"errors"
	"sync"
	"time"
)

// Cooldown 奖励完成后确认入账所需的冷却时长。
const Cooldown = 15 * time.Minute

var (
	ErrRewardNotFound   = errors.New("reward not found")
	ErrCaseNotFound     = errors.New("case not found")
	ErrAccountNotFound  = errors.New("account not found")
	ErrCaseExists       = errors.New("case already exists")
	ErrMemberFinalized  = errors.New("case member already confirmed or rejected")
	ErrDecisionConflict = errors.New("case already resolved with a different decision")
	ErrInvalidAmount    = errors.New("amount must be a positive integer")
	ErrDuplicateReward  = errors.New("duplicate reward id")
)

type reward struct {
	id          string
	accountID   string
	amount      int64
	completedAt time.Time
	status      RewardStatus
	caseIDs     []string
}

type caseRecord struct {
	id        string
	status    CaseStatus
	rewardIDs []string
}

// Engine 进程内结算库，所有操作在单把互斥锁下原子完成。
type Engine struct {
	mu       sync.Mutex
	rewards  map[string]*reward
	cases    map[string]*caseRecord
	ledger   map[string][]LedgerEntry // accountID -> entries
	byReward map[string]bool          // 已入账的 rewardID，保证恰一条入账记录
	accounts map[string]bool          // 出现过的账户
}

// New 载入一批已完成奖励，初始均为待确认，账户余额为零。
func New(inputs []RewardInput) (*Engine, error) {
	e := &Engine{
		rewards:  make(map[string]*reward),
		cases:    make(map[string]*caseRecord),
		ledger:   make(map[string][]LedgerEntry),
		byReward: make(map[string]bool),
		accounts: make(map[string]bool),
	}
	for _, in := range inputs {
		if in.Amount <= 0 {
			return nil, ErrInvalidAmount
		}
		if _, dup := e.rewards[in.RewardID]; dup {
			return nil, ErrDuplicateReward
		}
		e.rewards[in.RewardID] = &reward{
			id:          in.RewardID,
			accountID:   in.AccountID,
			amount:      in.Amount,
			completedAt: in.CompletedAt,
			status:      StatusPending,
		}
		e.accounts[in.AccountID] = true
	}
	return e, nil
}

// Freeze 创建未决案件并原子冻结整组奖励。
func (e *Engine) Freeze(caseID string, rewardIDs []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.cases[caseID]; exists {
		return ErrCaseExists
	}
	// 先整体校验：任何成员已确认或已拒绝则整次失败，不留残留。
	for _, id := range rewardIDs {
		r, ok := e.rewards[id]
		if !ok {
			return ErrRewardNotFound
		}
		if r.status != StatusPending {
			return ErrMemberFinalized
		}
	}
	c := &caseRecord{id: caseID, status: CasePending, rewardIDs: append([]string(nil), rewardIDs...)}
	e.cases[caseID] = c
	for _, id := range rewardIDs {
		e.rewards[id].caseIDs = append(e.rewards[id].caseIDs, caseID)
	}
	return nil
}

// Resolve 批准或拒绝案件，结论不可更改。
func (e *Engine) Resolve(caseID string, decision Decision) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return ErrCaseNotFound
	}
	if c.status != CasePending {
		// 同一结论重试无副作用；相反结论返回冲突。
		same := (c.status == CaseApproved && decision == DecisionApprove) ||
			(c.status == CaseRejected && decision == DecisionReject)
		if same {
			return nil
		}
		return ErrDecisionConflict
	}
	if decision == DecisionApprove {
		// 批准只解除本案件的阻挡，不触碰其他案件与奖励状态。
		c.status = CaseApproved
		return nil
	}
	// 拒绝：所有尚未终结的成员永久拒绝，整组原子可见。
	c.status = CaseRejected
	for _, id := range c.rewardIDs {
		if r := e.rewards[id]; r.status == StatusPending {
			r.status = StatusRejected
		}
	}
	return nil
}

// Settle 尝试确认指定奖励，逐笔独立提交。
func (e *Engine) Settle(rewardIDs []string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, id := range rewardIDs {
		r, ok := e.rewards[id]
		if !ok || r.status != StatusPending {
			continue
		}
		// 恰满 15 分钟算到期。
		if now.Before(r.completedAt.Add(Cooldown)) {
			continue
		}
		// 所有关联案件均已批准才可确认；不存在案件也满足条件。
		blocked := false
		for _, cid := range r.caseIDs {
			if e.cases[cid].status != CaseApproved {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		// 检查资格、改变状态和记账在同一临界区内完成。
		r.status = StatusConfirmed
		if !e.byReward[id] {
			e.byReward[id] = true
			e.ledger[r.accountID] = append(e.ledger[r.accountID], LedgerEntry{
				RewardID:  r.id,
				AccountID: r.accountID,
				Amount:    r.amount,
				PostedAt:  now,
			})
		}
	}
}

// QueryReward 查询单个奖励状态。
func (e *Engine) QueryReward(rewardID string) (RewardView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, ok := e.rewards[rewardID]
	if !ok {
		return RewardView{}, ErrRewardNotFound
	}
	return RewardView{
		RewardID:    r.id,
		AccountID:   r.accountID,
		Amount:      r.amount,
		CompletedAt: r.completedAt,
		Status:      r.status,
		CaseIDs:     append([]string(nil), r.caseIDs...),
	}, nil
}

// QueryCase 查询单个案件状态。
func (e *Engine) QueryCase(caseID string) (CaseView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return CaseView{}, ErrCaseNotFound
	}
	return CaseView{
		CaseID:    c.id,
		Status:    c.status,
		RewardIDs: append([]string(nil), c.rewardIDs...),
	}, nil
}

// QueryAccount 查询账户余额及入账记录。
func (e *Engine) QueryAccount(accountID string) (AccountView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entries, ok := e.ledger[accountID]
	if !ok && !e.accounts[accountID] {
		return AccountView{}, ErrAccountNotFound
	}
	view := AccountView{AccountID: accountID, Entries: append([]LedgerEntry(nil), entries...)}
	for _, en := range entries {
		view.Balance += en.Amount
	}
	return view, nil
}
