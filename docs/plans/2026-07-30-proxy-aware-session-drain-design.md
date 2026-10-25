# obtop 代理感知会话排空设计

## 背景

`obtop` 当前在会话统计页按用户执行批量关闭时，会固定确认时的
`GV$OB_PROCESSLIST.ID` 快照，然后并发执行 `KILL CONNECTION <ID>`。
命令返回成功或目标已经消失后，程序即把该 ID 计为已清理。

实际运维流程会先锁定待清理账号，再通过 `obtop` 关闭存量连接。现场仍可能观察到：

- 一部分会话长期不消失；
- 反复执行批量关闭后，`GV$OB_PROCESSLIST.ID` 发生变化；
- 状态栏显示命令成功，但用户会话数没有归零。

OceanBase 通过 ODP/OBProxy 接入时，数据库后端 Server Session 与代理侧 Client
Session 并非同一层会话。账号锁定只能阻止新的认证登录，不能主动关闭锁定前已经
认证成功的 ODP Client Session。只关闭 Server Session 后，仍存活的 Client
Session 可能重新建立 Server Session，从而表现为 `ID` 变化。

## 目标

在不负责账号锁定和解锁的前提下，让 `obtop` 能够：

1. 区分 ODP 会话和直连 OBServer 会话；
2. ODP 会话优先关闭 Client Session，直连会话继续关闭数据库连接；
3. 每轮关闭后重新查询目标用户，按真实剩余会话决定是否完成；
4. 对权限不足、命令不兼容和会话重建给出可判断的结果；
5. 设置明确的轮数、时间和操作数量上限，避免无限执行。

## 非目标

- 不在 `obtop` 中执行 `ALTER USER ... ACCOUNT LOCK/UNLOCK`；
- 不自动停止应用或修改连接池配置；
- 不尝试绕过 OceanBase/ODP 的权限检查；
- 不保证在账号未正确锁定或客户端仍可重新认证时强制归零；
- 不改变单个会话 `k` 键的现有行为。

## 会话识别

活动会话查询增加 `PROXY_SESSID`：

```sql
SELECT
  ID,
  PROXY_SESSID,
  SVR_IP,
  USER,
  HOST,
  DB,
  COMMAND,
  TIME,
  TOTAL_TIME,
  STATE,
  INFO
FROM oceanbase.GV$OB_PROCESSLIST
```

识别规则：

- `PROXY_SESSID` 非空且大于 0：视为 ODP 会话；
- `PROXY_SESSID` 为空或不大于 0：视为直连会话；
- `ID` 仍作为 OceanBase connection ID 使用；
- `PROXY_SESSID` 用于识别同一 ODP Client Session 是否重建了新的后端会话，
  不直接拼入关闭命令。

`GV$OB_PROCESSLIST.ID` 的含义受 ODP `client_session_id_version` 影响，但 ODP
的 `KILL PROXYSESSION` 同时支持 Client Session ID 和 OceanBase connection
ID。因此，对 ODP 行使用当前行的 `ID` 执行能力探测和关闭，不依赖读取 ODP
配置。

## 排空策略

### 1. 目标快照

用户在会话统计页选择用户并按 `K` 后，仍需输入完整用户名确认。确认后不再只保存
一份固定 ID 列表，而是启动针对该用户名的有界排空任务。

每轮使用参数化条件重新查询：

```sql
SELECT ID, PROXY_SESSID, USER, HOST
FROM oceanbase.GV$OB_PROCESSLIST
WHERE USER = ?
```

查询结果必须再次校验 `USER` 与确认用户名完全一致，避免显示数据变化或异常返回
导致误操作。

### 2. ODP 会话

按正数 `PROXY_SESSID` 分组，同一组只选一个当前有效 `ID`。先探测新版语法：

```sql
KILL PROXYSESSION <ID>
```

如果 ODP 返回 5010 `Unknown operator, bad internal cmd`，说明当前版本使用旧版客户端
关闭语法，立即在同一目标上改为：

```sql
KILL <ID>
```

旧语法探测成功后，本次排空的其余 ODP 目标直接使用旧语法。
旧版 ODP 可能在客户端会话已关闭后返回 1317/70100
`Query execution was interrupted`；仅对上述旧语法将该返回视为命令已受理，实际
是否排空仍由后续会话快照确认。

每次排空任务先串行探测一个 ODP 会话：

- 新版或旧版语法成功，或者返回会话不存在：确认当前连接和权限支持该命令，再
  并发处理其余 ODP Client Session；
- 语法不支持或权限不足：停止代理批量命令，记录降级原因；
- 连接类错误：停止整次任务，避免并发放大故障。

代理命令不可用时，可以在后续轮次对当前数据库 `ID` 使用
`KILL CONNECTION` 进行有界降级清理，但最终必须标记为“代理关闭能力不可用”，
不能把后端命令成功误报为 Client Session 已关闭。

### 3. 直连会话

对没有有效 `PROXY_SESSID` 的会话，按 `ID` 去重并继续执行：

```sql
KILL CONNECTION <ID>
```

沿用当前“先探测一个目标，再开启并发”的保护方式。

### 4. 并发和边界

- 单轮最大并发保持 32；
- 最大执行 5 轮；
- 整体最长执行 30 秒；
- 每轮之间等待 1 秒；
- 整次任务最多处理 5000 个唯一命令目标；
- 用户按 `Esc` 可以取消尚未完成的排空任务；
- 任一全局连接错误立即停止；
- MySQL/OceanBase 错误 1094 继续按“目标已经消失”处理。

这些限制使用代码常量，不增加命令行配置，避免为单一场景扩大配置面。

## 完成判定

命令返回成功不再等同于会话已经清理。

每轮执行后重新查询目标用户：

- 连续两次查询结果都为 0：排空完成；
- 第一次为 0 后等待 1 秒再次查询，防止异步退出或短暂空窗造成误判；
- 达到轮数、时间或目标数上限后仍非 0：排空未完成；
- 被用户取消：返回已完成的部分结果和当前剩余数。

变化判断：

- `ID` 变化但 `PROXY_SESSID` 不变：同一 ODP Client Session 重建了后端会话；
- `PROXY_SESSID` 也出现新值：可能存在锁定范围错误、锁定尚未生效或其他已认证
  代理会话，需要在结果中明确提示人工核查；
- 同一 `ID` 持续存在：记录为存活目标，在下一轮重试，并保留对应错误。

## 结果模型和界面

批量结果从“执行了多少个 KILL 命令”调整为“排空结果”，至少包含：

- 执行轮数；
- 首轮会话数；
- ODP Client Session 关闭命令数；
- 直连/降级 `KILL CONNECTION` 命令数；
- 已经消失的目标数；
- 当前剩余会话数；
- 观察到的后端 ID 重建次数；
- 观察到的新 Proxy Session 数；
- 是否使用降级路径；
- 是否因超时、上限、取消或致命错误停止；
- 首条失败的命令类型、目标 ID 和原始错误。

运行中状态栏示例：

```text
draining user app: round 2/5, current 18, proxy 12, direct 6...
```

成功示例：

```text
drain user app complete: 74 -> 0 sessions, 2 rounds
```

未完成示例：

```text
drain user app incomplete: 74 -> 3 sessions after 5 rounds;
proxy kill unavailable: permission denied
```

状态栏空间不足时优先展示“是否完成、当前剩余数、停止原因”，详细首错继续保存在
现有错误状态中，避免后续刷新覆盖。

## 错误处理

错误分为三类：

1. 目标级错误：会话已不存在、单个目标关闭失败；记录后继续处理其他目标；
2. 能力级错误：`KILL PROXYSESSION` 语法不支持或无权限；停止代理并发并进入明确
   标记的降级路径；
3. 全局错误：无法查询会话、连接中断、上下文超时；立即停止任务。

所有错误必须保留 OceanBase/ODP 返回的原始错误码和首条错误文本。不能只展示
“N 个失败”。

## 代码影响范围

预计只修改以下区域：

- `internal/model/process.go`：增加代理会话 ID；
- `internal/model/kill.go`：增加排空结果和停止原因；
- `internal/obclient/client.go`：读取 `PROXY_SESSID`、按用户复查会话、执行代理
  关闭命令；
- `internal/obclient/batch_kill.go`：实现分组、能力探测、有界多轮排空；
- `internal/ui/app.go`：启动、取消和展示排空进度；
- 对应的 `_test.go` 文件；
- `README.md`：更新批量关闭语义、权限要求和停止边界。

不引入新的第三方依赖。

## 测试和验收

自动化测试至少覆盖：

1. `PROXY_SESSID` 的空值和正数扫描；
2. 相同 `PROXY_SESSID` 的多行只生成一个代理关闭目标；
3. ODP 探测成功后才启动并发；
4. ODP 权限或语法错误进入标记清晰的降级路径；
5. 直连会话仍使用 `KILL CONNECTION`；
6. `ID` 变化而 `PROXY_SESSID` 不变时记录后端重建；
7. 连续两次为 0 才报告成功；
8. 五轮、30 秒、5000 个目标和用户取消均能停止；
9. 1094 计为目标已经消失；
10. 首条真实错误在刷新后仍保留；
11. `go test ./...` 和仓库原生构建入口通过。

人工验收建议在已锁定的测试账号上进行：

1. 通过 ODP 建立多条连接并确认 `PROXY_SESSID` 非空；
2. 锁定账号后执行排空，验证客户端连接被断开且不会出现同一
   `PROXY_SESSID` 对应的新 `ID`；
3. 使用直连 OBServer 的会话验证 `KILL CONNECTION` 路径；
4. 使用缺少代理管理权限的账号验证降级提示；
5. 解锁账号由操作者独立执行，不纳入 `obtop`。

## 风险与回滚

- `KILL PROXYSESSION` 会断开整个 ODP Client Session，影响比关闭单个后端
  Server Session 更大，必须继续保留完整用户名确认；
- 不同 OceanBase/ODP 版本和账号权限可能不支持代理命令，因此必须先探测；
- 已锁定账号如果仍出现新的 `PROXY_SESSID`，工具只能提示，不能假设锁定有效；
- 大量会话排空会增加 ODP 和数据库瞬时管理请求，必须保留并发和总量上限。

回滚时恢复现有 `KILL CONNECTION` 固定快照逻辑即可；数据模型新增字段和结果字段
不涉及持久化数据迁移。
