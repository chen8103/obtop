# obtop

`obtop` 是一个面向 OceanBase 的活动 SQL 监控工具，交互风格参考 `innotop`。它通过 OBProxy 连接数据库，查询 `oceanbase.GV$OB_PROCESSLIST`，过滤空闲会话，并在终端中实时展示活动 SQL。

同时提供：

- **公共 Go SDK**（`github.com/chen8103/obtop`），供其他 Go 程序直接 import
- **机器可读 CLI**（`snapshot` / `watch` / `sql-history` / `explain`），stdout 输出 JSON/NDJSON
- **短 SQL 历史查询**（基于 `GV$OB_SQL_AUDIT`，补充 Processlist 采样间隔内的短 SQL）

## 功能特性

- 支持使用 `user` 或 `user@tenant` 方式连接 OBProxy。
- 默认每 1 秒刷新一次活动 SQL。
- 主面板实时展示总 OPS、SQL QPS，以及 SELECT、INSERT、UPDATE、DELETE、REPLACE、OTHER、COMMIT、ROLLBACK 的每秒速率。
- 显示 `SESSID`、`SVR_IP`、`USER`、`HOST`、`DB`、`COMMAND`、`TIME`、`STATE`、`SQL`。
- 支持按 `TIME`、`COMMAND`、`USER`、`DB_NAME` 循环排序。
- 支持按用户、库名、关键字筛选。
- 支持根据会话 ID 执行 `kill`。
- 支持在会话统计页按用户执行代理感知、有界复查的会话排空。
- 支持根据会话 ID 对当前 SQL 执行 `explain`，也支持对已保存 SQL 文本执行 `explain`。
- 支持在当前窗口内显示帮助、输入命令、查看 explain 结果。
- 主界面下方提供 **SQL 面板**：可切换显示当前选中会话的 SQL（表格中 `SQL` 列仍为短预览），并对展示内容做简单格式化；展示长度上限为 20,000 个 Unicode 字符，避免超长语句拖慢终端。
- 支持 `q` 或 `Ctrl+C` 退出（在帮助页、Explain 结果页可按 `q` / `Esc` 先返回主界面）。

## 构建方式

```bash
CGO_ENABLED=0 go build -o obtop ./cmd/obtop
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o obtop ./cmd/obtop
```

当前依赖版本固定在可兼容 `go1.17.x` 的范围内。

在当前 macOS 环境下，建议始终使用 `CGO_ENABLED=0` 构建，避免出现 `dyld: missing LC_UUID load command` 的运行时错误。

## 版本与本地发版

```bash
make build                 # 开发构建：build/obtop，版本为 dev
./build/obtop version       # 或 --version；不连接数据库
make test
make vet
make race                  # 本机需要支持 CGO/race 的工具链
```

版本输出包含 Git commit、UTC 构建时间、Go 版本和目标平台。直接 `go build` 的版本为 `dev`，commit 和构建时间为 `unknown`。程序版本与 JSON 输出的 `schema_version` 独立管理，不改变现有 SDK 接口。

首期版本为 **v0.1.0**，属于试用阶段，SDK 和 CLI 后续可能调整。正式发版先将功能及发版配置审核合入 `main`，核对远端与本地提交一致，再在干净工作区操作：

```bash
# 以下 tag 操作由发布负责人确认后执行；make release 不创建或推送 tag。
git tag -a v0.1.0 -m 'obtop v0.1.0'
sh scripts/build.sh release --dry-run
make release
```

正式构建要求 HEAD 上恰好一个 annotated tag，格式为 `vMAJOR.MINOR.PATCH`，且没有未提交或未跟踪的源码。执行前检查所在提交属于审核后的 `main`；脚本也允许在该 tag 的 detached HEAD 上构建。已有同版本产物时拒绝覆盖。

`make release` 顺序执行测试、vet、race、SDK 示例编译，再以 `CGO_ENABLED=0` 和 `-trimpath` 构建 Linux amd64、macOS arm64；全部成功才生成最终目录：

```text
dist/v0.1.0/
  obtop_v0.1.0_linux_amd64.tar.gz
  obtop_v0.1.0_darwin_arm64.tar.gz
  SHA256SUMS
```

每个压缩包包含 `obtop`、`README.md`。需要 Go、Git、make、tar、shasum 及本机 race 所需的 C 工具链；可用 `make GO=/path/to/go release` 指定 Go。发版不改变 `go.mod` 的 Go 版本声明。

下载对应平台安装包及 `SHA256SUMS` 后，先校验，再解压到用户可写的安装目录。以下以 Linux amd64 为例：

```bash
# 校验文件包含两个平台；仅下载单个平台时提取该包的校验记录。
grep 'obtop_v0.1.0_linux_amd64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
mkdir -p "$HOME/.local/opt/obtop/v0.1.0"
tar -xzf obtop_v0.1.0_linux_amd64.tar.gz -C "$HOME/.local/opt/obtop/v0.1.0"
"$HOME/.local/opt/obtop/v0.1.0/obtop" version
"$HOME/.local/opt/obtop/v0.1.0/obtop" --help
```

macOS 使用 `darwin_arm64` 包替换上述包名。将选定版本的目录加入 PATH 即可；需要恢复旧版本时将 PATH 切回保留的旧目录。

发布负责人取得授权后，手动推送 `v0.1.0` tag，在 GitHub 创建同名 Release 并上传两个压缩包及 `SHA256SUMS`。发布说明记录该版本的 TUI、JSON/NDJSON CLI、Go SDK 能力、试用限制及实际验证平台。发布后核对远端 tag 的提交，并重新下载附件校验 SHA-256；交叉编译成功不等于 Linux 运行验证或 OceanBase 现场验证。SDK 消费者使用同一 `v0.1.0` tag 和原模块路径。

`make clean` 仅删除本流程的 `build/` 和 `dist/`，不会删除仓库根目录已有的 obtop 二进制。清理前可运行 `sh scripts/build.sh clean --dry-run`，需要保留的安装包应先另存。

## 使用示例

```bash
./obtop -h

./obtop -h 10.0.0.1 -P 3306 -u dba -p xxxx
./obtop -h 10.0.0.1 -P 3306 -u dba@my_tenant -p xxxx
```

参数说明：

- `-h host`：OBProxy 地址，默认 `127.0.0.1`
- `-P port`：OBProxy 端口，默认 `3306`
- `-u user`：用户名，支持 `user` 或 `user@tenant`
- `-p password`：密码
- `-d database`：默认数据库，默认 `oceanbase`
- `-i interval`：刷新间隔，单位秒，默认 `1`

## 机器可读 CLI

连接参数与 TUI 相同（`-h` / `-P` / `-u` / `-p` / `-d`）。数据只写 stdout，诊断日志写 stderr，不含 ANSI。

```bash
# 单次快照
./obtop snapshot -h 10.0.0.1 -u dba -p xxxx --format json

# 持续采集（有界时长）；慢消费者不会静默丢帧，而是拉大 actual_interval_ms
./obtop watch -h 10.0.0.1 -u dba -p xxxx --interval 1s --duration 60s --format ndjson > capture.ndjson

# 短 SQL 历史（GV$OB_SQL_AUDIT）
./obtop sql-history -h 10.0.0.1 -u dba -p xxxx --since 5m --format ndjson

# 自动翻页取完；或用 --cursor 续页（格式 server_ip:port/request_id）
./obtop sql-history -h 10.0.0.1 -u dba -p xxxx --since 5m --all --limit 500 --format ndjson
./obtop sql-history -h 10.0.0.1 -u dba -p xxxx --cursor 10.0.0.1:2882/99 --format ndjson

# 对已保存 SQL 做 EXPLAIN（不依赖原会话仍存在）
./obtop explain -h 10.0.0.1 -u dba -p xxxx --sql 'SELECT 1 FROM dual' --db-name oceanbase --format json
```

退出码：

| 码 | 含义 |
|----|------|
| 0 | 成功 |
| 1 | 连接或查询错误 |
| 2 | 参数错误 |
| 3 | 部分失败（已输出部分数据，或完整性非 complete） |
| 130 | 取消（Ctrl+C） |

协议字段要点：

- `schema_version`：当前为 `"1"`
- `seq`：记录序号
- `sampled_at` / `request_time`：UTC RFC3339Nano
- `source`：`processlist` / `sql-audit` / `sql-history-meta` / `explain`
- `sql`：源端原始 SQL，**不受** TUI 96 / 20000 字符显示限制
- `error`：对象或 `null`；尽量保留数据库 `code` / `sql_state`
- 速率状态：`rates.warming_up` / `rates.stale` / `rates.ready`，未知值不会伪装成 0

## 公共 Go SDK

模块路径：`github.com/chen8103/obtop`。

```bash
go get github.com/chen8103/obtop@latest
```

本地示例见 [`examples/consumer`](examples/consumer)（通过 `replace` 指向本仓库）。

```go
import (
    "context"
    "github.com/chen8103/obtop"
)

client, err := obtop.Open(ctx, obtop.Config{
    Host: "10.0.0.1", Port: 3306, User: "dba", Password: "xxxx", Database: "oceanbase",
})
defer client.Close()

snap, err := client.Snapshot(ctx, obtop.SnapshotOptions{ActiveOnly: true})
_ = client.Watch(ctx, obtop.WatchOptions{Interval: time.Second, Duration: time.Minute},
    func(s obtop.Snapshot) error { return nil })
page, err := client.QuerySQLHistory(ctx, obtop.HistoryOptions{Since: time.Now().Add(-5 * time.Minute)})
plan, err := client.ExplainSQL(ctx, obtop.ExplainRequest{SQL: "SELECT 1", DBName: "oceanbase"})
```

SDK 不会启动 TUI、不会写日志、不会调用 `os.Exit`。`KillSession` / `DrainUserSessions` 为显式管理方法，查询入口不会隐式触发。

更完整的契约见 [`docs/plans/2026-09-20-agent-sdk-cli-design.md`](docs/plans/2026-09-20-agent-sdk-cli-design.md)。

## 短 SQL 历史前置条件

- 目标租户可查询 `oceanbase.GV$OB_SQL_AUDIT`
- 需开启配置项 `enable_sql_audit` 与/或租户变量 `ob_enable_sql_audit`（二者都会探测；均不可读时 `completeness=unknown`，不会冒充 complete）
- 审计记录有内存淘汰；窗口起点早于最老保留记录时返回 `completeness=known_gap`
- 单页触达 `--limit` 时输出 `has_more=true` 与 `next_cursor`，退出码 3；用 `--all` 自动翻页或 `--cursor` 续取
- 审计关闭 / 无权限 / 视图不可用会返回明确错误，**不会用空结果表示失败**

**验证状态：** 本仓库已包含探测、游标推进、缺口判定的自动化测试；**尚未在真实 OceanBase 环境完成审计窗口验收**，不宣称零遗漏。

## 快捷键

- `s`：循环切换排序字段
- `d`：在底部内联输入新的刷新间隔，单位秒
- `f`：在底部内联输入筛选条件，格式为 `user,db,keyword`
- `t`：打开按用户聚合的会话统计页，实时展示活跃、非活跃和总会话数
- `K`：在会话统计页排空当前选中用户的全部会话，需要输入完整用户名确认
- `v`：显示/隐藏主界面下方的 **SQL 面板**（不跳转全屏页）
- `c`：在 SQL 面板可见时，将当前选中会话的 **原始 SQL 文本**复制到系统剪贴板（异步执行，带超时）
- `w`：在 SQL 面板可见时，切换 SQL 文本是否自动换行
- `Tab`：在 SQL 面板可见时，在表格与 SQL 文本区之间切换焦点（便于在面板内滚动查看）
- `e`：在底部内联输入会话 ID，并对该会话 SQL 执行 `EXPLAIN`
- `r`：立即手动刷新
- `k`：在底部内联输入会话 ID，并使用 `y/n` 进行 kill 确认
- `?`：在当前窗口显示帮助页
- `q`：主界面下为退出程序；在帮助页、Explain 结果页为返回主界面（也可用 `Esc`）
- `Ctrl+C`：直接退出程序

## 查询语句

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
ORDER BY TIME DESC;
```

工具采集完整会话用于统计，但主活动 SQL 表会在本地排除 `Sleep` 会话。

## 实时 OPS/QPS 说明

- 指标来自 `oceanbase.GV$SYSSTAT` 的累计 SQL 计数，并按 OBServer 节点计算相邻两次采样的差值。
- `SQL QPS` 包含 SELECT、INSERT、UPDATE、DELETE、REPLACE、OTHER；`OPS` 在此基础上包含 COMMIT 和 ROLLBACK。
- 第一次采样仅建立基线，主面板会显示 `warming up`；节点重启或新增节点不会产生负数或异常尖峰。
- 指标查询失败时保留最后一份有效数据并标记 `STALE`，不会影响活动 SQL 列表刷新。
- 目标租户需要能够查询 `GV$SYSSTAT`，并启用 `enable_perf_event`；否则主面板会显示指标不可用。

### OTHER QPS 口径

- `OTHER` 直接取自 OceanBase `GV$SYSSTAT` 中 `STAT_ID = 40018` 的 `sql other count`，`obtop` 只计算累计值在相邻采样之间的每秒增量，不会根据 SQL 文本自行分类。
- OceanBase 将其定义为除 `SELECT`、`INSERT`、`REPLACE`、`UPDATE`、`DELETE`、`COMMIT`、`ROLLBACK` 之外的其他 SQL 执行次数。
- 典型范围包括：
  - DDL：`CREATE`、`ALTER`、`DROP`、`TRUNCATE`、`RENAME` 等；
  - DCL：`GRANT`、`REVOKE` 等权限控制语句；
  - 其他事务控制语句：`BEGIN`、`START TRANSACTION`、`SAVEPOINT`、`RELEASE SAVEPOINT`、`SET TRANSACTION` 等；
  - 其他未落入五类主要 DML 的管理或辅助语句，通常包括 `SHOW`、`DESC`、`EXPLAIN`、`SET`、`USE`、`CALL`、`ANALYZE`、`KILL` 等，具体以当前 OceanBase 版本的内部分类为准。
- `COMMIT` 和 `ROLLBACK` 不属于 `OTHER`，分别读取 `STAT_ID = 40025` 和 `40027`，并单独展示。
- PS 协议的 `PREPARE`、`EXECUTE`、`CLOSE` 有独立统计项 `40020`～`40024`；`obtop` 不会把这些独立统计项额外累加到 `OTHER`。OceanBase 内部 SQL 的 `sql inner other count`（`40110`）也不在当前 `OTHER` 口径内。
- OceanBase 官方没有承诺跨版本固定的完整命令枚举，因此 `OTHER` 应理解为数据库原生的兜底分类，而不是一份由 `obtop` 固定维护的命令列表。

## 行颜色说明

为了更接近 `innotop` 的视觉效果，`obtop` 会按活动 SQL 持续时间对整行着色：

- `TIME < 10s`：白色
- `10s <= TIME < 30s`：黄色
- `30s <= TIME < 120s`：橙色
- `TIME >= 120s`：红色

持续时间越长，颜色越醒目，便于快速发现长时间执行的 SQL。

## Kill 说明

### 单会话关闭

- `KILL CONNECTION <sessid>` 需要具备足够权限。
- 如果当前账号没有权限，程序不会退出，错误会显示在状态栏中。
- 按下 `k` 后，刷新会暂停，直到 kill 被确认或取消。
- 输入会话 ID 后，底部会显示一条轻量确认提示，再决定是否执行 kill。

### 按用户批量排空的执行原理

在会话统计页选中用户并按 `K` 后，需要输入完整用户名进行二次确认。确认通过后，工具按以下流程执行：

1. **重新获取目标会话**：按用户名查询 `GV$OB_PROCESSLIST`，并再次做完整用户名精确匹配。执行对象不是打开确认框时保存的一组固定 `ID`，因此能够发现排空过程中发生变化的新会话和后端会话 ID。
2. **区分代理会话和直连会话**：
   - `PROXY_SESSID > 0` 视为经过 ODP 的代理会话。工具按 `PROXY_SESSID` 分组，同一个 ODP Client Session 即使对应多条后端连接，本轮也只选择一个当前有效的数据库会话 `ID` 作为关闭入口。
   - `PROXY_SESSID` 为空或不大于 0 视为直连会话，按数据库会话 `ID` 去重。
   - `PROXY_SESSID` 只用于分组、去重和判断后端 `ID` 是否重建，不会直接拼入 KILL 命令。
3. **探测代理关闭语法**：代理会话优先执行 `KILL PROXYSESSION <ID>`。如果 ODP 返回 5010 `Unknown operator, bad internal cmd`，说明不支持新版语法，本次排空任务会切换为旧版 `KILL <ID>`。两种语法都用于关闭代理侧 Client Session。
4. **先探测、再并发执行**：每组批量命令先同步执行一个有效目标，用于尽早发现语法、权限或网络问题；探测成功后，其余目标最多使用 32 路并发执行。这样可以避免在能力不兼容时同时发出大量必然失败的命令。
5. **处理直连和能力降级**：直连会话执行 `KILL CONNECTION <ID>`。如果两种代理 Client Session 关闭语法均不可用，工具会记录降级状态，并在后续轮次对剩余代理会话有界使用 `KILL CONNECTION`；这种降级只能关闭当前后端连接，不能保证关闭整个 ODP Client Session。
6. **等待并重新查询**：一轮命令完成后等待 1 秒，再按用户名查询当前会话。若会话仍存在，工具基于最新结果重新规划下一轮目标，而不是反复使用旧 ID。
7. **连续两次为零才完成**：首次查询到 0 后再等待 1 秒复查；只有连续两次查询均为 0，才报告该用户会话排空完成。

旧版 ODP 的 `KILL <ID>` 可能在会话实际已关闭后返回 1317/70100。工具只在这条兼容路径中将该错误视为命令已受理，最终是否成功仍以重新查询的剩余会话数为准。会话已经消失时返回的 1094 也会按“目标已不存在”统计，不视为排空失败。

### 完成条件与安全边界

- 按用户排空前，应由操作者先锁定目标账号，阻止新的认证登录；`obtop` 不负责锁定或解锁账号。
- 单条 KILL 命令的超时为 5 秒；整个任务最多执行 5 轮、30 秒，并限制为 5000 个唯一命令目标。
- 运行中可按 `Esc` 取消，工具会停止尚未发出的后续轮次，并返回已完成部分和当前剩余数。
- 状态栏会分别展示当前轮次、剩余会话、代理/直连目标数、后端 ID 重建、新代理会话、降级状态和首条失败原因。KILL 命令执行数不等于最终关闭数，是否清零只以复查结果为准。
- 网络中断、连接失效或执行超时属于致命错误，任务会提前停止，避免在连接状态不明时继续批量发送命令。
- `KILL PROXYSESSION` 需要当前 OceanBase/ODP 版本和执行账号具备相应权限。若账号未正确锁定、锁定尚未生效，或者已有客户端仍能创建新的代理会话，工具会在达到安全上限后报告未清零。
- 当前实现只通过启动 `obtop` 时配置的 OBProxy 地址执行关闭命令，不会直连 `SVR_IP` 指向的云产品内部 OBServer 地址，也没有调用 OBCloud 控制台的批量关闭 API。

## Explain 说明

- 按下 `e` 后输入会话 ID，程序会读取该会话**当前** SQL，并执行 `EXPLAIN`。
- 机器命令 / SDK 的 `ExplainSQL` 可直接对已保存的 SQL 文本发起 EXPLAIN，不依赖原会话仍存在。
- 返回的是当前环境重新生成的**估算计划**，不是原始执行时的实际计划；含 `?` 占位符时仅替换字面量/注释之外的占位符为 `NULL`，并在结果 note 中说明。
- 按下 `e` 后，刷新会暂停，直到你从 explain 结果页返回主界面。
- 如果 `EXPLAIN` 失败，错误会直接显示在 explain 页面中，不会一闪而过。
- 对于不支持 explain 的语句类型，或者会话已经结束的情况，explain 可能失败。

## SQL 面板说明

- 打开 SQL 面板后，内容会随表格当前选中行自动更新；正在复制到剪贴板时会暂时冻结面板内容，避免与选中行不一致。
- 面板内展示的是 **格式化后的 SQL**（便于阅读）；`c` 复制的是 **从进程列表读到的原始 SQL 文本**（未按面板换行与关键字排版改写）。若原始 SQL 超过 20,000 个 Unicode 字符，面板只显示前 20,000 字符并提示截断，复制仍为完整原始文本。
- 剪贴板写入依赖本机常见工具（如 macOS 的 `pbcopy`、Windows 的 `clip`、Linux 下的 `wl-copy` / `xclip` / `xsel` 等）。若环境中没有可用命令，状态栏或面板底部会提示复制失败。

## 刷新间隔说明

- 按下 `d` 后输入一个正整数秒数，即可修改刷新间隔。
- 新的刷新间隔会立即生效，并同步更新顶部标题里的 `every Ns` 显示。

## 筛选说明

- 按下 `f` 后，在底部输入一行筛选条件，格式为 `user,db,keyword`。
- 示例：
  - `dba,,`
  - `,test_db,`
  - `dba,test_db,select`
- 某一段留空表示清空该项筛选条件。
