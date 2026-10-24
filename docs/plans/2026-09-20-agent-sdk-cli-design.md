# obtop SDK / Agent CLI 设计说明

本文记录公共 SDK 与机器可读 CLI 的设计。

## 目标

将 obtop 从纯 TUI 扩展为：

1. 可供其他 Go 模块 `import` 的公共 SDK
2. 面向 agent 的非交互 JSON/NDJSON CLI
3. 基于 `GV$OB_SQL_AUDIT` 的短 SQL 历史查询
4. 与已保存 SQL 解耦的 EXPLAIN

TUI、CLI、SDK 共用查询与计算逻辑。

## 包布局

```
github.com/chen8103/obtop          # 公共 SDK 入口 package obtop
github.com/chen8103/obtop/obmodel  # 公共纯数据类型（叶子包，无 DB 依赖）
internal/obclient                     # 查询 / EXPLAIN / audit / drain
internal/service                      # Sampler + Monitor
internal/ui                           # TUI
cmd/obtop                             # TUI 与机器命令入口
examples/consumer                     # 独立消费模块示例
```

根包通过类型别名再导出 `obmodel` 中的常用类型，调用方通常只需 import 根包。

## SDK 契约

```go
client, err := obtop.Open(ctx, obtop.Config{...})
defer client.Close()

snap, err := client.Snapshot(ctx, obtop.SnapshotOptions{ActiveOnly: true})
err = client.Watch(ctx, obtop.WatchOptions{Interval: time.Second}, func(s obtop.Snapshot) error {
    // 同步回调；返回 error 立即终止；无队列，不静默丢帧
    return nil
})
page, err := client.QuerySQLHistory(ctx, obtop.HistoryOptions{Since: time.Now().Add(-5*time.Minute)})
plan, err := client.ExplainSQL(ctx, obtop.ExplainRequest{SQL: "...", DBName: "db"})
```

- 不调用 `os.Exit`，不写 stdout/stderr，不启动 TUI
- 管理动作只有显式 `KillSession` / `DrainUserSessions`
- 每次 `Watch` 使用独立 `Sampler` 速率基线
- `Snapshot` 的会话/指标问题写在字段里（`SessionsErr`、`MetricsErr`、`RatesWarmingUp`、`RatesStale`），空结果与失败可区分

## 录制路径 vs TUI Monitor

| 路径 | 行为 |
|------|------|
| TUI `Monitor` | 容量 8 channel，满则丢旧帧（人眼刷新可接受） |
| SDK `Watch` / CLI `watch` | 同步 `consume`，无队列；慢消费者拉大 `ActualInterval` |

## JSON / NDJSON 协议

- `schema_version`: `"1"`
- `seq`: 记录序号
- `sampled_at` / `request_time`: RFC3339Nano UTC
- `source`: `processlist` / `sql-audit` / `sql-history-meta` / `explain`
- `sql`: 源端原始文本，不受 TUI 96 / 20000 显示限制
- `error`: 对象或 null；数据库错误尽量带 `code` / `sql_state`

退出码：`0` 成功，`1` 连接/查询错误，`2` 参数错误，`3` 部分失败，`130` 取消。

## SQL 历史边界

- 数据源：`oceanbase.GV$OB_SQL_AUDIT`
- 启用探测：配置项 `enable_sql_audit`（`SHOW PARAMETERS`）与租户变量 `ob_enable_sql_audit`（`SHOW VARIABLES`）；均不可读时 `Completeness=unknown`
- 游标：`(SVR_IP, SVR_PORT, REQUEST_ID)`；CLI 支持 `--cursor` 与 `--all`
- 完整性：`complete` / `unknown` / `known_gap`（保留窗口）；与导出截断字段 `has_more` 分离
- 审计关闭、无权限、视图不存在返回明确错误，**不用空结果冒充**
- 审计是有淘汰的内存视图，不宣称零遗漏或永久历史

**本轮未在真实 OceanBase 环境验证审计窗口。** 代码与单测已覆盖探测、游标、缺口判定；真实环境验收待补。

## EXPLAIN

- `ExplainSQL` / `ExplainStatement` 接受已保存 SQL + 库名
- 返回结构化 `Columns` / `Rows` / `Notes`，`Estimated=true`
- 占位符 `?` 仅在字面量/注释之外替换为 `NULL`，并写入 note
- `ExplainSession` 仍可用，会记录从会话读到的 SQL

## 公共模块获取

```bash
go get github.com/chen8103/obtop@latest
```

本地开发可用 `replace`，见 `examples/consumer/go.mod`。

## 非目标（本 Issue 不做）

- MCP / HTTP 服务
- 常驻采集或长期存储
- 自动 SQL 优化 / 自动 KILL / 账号锁定
- 升级 Go 1.17 基线
