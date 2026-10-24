# obtop v0.1.0 发布说明

首个试用版本将 OceanBase SQL 监控工具以 Linux amd64 和 macOS arm64 安装包交付，并提供同版本 Go SDK。

## 能力

- TUI 展示活动会话、SQL 与统计信息，支持筛选、Explain 及现有会话管理操作。
- `snapshot`、`watch`、`sql-history`、`explain` 提供 JSON/NDJSON 输出。
- 公共 Go SDK 使用模块路径 `github.com/chen8103/obtop`。
- `version` 和 `--version` 展示版本、提交、构建时间、Go 版本和平台，无需连接数据库。
- 安装包附 README，并提供 SHA-256 校验文件。

## 修复

- 修复 Watch 持续时间到期与采样 tick 同时触发时偶发返回超时错误的问题；自身持续时间到期正常结束，父 context 与回调错误继续向上传递。

## 兼容与限制

- v0.1.0 为试用版，SDK 和 CLI 后续可能调整；JSON `schema_version` 仍为 `1`。
- 不改变已有数据库行为；可用功能受目标 OceanBase 版本、视图及账号权限影响。
- 本次发版工具验证不替代生产数据库功能验收。

## 安装与验证

安装包提供 Linux amd64、macOS arm64 两个平台，每个包包含 `obtop` 和 README；下载后先按 `SHA256SUMS` 校验，再解压使用。运行 `obtop version` 可核对版本、Git commit、UTC 构建时间、Go 版本与平台。

本地发版执行单元测试、vet、race 检查和 SDK 示例编译。正式发布页记录实际构建提交、工具链与附件校验结果。

Linux amd64 已完成交叉编译和 ELF 架构检查，尚未在 Linux 环境运行验证。macOS arm64 可在发布主机执行版本与帮助命令验证。本次未连接生产 OceanBase 验证数据库功能。
