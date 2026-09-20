# obtop 代理感知会话排空 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将按用户固定快照批量 `KILL CONNECTION` 改为代理感知、有界复查的会话排空，在 ODP 场景优先关闭 Client Session，并以连续两次查询为零作为完成标准。

**Architecture:** 在现有 `Process` 模型中保留 `PROXY_SESSID`，由 `obclient` 负责按用户名精确查询、规划 ODP/直连目标并执行最多 5 轮排空。UI 只负责完整用户名确认、进度展示和取消；命令成功数与最终剩余会话数分开统计。

**Tech Stack:** Go 1.17、`database/sql`、`go-sql-driver/mysql`、tview/tcell、Go 标准库测试。

## Global Constraints

- 不在 `obtop` 中执行 `ALTER USER ... ACCOUNT LOCK/UNLOCK`。
- ODP 行优先探测 `KILL PROXYSESSION <ID>`；遇到 5010 `Unknown operator, bad internal cmd` 自动切换为旧版 `KILL <ID>`。`PROXY_SESSID` 只用于识别和去重同一代理客户端会话，不直接拼入命令。
- 直连行继续使用 `KILL CONNECTION <ID>`。
- 单轮最大并发 32，最多 5 轮，整体最长 30 秒，轮间等待 1 秒，最多处理 5000 个唯一命令目标。
- 连续两次按用户名查询结果为 0 才报告完成。
- 保留完整用户名确认；运行时允许 `Esc` 取消。
- MySQL/OceanBase 1094 视为目标已经消失；能力错误必须降级并明确展示，全局连接错误必须停止。
- 不新增第三方依赖，不修改单会话 `k` 键行为，不做无关重构。

> 现场兼容性修订：实际 ODP 返回 5010，验证了旧版客户端关闭语法仍在使用。实现时
> 必须在首个 ODP 目标上完成新版到旧版的单次探测切换，再并发处理剩余目标。

---

## 文件结构

- `internal/model/process.go`：会话快照增加代理会话标识。
- `internal/model/kill.go`：定义排空进度、结果、停止原因和失败详情。
- `internal/obclient/drain.go`：新增纯排空编排和目标规划，保持 `client.go` 聚焦数据库访问。
- `internal/obclient/drain_test.go`：覆盖目标分组、多轮核验、降级、上限和取消。
- `internal/obclient/client.go`：读取 `PROXY_SESSID`，实现精确用户查询和两类 KILL 命令。
- `internal/ui/app.go`：接入排空接口、状态更新和取消。
- `internal/ui/app_test.go`：覆盖确认、进度、完成/未完成提示和 `Esc`。
- `README.md`：更新行为、权限和运维前置条件。

### Task 1: 采集并保留 ODP 会话标识

**Files:**
- Modify: `internal/model/process.go`
- Modify: `internal/obclient/client.go`
- Modify: `internal/model/process_test.go`

**Interfaces:**
- Produces: `model.Process.ProxySessID int64`
- Consumes: `oceanbase.GV$OB_PROCESSLIST.PROXY_SESSID`

- [ ] **Step 1: 写失败测试，证明模型可区分 ODP 和直连会话**

在 `internal/model/process_test.go` 增加：

```go
func TestProcessIdentifiesProxySession(t *testing.T) {
	proxy := Process{SessID: 101, ProxySessID: 9001}
	direct := Process{SessID: 102}

	if proxy.ProxySessID <= 0 {
		t.Fatalf("proxy session id = %d, want positive", proxy.ProxySessID)
	}
	if direct.ProxySessID != 0 {
		t.Fatalf("direct proxy session id = %d, want zero", direct.ProxySessID)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/model -run TestProcessIdentifiesProxySession -v
```

Expected: FAIL，提示 `Process` 没有 `ProxySessID` 字段。

- [ ] **Step 3: 最小实现字段和查询扫描**

在 `model.Process` 中将字段放在 `SessID` 后：

```go
type Process struct {
	SessID      int64
	ProxySessID int64
	ServerIP    string
	// existing fields stay unchanged
}
```

在 `activeSQLQuery` 的 `ID` 后增加 `PROXY_SESSID`。在 `FetchProcesses` 中增加：

```go
var proxySessID sql.NullInt64
```

并在 `rows.Scan` 中紧跟 `sessID` 扫描 `&proxySessID`，随后赋值：

```go
proc.SessID = sessID.Int64
proc.ProxySessID = proxySessID.Int64
```

- [ ] **Step 4: 运行模型测试和全量编译测试**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/model ./internal/obclient
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/model/process.go internal/model/process_test.go internal/obclient/client.go
git commit -m "feat: 采集 ODP 代理会话标识"
```

### Task 2: 定义排空结果并规划代理/直连目标

**Files:**
- Modify: `internal/model/kill.go`
- Create: `internal/obclient/drain.go`
- Create: `internal/obclient/drain_test.go`

**Interfaces:**
- Produces: `model.SessionDrainStopReason`
- Produces: `model.SessionDrainProgress`
- Produces: `model.SessionDrainFailure`
- Produces: `model.SessionDrainResult`
- Produces: `planSessionDrainTargets([]model.Process) drainTargets`
- Consumes: `model.Process.SessID` and `model.Process.ProxySessID`

- [ ] **Step 1: 写目标分组失败测试**

在 `internal/obclient/drain_test.go` 创建测试，要求相同 `PROXY_SESSID` 只保留一个
ODP 关闭目标，直连 ID 独立去重：

```go
func TestPlanSessionDrainTargetsGroupsProxySessions(t *testing.T) {
	rows := []model.Process{
		{SessID: 101, ProxySessID: 9001, User: "app"},
		{SessID: 102, ProxySessID: 9001, User: "app"},
		{SessID: 201, User: "app"},
		{SessID: 201, User: "app"},
		{SessID: 0, ProxySessID: 9002, User: "app"},
	}

	got := planSessionDrainTargets(rows)
	if !reflect.DeepEqual(got.proxyConnectionIDs, []int64{101}) {
		t.Fatalf("proxy targets = %#v, want [101]", got.proxyConnectionIDs)
	}
	if !reflect.DeepEqual(got.directConnectionIDs, []int64{201}) {
		t.Fatalf("direct targets = %#v, want [201]", got.directConnectionIDs)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -run TestPlanSessionDrainTargetsGroupsProxySessions -v
```

Expected: FAIL，提示 `planSessionDrainTargets` 未定义。

- [ ] **Step 3: 定义排空结果类型**

在 `internal/model/kill.go` 保留现有 `BatchKillResult`，并增加：

```go
type SessionDrainStopReason string

const (
	SessionDrainCompleted   SessionDrainStopReason = "completed"
	SessionDrainRoundLimit  SessionDrainStopReason = "round-limit"
	SessionDrainTargetLimit SessionDrainStopReason = "target-limit"
	SessionDrainTimeout     SessionDrainStopReason = "timeout"
	SessionDrainCancelled   SessionDrainStopReason = "cancelled"
	SessionDrainFatalError  SessionDrainStopReason = "fatal-error"
)

type SessionDrainProgress struct {
	Round         int
	MaxRounds     int
	Initial       int
	Current       int
	ProxyTargets  int
	DirectTargets int
}

type SessionDrainFailure struct {
	Command   string
	SessionID int64
	Err       error
}

type SessionDrainResult struct {
	User               string
	Rounds             int
	Initial            int
	Remaining          int
	ProxyCommands      int
	ConnectionCommands int
	AlreadyGone        int
	RebuiltIDs         int
	NewProxySessions   int
	Degraded           bool
	StopReason         SessionDrainStopReason
	Failures           []SessionDrainFailure
}
```

- [ ] **Step 4: 实现纯目标规划**

在 `internal/obclient/drain.go` 增加：

```go
const (
	maxSessionDrainRounds  = 5
	maxSessionDrainTargets = 5000
	sessionDrainInterval   = time.Second
)

type drainTargets struct {
	proxyConnectionIDs  []int64
	directConnectionIDs []int64
}

func planSessionDrainTargets(rows []model.Process) drainTargets {
	seenProxy := make(map[int64]struct{})
	seenDirect := make(map[int64]struct{})
	var targets drainTargets
	for _, row := range rows {
		if row.SessID <= 0 {
			continue
		}
		if row.ProxySessID > 0 {
			if _, ok := seenProxy[row.ProxySessID]; ok {
				continue
			}
			seenProxy[row.ProxySessID] = struct{}{}
			targets.proxyConnectionIDs = append(targets.proxyConnectionIDs, row.SessID)
			continue
		}
		if _, ok := seenDirect[row.SessID]; ok {
			continue
		}
		seenDirect[row.SessID] = struct{}{}
		targets.directConnectionIDs = append(targets.directConnectionIDs, row.SessID)
	}
	return targets
}
```

- [ ] **Step 5: 运行目标规划测试**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -run TestPlanSessionDrainTargetsGroupsProxySessions -v
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/model/kill.go internal/obclient/drain.go internal/obclient/drain_test.go
git commit -m "feat: 定义会话排空目标和结果"
```

### Task 3: 实现有界多轮排空编排

**Files:**
- Modify: `internal/obclient/drain.go`
- Modify: `internal/obclient/drain_test.go`

**Interfaces:**
- Produces: `runUserSessionDrain(context.Context, string, sessionDrainOps, func(model.SessionDrainProgress)) (model.SessionDrainResult, error)`
- Consumes: Task 2 的 `planSessionDrainTargets` 和结果类型

- [ ] **Step 1: 写“连续两次为零”失败测试**

定义可注入操作：

```go
type sessionDrainOps struct {
	fetch          func(context.Context, string) ([]model.Process, error)
	killProxy      func(context.Context, int64) error
	killConnection func(context.Context, int64) error
	wait           func(context.Context, time.Duration) error
}
```

测试快照依次为 1 条、0 条、0 条；断言只在第二个空快照后完成：

```go
func TestRunUserSessionDrainRequiresTwoEmptySnapshots(t *testing.T) {
	snapshots := [][]model.Process{
		{{SessID: 101, User: "app"}},
		nil,
		nil,
	}
	fetches := 0
	ops := sessionDrainOps{
		fetch: func(context.Context, string) ([]model.Process, error) {
			rows := snapshots[fetches]
			fetches++
			return rows, nil
		},
		killConnection: func(context.Context, int64) error { return nil },
		killProxy:      func(context.Context, int64) error { return nil },
		wait:           func(context.Context, time.Duration) error { return nil },
	}

	result, err := runUserSessionDrain(context.Background(), "app", ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != model.SessionDrainCompleted || result.Remaining != 0 {
		t.Fatalf("result = %#v, want completed with zero remaining", result)
	}
	if fetches != 3 {
		t.Fatalf("fetches = %d, want 3", fetches)
	}
}
```

- [ ] **Step 2: 写代理重建和新代理会话失败测试**

构造快照：

```go
[]model.Process{{SessID: 101, ProxySessID: 9001, User: "app"}}
[]model.Process{
	{SessID: 102, ProxySessID: 9001, User: "app"},
	{SessID: 103, ProxySessID: 9002, User: "app"},
}
nil
nil
```

断言最终 `RebuiltIDs == 1`、`NewProxySessions == 1`，且代理关闭函数接收到
`101`、随后接收到 `102` 和 `103`。

- [ ] **Step 3: 写能力降级、1094 和致命错误失败测试**

分别验证：

```go
&mysql.MySQLError{Number: 1227, Message: "access denied"} // proxy probe -> Degraded
&mysql.MySQLError{Number: 1094, Message: "unknown thread"} // AlreadyGone++
driver.ErrBadConn // StopReason == fatal-error
```

降级后下一轮应把 ODP 行的 `ID` 交给 `killConnection`，且结果中
`Degraded == true`、首条失败保留命令名 `KILL PROXYSESSION`。

- [ ] **Step 4: 写边界和取消失败测试**

使用无等待的 `wait` 假实现分别验证：

- 始终返回同一会话时，最多执行 5 轮并返回 `round-limit`；
- 构造超过 5000 个唯一目标时返回 `target-limit`；
- 已取消 context 返回 `cancelled`；
- `context.DeadlineExceeded` 返回 `timeout`。

- [ ] **Step 5: 运行新增测试确认失败**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -run 'TestRunUserSessionDrain' -v
```

Expected: FAIL，提示排空编排尚未实现。

- [ ] **Step 6: 实现错误分类和等待**

在 `drain.go` 增加：

```go
func waitForDrain(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isProxyCapabilityError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1044, 1045, 1064, 1227:
		return true
	default:
		return false
	}
}

func isFatalSessionKillError(err error) bool {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) &&
		(mysqlErr.Number == 2006 || mysqlErr.Number == 2013)
}
```

- [ ] **Step 7: 实现多轮状态机**

实现 `runUserSessionDrain`，遵守以下顺序：

1. 校验 `strings.TrimSpace(user) != ""`；
2. 首次 `fetch`，过滤 `row.User == user`，记录 `Initial`；
3. 每轮调用 `planSessionDrainTargets` 并通过回调报告进度；
4. ODP 目标首次串行探测，成功后使用现有 `runBatchSessionKills` 并发；
5. 能力错误设置 `Degraded`，下一轮把 ODP ID 合并到 connection 目标；
6. 每执行一个新的 `命令类型/ID` 键前检查 5000 上限；
7. 一轮完成后等待并重新 `fetch`；
8. 对比前后 `ID -> PROXY_SESSID` 集合，累计重建和新代理会话；
9. 第一次查询为零后再等待和查询一次，第二次仍为零才设置 `completed`；
10. context 停止时区分 `cancelled` 和 `timeout`；
11. 五轮结束仍有会话则设置 `round-limit`；
12. 返回时始终填充 `Remaining` 和首条失败详情。

目标级并发复用 `runBatchSessionKills`，但增加一个内部适配函数把
`BatchKillResult.Failures` 转成带命令名的 `SessionDrainFailure`，不要复制并发
worker 实现。

- [ ] **Step 8: 运行排空测试和竞态检查**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -run 'TestRunUserSessionDrain|TestPlanSessionDrainTargets' -v
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test -race ./internal/obclient
```

Expected: PASS；没有 data race。

- [ ] **Step 9: 提交**

```bash
git add internal/obclient/drain.go internal/obclient/drain_test.go
git commit -m "feat: 实现有界会话排空编排"
```

### Task 4: 接入真实 OceanBase/ODP 命令

**Files:**
- Modify: `internal/obclient/client.go`
- Modify: `internal/obclient/drain_test.go`

**Interfaces:**
- Produces: `(*Client).DrainUserSessions(context.Context, string, func(model.SessionDrainProgress)) (model.SessionDrainResult, error)`
- Consumes: Task 3 的 `runUserSessionDrain`

- [ ] **Step 1: 写 SQL 生成失败测试**

在 `drain_test.go` 增加纯函数测试：

```go
func TestSessionKillStatementsUseConnectionID(t *testing.T) {
	if got := proxySessionKillStatement(101); got != "KILL PROXYSESSION 101" {
		t.Fatalf("proxy statement = %q", got)
	}
	if got := connectionKillStatement(202); got != "KILL CONNECTION 202" {
		t.Fatalf("connection statement = %q", got)
	}
}
```

同时测试非正数 ID 返回空字符串，防止异常值拼接。

- [ ] **Step 2: 运行测试确认失败**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -run TestSessionKillStatementsUseConnectionID -v
```

Expected: FAIL，提示 SQL 生成函数未定义。

- [ ] **Step 3: 实现精确用户查询**

在 `client.go` 增加常量：

```go
const userSessionsQuery = `
SELECT ID, PROXY_SESSID, USER, HOST
FROM oceanbase.GV$OB_PROCESSLIST
WHERE USER = ?
`
```

增加私有方法：

```go
func (c *Client) fetchUserSessions(ctx context.Context, user string) ([]model.Process, error)
```

要求：

- 使用 5 秒子超时；
- 使用参数绑定用户名；
- 扫描 `sql.NullInt64` 和 `sql.NullString`；
- 只保留 `strings.TrimSpace(proc.User) == user`；
- 查询、扫描、迭代错误分别包装上下文。

- [ ] **Step 4: 实现两类关闭命令和公共排空入口**

增加：

```go
func proxySessionKillStatement(sessionID int64) string
func connectionKillStatement(sessionID int64) string
```

只接受正整数并用 `strconv.FormatInt` 生成语句。

公共入口：

```go
func (c *Client) DrainUserSessions(
	ctx context.Context,
	user string,
	report func(model.SessionDrainProgress),
) (model.SessionDrainResult, error)
```

入口创建独立 `sql.DB`，最多 32 个连接，随后把以下闭包传给
`runUserSessionDrain`：

```go
sessionDrainOps{
	fetch: c.fetchUserSessions,
	killProxy: func(ctx context.Context, id int64) error {
		return execSessionKill(ctx, killDB, proxySessionKillStatement(id), id)
	},
	killConnection: func(ctx context.Context, id int64) error {
		return execSessionKill(ctx, killDB, connectionKillStatement(id), id)
	},
	wait: waitForDrain,
}
```

每条 KILL 使用 5 秒子超时。`DrainUserSessions` 不再接受 UI 快照 ID，确保每轮
都从数据库重新读取目标用户。

- [ ] **Step 5: 保留旧内部批量 helper，移除未使用公共方法**

`runBatchSessionKills` 继续作为并发 worker 使用。确认 UI 切换完成后删除不再被
调用的 `(*Client).KillSessions` 公共方法；不要删除单会话 `KillSession`。

- [ ] **Step 6: 运行 obclient 全量测试**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/obclient -v
```

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/obclient/client.go internal/obclient/drain_test.go
git commit -m "feat: 接入 ODP 客户端会话关闭"
```

### Task 5: 接入 UI 进度和取消

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/app_test.go`

**Interfaces:**
- Consumes: `DrainUserSessions(context.Context, string, func(model.SessionDrainProgress))`
- Produces: `App.cancelSessionDrain context.CancelFunc`

- [ ] **Step 1: 改写接口并写确认流程失败测试**

将 `SessionActions` 改为：

```go
type SessionActions interface {
	KillSession(context.Context, int64) error
	DrainUserSessions(
		context.Context,
		string,
		func(model.SessionDrainProgress),
	) (model.SessionDrainResult, error)
	ExplainSession(context.Context, int64) (string, error)
}
```

修改 `TestStatsViewStartsBatchKillConfirmationForSelectedUser`，断言确认提示仍展示
当前可见数量，但 `App` 不再保存 `pendingKillIDs`，只保存精确用户名。

- [ ] **Step 2: 写运行进度和取消失败测试**

增加 fake actions：

```go
type blockingDrainActions struct {
	started chan string
	done    chan struct{}
}

func (f *blockingDrainActions) KillSession(context.Context, int64) error {
	return nil
}

func (f *blockingDrainActions) ExplainSession(context.Context, int64) (string, error) {
	return "", nil
}

func (f *blockingDrainActions) DrainUserSessions(
	ctx context.Context,
	user string,
	report func(model.SessionDrainProgress),
) (model.SessionDrainResult, error) {
	f.started <- user
	report(model.SessionDrainProgress{
		Round: 1, MaxRounds: 5, Initial: 74, Current: 12,
		ProxyTargets: 10, DirectTargets: 2,
	})
	<-ctx.Done()
	close(f.done)
	return model.SessionDrainResult{
		User: user, Initial: 74, Remaining: 12,
		StopReason: model.SessionDrainCancelled,
	}, nil
}
```

测试要求：

- 提交完整用户名后进入 `modeKillUserRun`；
- 进度文本包含 `round 1/5` 和 `current 12`；
- `Esc` 调用保存的 cancel function；
- 结束后恢复刷新并清空 cancel function。

- [ ] **Step 3: 写完成和未完成状态失败测试**

替换现有 `finishBatchKill` 测试，覆盖：

```go
model.SessionDrainResult{
	User: "app", Initial: 74, Remaining: 0,
	Rounds: 2, StopReason: model.SessionDrainCompleted,
}
```

状态包含：

```text
drain user app complete: 74 -> 0 sessions, 2 rounds
```

再覆盖 `round-limit`、`Degraded == true`、`RebuiltIDs > 0`、
`NewProxySessions > 0` 和首条 `KILL PROXYSESSION` 权限错误，确保未完成提示能区分
后端 ID 重建和新代理会话，且刷新后错误详情仍保留。

- [ ] **Step 4: 运行 UI 测试确认失败**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/ui -run 'TestStatsView|TestSessionDrain|TestDrain' -v
```

Expected: FAIL，提示新接口、取消字段和完成函数尚未实现。

- [ ] **Step 5: 实现 UI 接入**

在 `App` 中：

```go
cancelSessionDrain context.CancelFunc
```

确认后创建整体 30 秒 context：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
a.cancelSessionDrain = cancel
```

调用：

```go
result, drainErr := a.actions.DrainUserSessions(ctx, user, func(progress model.SessionDrainProgress) {
	a.app.QueueUpdateDraw(func() {
		a.setStatusNote(fmt.Sprintf(
			"draining user %s: round %d/%d, current %d, proxy %d, direct %d...",
			user, progress.Round, progress.MaxRounds, progress.Current,
			progress.ProxyTargets, progress.DirectTargets,
		), "yellow")
		a.updateStatus(nil)
	})
})
```

新增 `finishSessionDrain`，按 `StopReason` 和 `Remaining` 生成成功、取消、超时、
达到上限或致命错误文案；未完成时附加降级、后端 ID 重建和新代理会话摘要。始终
调用 `resumeRefresh()`，并在 `monitor != nil` 时调用
`monitor.TriggerRefresh()`，同时保留首条失败详情。

- [ ] **Step 6: 在运行模式处理 Esc**

在 `handleKeys` 中将 `modeKillUserRun` 单独处理：

```go
case modeKillUserRun:
	if event.Key() == tcell.KeyEsc && a.cancelSessionDrain != nil {
		a.cancelSessionDrain()
		a.setStatusNote("cancelling session drain...", "yellow")
		a.updateStatus(nil)
	}
	return nil
```

运行期间其他按键继续被吞掉，避免切换页面或重复启动。

- [ ] **Step 7: 清理旧快照状态**

删除：

```go
pendingKillIDs []int64
```

删除旧 `finishBatchKill`，把 `modeKillUserInput` 的 goroutine 调用切换到
`DrainUserSessions`。确认提示中的数量仍由 `KillSessionIDsByUser(a.rows, user)`
计算，仅用于向操作者展示，不作为执行目标。

- [ ] **Step 8: 运行 UI 测试**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./internal/ui -v
```

Expected: PASS。

- [ ] **Step 9: 提交**

```bash
git add internal/ui/app.go internal/ui/app_test.go
git commit -m "feat: 展示并支持取消会话排空"
```

### Task 6: 更新说明并完成全量验证

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: 前五个任务形成的最终行为
- Produces: 可交付的文档、测试结果和本机构建产物

- [ ] **Step 1: 更新功能和查询字段**

将“按用户批量并发关闭确认时快照”改为“代理感知、有界复查的按用户会话排空”。
查询示例在 `ID` 后加入 `PROXY_SESSID`。

- [ ] **Step 2: 重写 Kill 说明**

明确写入：

- 运维前置条件是操作者已锁定目标账号；
- `obtop` 不锁定、不解锁账号；
- ODP 会话优先执行 `KILL PROXYSESSION <ID>`；
- 直连和代理能力降级使用 `KILL CONNECTION <ID>`；
- 最多 5 轮/30 秒/5000 个唯一目标，单轮最多 32 并发；
- 连续两次查询为零才完成；
- `Esc` 可取消；
- ODP 命令需要相应权限，降级不代表代理 Client Session 已关闭。

- [ ] **Step 3: 运行格式化和静态检查**

Run:

```bash
gofmt -w internal/model/process.go internal/model/kill.go internal/obclient/client.go internal/obclient/batch_kill.go internal/obclient/drain.go internal/obclient/batch_kill_test.go internal/obclient/drain_test.go internal/model/process_test.go internal/ui/app.go internal/ui/app_test.go
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go vet ./...
```

Expected: `gofmt` 无剩余差异；`go vet` PASS。

- [ ] **Step 4: 运行全量测试和竞态测试**

Run:

```bash
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test -race ./...
```

Expected: 全部 PASS。

- [ ] **Step 5: 使用仓库原生入口构建**

Run:

```bash
CGO_ENABLED=0 GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go build -o obtop ./cmd/obtop
file obtop
./obtop -h
```

Expected:

- 构建退出码为 0；
- `file obtop` 显示当前本机架构的可执行文件；
- `./obtop -h` 正常展示帮助并退出。

- [ ] **Step 6: 检查改动边界**

Run:

```bash
git diff --check
git status --short
git diff --stat HEAD
```

Expected: 只有计划列出的实现、测试、README 和本地未追踪构建产物；提交时不暂存
`obtop` 二进制和 `.gocache/.gomodcache`。

- [ ] **Step 7: 提交文档和最终整理**

```bash
git add README.md
git commit -m "docs: 更新会话排空使用说明"
```

- [ ] **Step 8: 最终验收记录**

交付时明确列出：

- 修改内容；
- 自动测试、竞态测试和构建结果；
- 未在真实 ODP/OBServer 环境执行 KILL 的事实边界；
- `KILL PROXYSESSION` 权限和版本兼容风险；
- 回滚到设计提交前一个实现提交的方法，不执行自动回滚。
