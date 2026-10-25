# v0.1.2 自动发版验收

验收日期：2026-10-25（Asia/Shanghai）。结论：从最新 GitLab `main` 推送 annotated tag，到自动构建、GitHub Release 公开发布及独立下载 SHA-256 校验，全流程通过。

本记录在发布完成后补充到项目文档。发布 tag 和产物保持原样，构建源码仍以以下固定提交为准。

## 发布对象与源码对应

| 对象 | 标识 |
|---|---|
| 版本 | `v0.1.2` |
| 发版时的 GitLab main / tag 源提交 | `a0fc171cfe954020b4a998995c97fdd920cac5a5` |
| GitLab annotated tag 对象 | `45e3906b6a1d930e9653842a0845a2087f658f78` |
| GitLab source tree | `2038f42fafb48b535b6c210012c8a061bc50f97a` |
| GitHub 源码快照提交 | `f154cba4fd27d167fa4bbd6aa9c9780ab6f42ee5` |
| GitHub source tree | `2038f42fafb48b535b6c210012c8a061bc50f97a` |

GitHub 快照没有 parent，不携带 GitLab 提交历史。两边 source tree 完全一致；二进制中的 Git commit 记录 GitLab 源提交。发布后回读确认 tag 未移动。

## 自动流水线结果

本次直接推送 `v0.1.2` tag 触发发布，一次流水线完成，没有手工补发、重试作业或移动版本 tag。

| 对象 | 结果 | 完成时间（UTC+08） |
|---|---|---|
| [tag 流水线 #3188918](http://gitlab.qiyi.domain/dba/obtop/pipelines/3188918) | success | 2026-10-25 07:52:59 |
| [build_release #6542689](http://gitlab.qiyi.domain/dba/obtop/-/jobs/6542689) | success | 2026-10-25 07:51:48 |
| [publish_github_release #6542690](http://gitlab.qiyi.domain/dba/obtop/-/jobs/6542690) | success | 2026-10-25 07:52:59 |

发布作业日志确认：

```text
Published and verified: https://github.com/chen8103/obtop/releases/tag/v0.1.2
```

[GitHub Release v0.1.2](https://github.com/chen8103/obtop/releases/tag/v0.1.2) 的 ID 为 `423049573`，`draft=false`、`prerelease=false`，公开时间为 **2026-10-25 07:52:53（UTC+08）**。

## 构建与运行检查

- 33 项发版脚本测试、Go 单元测试、`go vet`、race 检查、SDK 示例编译均通过。
- 使用 `CGO_ENABLED=0` 构建 Linux amd64、macOS arm64 两个安装包，实际 Go 版本为 `go1.26.3`。
- 两个包均只包含可执行的 `obtop` 与 `README.md`；Linux 为静态 ELF x86-64，macOS 为 Mach-O arm64。
- Linux amd64 在原生 CI 环境执行 `version` 和 `--help` 通过；macOS arm64 的最终包在本机执行相同检查通过。

最终包的版本信息如下，Linux 的 Platform 为 `linux/amd64`，其余版本字段相同：

```text
Version: v0.1.2
Git Commit: a0fc171cfe954020b4a998995c97fdd920cac5a5
Build Time: 2026-10-24T23:51:00Z
Go Version: go1.26.3
Platform: darwin/arm64
```

## 公开附件与 SHA-256

三个附件均使用无认证 `curl` 从公开下载 URL 独立下载，逐字节与原 GitLab CI 制品比较一致，计算所得 SHA-256 与 GitHub asset digest 一致。

| 附件 | GitHub asset ID | 字节数 |
|---|---|---|
| [Linux amd64 安装包](https://github.com/chen8103/obtop/releases/download/v0.1.2/obtop_v0.1.2_linux_amd64.tar.gz) | `673616579` | 5296496 |
| [macOS arm64 安装包](https://github.com/chen8103/obtop/releases/download/v0.1.2/obtop_v0.1.2_darwin_arm64.tar.gz) | `673616809` | 5021667 |
| [SHA256SUMS](https://github.com/chen8103/obtop/releases/download/v0.1.2/SHA256SUMS) | `673617083` | 201 |

| 文件 | SHA-256 |
|---|---|
| `obtop_v0.1.2_linux_amd64.tar.gz` | `0d9f529cca45fd1801ad8adcb583565220eb0b33cbc588e601c4948b8b353d6a` |
| `obtop_v0.1.2_darwin_arm64.tar.gz` | `fe4fe67db9b9e87fd400f4eb6032369e601baee6924627fb308e592f109c16f8` |
| `SHA256SUMS` | `7c9825919b60595c93e1a566f70d1c0fc18b55c58c27d45fd9410191341f3cbd` |

可使用已登录的 GitHub CLI 下载并复核；文件保留在命令创建的临时目录：

```bash
VERIFY_DIR=$(mktemp -d /tmp/obtop-v0.1.2-verify.XXXXXX)
gh release download v0.1.2 --repo chen8103/obtop --dir "$VERIFY_DIR"
(cd "$VERIFY_DIR" && shasum -a 256 -c SHA256SUMS)
```

本次校验输出：

```text
./obtop_v0.1.2_darwin_arm64.tar.gz: OK
./obtop_v0.1.2_linux_amd64.tar.gz: OK
```

`SOURCE_COMMIT` 仅保存在 GitLab CI 制品中，其值与发布源提交一致，没有上传为 GitHub 附件。GitLab 构建制品保留 30 天；长期复核可使用上面的 GitHub 附件和固定哈希。

## 验收范围

本次覆盖正常 tag 触发、测试、双平台构建打包、源码对应、自动上传、公开下载和版本/帮助命令。发布时未修改源代码、CI 配置、权限或凭据。未连接生产 OceanBase，本记录不代表数据库现场功能验收。

后续发版按 [最简发版命令](auto-release.md#发布一个新版本最简命令) 使用新的版本号；已发布的 `v0.1.2` 不重打、不覆盖。
