package rewardsettle

import (
	"errors"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func mustNew(t *testing.T, inputs ...RewardInput) *Engine {
	t.Helper()
	e, err := New(inputs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func input(id, account string, amount int64, completedAt time.Time) RewardInput {
	return RewardInput{RewardID: id, AccountID: account, Amount: amount, CompletedAt: completedAt}
}

// 场景一：两笔奖励的整组冻结因其中一笔已确认而失败，
// 验证没有残留阻挡；重复结算，核对逐笔入账。
func TestFreezeFailsWhenMemberConfirmed(t *testing.T) {
	e := mustNew(t,
		input("r1", "acct-a", 100, base),
		input("r2", "acct-a", 200, base),
	)

	after := base.Add(Cooldown) // 恰满 15 分钟
	settled, err := e.Settle([]string{"r1"}, after)
	if err != nil {
		t.Fatalf("Settle r1: %v", err)
	}
	if len(settled) != 1 || settled[0] != "r1" {
		t.Fatalf("settled = %v, want [r1]", settled)
	}

	// 整组冻结：r1 已确认，整次失败。
	if err := e.Freeze("case-1", []string{"r1", "r2"}); !errors.Is(err, ErrMemberFinalized) {
		t.Fatalf("Freeze err = %v, want ErrMemberFinalized", err)
	}

	// 不留案件。
	if _, err := e.QueryCase("case-1"); !errors.Is(err, ErrUnknownCase) {
		t.Fatalf("QueryCase err = %v, want ErrUnknownCase", err)
	}
	// 不留部分冻结：r2 无任何关联案件。
	r2, _, err := e.QueryReward("r2")
	if err != nil {
		t.Fatalf("QueryReward r2: %v", err)
	}
	if len(r2.CaseIDs) != 0 {
		t.Fatalf("r2.CaseIDs = %v, want empty", r2.CaseIDs)
	}
	if r2.Status != StatusPending {
		t.Fatalf("r2.Status = %v, want pending", r2.Status)
	}

	// 重复结算：r1 不重复入账，r2 首次入账、之后幂等。
	for i := 0; i < 3; i++ {
		settled, err = e.Settle([]string{"r1", "r2"}, after.Add(time.Minute))
		if err != nil {
			t.Fatalf("Settle retry %d: %v", i, err)
		}
		want := 0
		if i == 0 {
			want = 1
		}
		if len(settled) != want {
			t.Fatalf("retry %d settled = %v, want len %d", i, settled, want)
		}
	}

	// 逐笔核对入账记录：每笔确认奖励恰一条。
	r1, e1, err := e.QueryReward("r1")
	if err != nil || r1.Status != StatusConfirmed {
		t.Fatalf("r1 = %v, %v", r1.Status, err)
	}
	if e1 == nil || e1.Amount != 100 || e1.AccountID != "acct-a" {
		t.Fatalf("r1 entry = %+v", e1)
	}
	_, e2, err := e.QueryReward("r2")
	if err != nil || e2 == nil || e2.Amount != 200 {
		t.Fatalf("r2 entry = %+v, %v", e2, err)
	}

	// 账户余额 = 全部确认奖励币额之和，入账记录恰两条。
	view := e.QueryAccount("acct-a")
	if view.Balance != 300 {
		t.Fatalf("balance = %d, want 300", view.Balance)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(view.Entries))
	}
}

// 场景二：案件 A={x,y}，案件 B={y,z}，拒绝 A、批准 B；
// y 不得被批准路径复活，z 仍须遵守冷却边界。
func TestCrossCasesRejectThenApprove(t *testing.T) {
	e := mustNew(t,
		input("x", "acct-a", 10, base),
		input("y", "acct-a", 20, base),
		input("z", "acct-b", 30, base),
	)
	if err := e.Freeze("caseA", []string{"x", "y"}); err != nil {
		t.Fatalf("Freeze caseA: %v", err)
	}
	if err := e.Freeze("caseB", []string{"y", "z"}); err != nil {
		t.Fatalf("Freeze caseB: %v", err)
	}

	if err := e.Resolve("caseA", DecisionReject); err != nil {
		t.Fatalf("Resolve caseA: %v", err)
	}
	if err := e.Resolve("caseB", DecisionApprove); err != nil {
		t.Fatalf("Resolve caseB: %v", err)
	}

	// y 被 caseA 拒绝后，caseB 的批准不得将其复活。
	y, yEntry, err := e.QueryReward("y")
	if err != nil {
		t.Fatalf("QueryReward y: %v", err)
	}
	if y.Status != StatusRejected {
		t.Fatalf("y.Status = %v, want rejected", y.Status)
	}
	if yEntry != nil {
		t.Fatalf("y entry = %+v, want nil", yEntry)
	}
	x, _, _ := e.QueryReward("x")
	if x.Status != StatusRejected {
		t.Fatalf("x.Status = %v, want rejected", x.Status)
	}

	// z 的所有案件均已批准，但冷却边界仍须遵守。
	before := base.Add(Cooldown - time.Second)
	settled, err := e.Settle([]string{"z"}, before)
	if err != nil {
		t.Fatalf("Settle z early: %v", err)
	}
	if len(settled) != 0 {
		t.Fatalf("settled = %v, want empty (cooldown not reached)", settled)
	}
	z, _, _ := e.QueryReward("z")
	if z.Status != StatusPending {
		t.Fatalf("z.Status = %v, want pending", z.Status)
	}

	// 恰满 15 分钟到期，可确认。
	settled, err = e.Settle([]string{"z"}, base.Add(Cooldown))
	if err != nil {
		t.Fatalf("Settle z at boundary: %v", err)
	}
	if len(settled) != 1 || settled[0] != "z" {
		t.Fatalf("settled = %v, want [z]", settled)
	}
	if got := e.QueryAccount("acct-b").Balance; got != 30 {
		t.Fatalf("acct-b balance = %d, want 30", got)
	}
	// 被拒的 x、y 永不到账。
	if got := e.QueryAccount("acct-a").Balance; got != 0 {
		t.Fatalf("acct-a balance = %d, want 0", got)
	}
}

// 同一结论重试无副作用，相反结论返回冲突。
func TestResolveIdempotentAndConflict(t *testing.T) {
	e := mustNew(t, input("r1", "acct-a", 5, base))
	if err := e.Freeze("c1", []string{"r1"}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if err := e.Resolve("c1", DecisionApprove); err != nil {
		t.Fatalf("Resolve approve: %v", err)
	}
	if err := e.Resolve("c1", DecisionApprove); err != nil {
		t.Fatalf("Resolve approve retry: %v", err)
	}
	if err := e.Resolve("c1", DecisionReject); !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("Resolve reject err = %v, want ErrDecisionConflict", err)
	}
	c, err := e.QueryCase("c1")
	if err != nil || c.Status != CaseApproved {
		t.Fatalf("case = %+v, %v", c, err)
	}

	if err := e.Resolve("missing", DecisionApprove); !errors.Is(err, ErrUnknownCase) {
		t.Fatalf("Resolve missing err = %v, want ErrUnknownCase", err)
	}
	if err := e.Resolve("c1", Decision("bogus")); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("Resolve bogus err = %v, want ErrInvalidDecision", err)
	}
}

// 无案件奖励到期即确认；未决案件阻挡结算，批准后放行。
func TestSettleWithoutCaseAndPendingBlocks(t *testing.T) {
	e := mustNew(t,
		input("free", "acct-a", 7, base),
		input("held", "acct-a", 9, base),
	)
	if err := e.Freeze("c1", []string{"held"}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	after := base.Add(Cooldown)
	settled, err := e.Settle([]string{"free", "held"}, after)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(settled) != 1 || settled[0] != "free" {
		t.Fatalf("settled = %v, want [free]", settled)
	}
	if err := e.Resolve("c1", DecisionApprove); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	settled, err = e.Settle([]string{"held"}, after)
	if err != nil {
		t.Fatalf("Settle held: %v", err)
	}
	if len(settled) != 1 || settled[0] != "held" {
		t.Fatalf("settled = %v, want [held]", settled)
	}
	if got := e.QueryAccount("acct-a").Balance; got != 16 {
		t.Fatalf("balance = %d, want 16", got)
	}
}

// 构造校验与冻结的未知成员/重复案件。
func TestConstructorAndFreezeValidation(t *testing.T) {
	if _, err := New([]RewardInput{input("a", "acct", 1, base), input("a", "acct", 1, base)}); !errors.Is(err, ErrDuplicateRewardID) {
		t.Fatalf("dup reward err = %v", err)
	}
	if _, err := New([]RewardInput{input("a", "acct", 0, base)}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero amount err = %v", err)
	}
	e := mustNew(t, input("a", "acct", 1, base))
	if err := e.Freeze("c1", []string{"a"}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if err := e.Freeze("c1", []string{"a"}); !errors.Is(err, ErrDuplicateCaseID) {
		t.Fatalf("dup case err = %v", err)
	}
	if err := e.Freeze("c2", []string{"nope"}); !errors.Is(err, ErrUnknownReward) {
		t.Fatalf("unknown member err = %v", err)
	}
	if _, err := e.QueryCase("c2"); !errors.Is(err, ErrUnknownCase) {
		t.Fatalf("c2 should not exist, err = %v", err)
	}
	if _, err := e.Settle([]string{"nope"}, base); !errors.Is(err, ErrUnknownReward) {
		t.Fatalf("settle unknown err = %v", err)
	}
}
