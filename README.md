# rewardsettle

进程内奖励结算库（Go）：奖励冷却（15 分钟）+ 风控案件复核，全部数据在内存中，时间显式传入。

## 模型

- 构造 `New([]RewardInput)` 载入已完成奖励：唯一奖励 ID、账户 ID、正整数币额、完成时间；初始均为待确认，账户余额为零。
- 奖励确认条件：完成满 15 分钟（恰好满也算到期）且所有关联案件均已批准；不存在案件也满足条件。
- 任一关联案件被拒绝后，该奖励永久拒绝。
- 每笔确认奖励恰好产生一条入账记录；账户余额 = 该账户全部确认奖励币额之和。

## API

- `Freeze(caseID, rewardIDs)`：创建未决案件并原子冻结整组。成员可跨账户、可与其他案件重叠；只要有一笔已确认/已拒绝，整次失败且不留残留。
- `Resolve(caseID, decision)`：批准或拒绝。批准只解除本案件阻挡；拒绝使所有未终结成员永久拒绝，整组原子可见。同一结论重试无副作用，相反结论返回 `ErrDecisionConflict`。
- `Settle(rewardIDs, now)`：逐笔独立提交（不整批原子）；资格检查、状态变更、记账在同一临界区完成。重复调用不重复入账；未到期或仍被冻结的保持待确认，可稍后重试。
- `QueryReward / QueryCase / QueryAccount`：查询奖励、案件、账户状态及入账记录。

## 使用示例

```go
e, _ := rewardsettle.New([]rewardsettle.RewardInput{
    {RewardID: "r1", AccountID: "acc1", Amount: 100, CompletedAt: t0},
    {RewardID: "r2", AccountID: "acc1", Amount: 200, CompletedAt: t0},
})

_ = e.Freeze("case1", []string{"r1", "r2"})        // 风控冻结
_ = e.Resolve("case1", rewardsettle.DecisionApprove) // 复核批准

now := t0.Add(15 * time.Minute)
e.Settle([]string{"r1", "r2"}, now)                // 到期确认入账

acct, _ := e.QueryAccount("acc1")                  // acct.Balance == 300
```

## 测试

```sh
go test ./...
```
