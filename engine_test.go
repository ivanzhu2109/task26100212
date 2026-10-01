package rewardsettle

import (
	"errors"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func mustNew(t *testing.T, inputs []RewardInput) *Engine {
	t.Helper()
	e, err := New(inputs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func rewardStatus(t *testing.T, e *Engine, id string) RewardStatus {
	t.Helper()
	v, err := e.QueryReward(id)
	if err != nil {
		t.Fatalf("QueryReward(%s): %v", id, err)
	}
	return v.Status
}

func accountBalance(t *testing.T, e *Engine, id string) int64 {
	t.Helper()
	v, err := e.QueryAccount(id)
	if err != nil {
		t.Fatalf("QueryAccount(%s): %v", id, err)
	}
	return v.Balance
}

// 场景一：两笔奖励的整组冻结因其中一笔已确认而失败，
// 验证没有残留阻挡；重复结算，核对逐笔入账。
func TestFreezeFailsOnConfirmedMember(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "r1", AccountID: "acc1", Amount: 100, CompletedAt: base},
		{RewardID: "r2", AccountID: "acc1", Amount: 200, CompletedAt: base},
	})

	// 先确认 r1。
	e.Settle([]string{"r1"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r1"); got != StatusConfirmed {
		t.Fatalf("r1 status = %v, want confirmed", got)
	}

	// 整组冻结应失败，且不留案件、不留部分冻结。
	if err := e.Freeze("case1", []string{"r1", "r2"}); !errors.Is(err, ErrMemberFinalized) {
		t.Fatalf("Freeze err = %v, want ErrMemberFinalized", err)
	}
	if _, err := e.QueryCase("case1"); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("case1 should not exist, err = %v", err)
	}
	if v, _ := e.QueryReward("r2"); len(v.CaseIDs) != 0 {
		t.Fatalf("r2 should have no residual case link, got %v", v.CaseIDs)
	}

	// r2 未受阻挡，到期后可确认；重复结算不重复入账。
	now := base.Add(Cooldown)
	e.Settle([]string{"r2"}, now)
	e.Settle([]string{"r1", "r2"}, now.Add(time.Minute))

	if got := rewardStatus(t, e, "r2"); got != StatusConfirmed {
		t.Fatalf("r2 status = %v, want confirmed", got)
	}
	acct, err := e.QueryAccount("acc1")
	if err != nil {
		t.Fatalf("QueryAccount: %v", err)
	}
	if acct.Balance != 300 {
		t.Fatalf("balance = %d, want 300", acct.Balance)
	}
	if len(acct.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (exactly one per reward)", len(acct.Entries))
	}
	amounts := map[string]int64{}
	for _, en := range acct.Entries {
		amounts[en.RewardID] = en.Amount
	}
	if amounts["r1"] != 100 || amounts["r2"] != 200 {
		t.Fatalf("entries = %v, want r1=100 r2=200", amounts)
	}
}

// 场景二：案件 A={x,y}，案件 B={y,z}；拒绝 A、批准 B。
// y 不得被批准路径复活，z 仍须遵守冷却边界。
func TestOverlappingCasesRejectAndApprove(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "x", AccountID: "acc1", Amount: 10, CompletedAt: base},
		{RewardID: "y", AccountID: "acc2", Amount: 20, CompletedAt: base},
		{RewardID: "z", AccountID: "acc3", Amount: 30, CompletedAt: base},
	})
	if err := e.Freeze("A", []string{"x", "y"}); err != nil {
		t.Fatalf("Freeze A: %v", err)
	}
	if err := e.Freeze("B", []string{"y", "z"}); err != nil {
		t.Fatalf("Freeze B: %v", err)
	}

	if err := e.Resolve("A", DecisionReject); err != nil {
		t.Fatalf("Resolve A reject: %v", err)
	}
	if err := e.Resolve("B", DecisionApprove); err != nil {
		t.Fatalf("Resolve B approve: %v", err)
	}

	// x、y 永久拒绝；B 的批准不能复活 y。
	if got := rewardStatus(t, e, "x"); got != StatusRejected {
		t.Fatalf("x status = %v, want rejected", got)
	}
	if got := rewardStatus(t, e, "y"); got != StatusRejected {
		t.Fatalf("y status = %v, want rejected (must not be revived by B's approval)", got)
	}

	// 到期后结算：x、y 保持拒绝，z 确认入账。
	e.Settle([]string{"x", "y", "z"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "y"); got != StatusRejected {
		t.Fatalf("y status after settle = %v, want rejected", got)
	}
	if got := rewardStatus(t, e, "z"); got != StatusConfirmed {
		t.Fatalf("z status = %v, want confirmed", got)
	}
	if got := accountBalance(t, e, "acc3"); got != 30 {
		t.Fatalf("acc3 balance = %d, want 30", got)
	}
}

// 冷却边界：恰满 15 分钟算到期，差 1 秒不可确认。
func TestCooldownBoundary(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "r", AccountID: "acc", Amount: 50, CompletedAt: base},
	})

	e.Settle([]string{"r"}, base.Add(Cooldown).Add(-time.Second))
	if got := rewardStatus(t, e, "r"); got != StatusPending {
		t.Fatalf("before cooldown: status = %v, want pending", got)
	}
	if got := accountBalance(t, e, "acc"); got != 0 {
		t.Fatalf("before cooldown: balance = %d, want 0", got)
	}

	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusConfirmed {
		t.Fatalf("at exact cooldown: status = %v, want confirmed", got)
	}
	if got := accountBalance(t, e, "acc"); got != 50 {
		t.Fatalf("balance = %d, want 50", got)
	}
}

// 未决案件阻挡结算；批准后放行；重复同结论无副作用；相反结论冲突。
func TestResolveSemantics(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "r", AccountID: "acc", Amount: 70, CompletedAt: base},
	})
	if err := e.Freeze("c", []string{"r"}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusPending {
		t.Fatalf("pending case should block settle, status = %v", got)
	}

	if err := e.Resolve("c", DecisionApprove); err != nil {
		t.Fatalf("Resolve approve: %v", err)
	}
	// 同一结论重试无副作用。
	if err := e.Resolve("c", DecisionApprove); err != nil {
		t.Fatalf("retry same decision: %v", err)
	}
	// 相反结论返回冲突。
	if err := e.Resolve("c", DecisionReject); !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("opposite decision err = %v, want ErrDecisionConflict", err)
	}

	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusConfirmed {
		t.Fatalf("status = %v, want confirmed", got)
	}
}

// 拒绝结论不可更改，且拒绝后重试相同结论无副作用。
func TestRejectIsFinal(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "r", AccountID: "acc", Amount: 5, CompletedAt: base},
	})
	if err := e.Freeze("c", []string{"r"}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if err := e.Resolve("c", DecisionReject); err != nil {
		t.Fatalf("Resolve reject: %v", err)
	}
	if err := e.Resolve("c", DecisionReject); err != nil {
		t.Fatalf("retry reject: %v", err)
	}
	if err := e.Resolve("c", DecisionApprove); !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("approve after reject err = %v, want ErrDecisionConflict", err)
	}
	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusRejected {
		t.Fatalf("status = %v, want rejected", got)
	}
	if got := accountBalance(t, e, "acc"); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
}

// 多案件共同阻挡：任一未决都不可确认，全部批准后才放行。
func TestAllCasesMustApprove(t *testing.T) {
	e := mustNew(t, []RewardInput{
		{RewardID: "r", AccountID: "acc", Amount: 9, CompletedAt: base},
	})
	if err := e.Freeze("c1", []string{"r"}); err != nil {
		t.Fatalf("Freeze c1: %v", err)
	}
	if err := e.Freeze("c2", []string{"r"}); err != nil {
		t.Fatalf("Freeze c2: %v", err)
	}
	if err := e.Resolve("c1", DecisionApprove); err != nil {
		t.Fatalf("Resolve c1: %v", err)
	}
	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusPending {
		t.Fatalf("c2 still pending: status = %v, want pending", got)
	}
	if err := e.Resolve("c2", DecisionApprove); err != nil {
		t.Fatalf("Resolve c2: %v", err)
	}
	e.Settle([]string{"r"}, base.Add(Cooldown))
	if got := rewardStatus(t, e, "r"); got != StatusConfirmed {
		t.Fatalf("status = %v, want confirmed", got)
	}
}

// 构造校验：重复奖励 ID 与非正币额被拒绝。
func TestNewValidation(t *testing.T) {
	if _, err := New([]RewardInput{
		{RewardID: "r", AccountID: "a", Amount: 1, CompletedAt: base},
		{RewardID: "r", AccountID: "a", Amount: 1, CompletedAt: base},
	}); !errors.Is(err, ErrDuplicateReward) {
		t.Fatalf("dup err = %v, want ErrDuplicateReward", err)
	}
	if _, err := New([]RewardInput{
		{RewardID: "r", AccountID: "a", Amount: 0, CompletedAt: base},
	}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("amount err = %v, want ErrInvalidAmount", err)
	}
}

// 查询不存在的对象返回对应错误。
func TestQueryNotFound(t *testing.T) {
	e := mustNew(t, nil)
	if _, err := e.QueryReward("nope"); !errors.Is(err, ErrRewardNotFound) {
		t.Fatalf("err = %v, want ErrRewardNotFound", err)
	}
	if _, err := e.QueryCase("nope"); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("err = %v, want ErrCaseNotFound", err)
	}
	if _, err := e.QueryAccount("nope"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}
