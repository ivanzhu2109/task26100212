# rewardsettle

进程内的奖励冷却与风控复核结算库（纯内存，无外部依赖）。

## 规则

- 构造时载入一批已完成奖励（唯一 ID、账户、正整数币额、完成时间），初始均为待确认，账户余额为零。
- 奖励完成满 15 分钟（恰满算到期）且所有关联案件均已批准，才能确认入账；任一关联案件拒绝则永久拒绝。
- 每笔确认奖励恰好产生一条入账记录；账户余额恒等于该账户全部确认奖励币额之和。
- 时间由调用方显式传入，不依赖真实时钟。

## 使用

```go
e, _ := rewardsettle.New([]rewardsettle.RewardInput{
    {RewardID: "r1", AccountID: "acct-a", Amount: 100, CompletedAt: t0},
})

// 风控复核：原子冻结整组；任一成员已确认/已拒绝则整次失败。
_ = e.Freeze("case-1", []string{"r1"})
// 批准或拒绝；同结论重试幂等，相反结论返回 ErrDecisionConflict。
_ = e.Resolve("case-1", rewardsettle.DecisionApprove)

// 逐笔独立结算，返回本次成功确认的奖励 ID；未到期或仍冻结的保持待确认。
settled, _ := e.Settle([]string{"r1"}, t0.Add(15*time.Minute))

// 查询
r, entry, _ := e.QueryReward("r1")   // 奖励状态及其唯一入账记录（未确认为 nil）
c, _ := e.QueryCase("case-1")        // 案件状态与成员
view := e.QueryAccount("acct-a")     // 余额与全部入账记录
```

## 测试

```sh
go test ./...
```
