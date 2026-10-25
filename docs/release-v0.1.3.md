# v0.1.3 全链路发版验收

验收日期：2026-10-25（Asia/Shanghai）。结论：从最新 GitLab `main` 推送 annotated tag，到自动构建、GitHub Release 上传、公开下载 SHA-256 校验及 GitHub 首页 README 同步，全流程通过；现有版本的不变性核对通过。

本记录在发布后补充，发布源码、tag、Release 和附件保持原样，以以下固定标识为准。

## 发布对象与源码对应

| 对象 | 标识 |
|---|---|
| 版本 | `v0.1.3` |
| 发版时的 GitLab main / tag 源提交 | `1e6b9eb77ed56df6fb48c718e64aa9d8a8ba2855` |
| GitLab annotated tag 对象 | `58f92141eb3143af84597570ff1ae7ccec30fb84` |
| GitLab source tree | `2b3c730a3e5336993c162a5175639a500b8c4bac` |
| GitHub 源码快照提交 | `162794836214e46acadbb37951c5e90eff7476b6` |
| GitHub source tree | `2b3c730a3e5336993c162a5175639a500b8c4bac` |

GitHub 源码快照没有 parent，不携带 GitLab 提交历史。两边 source tree 一致，二进制中的 Git commit 记录 GitLab 源提交。GitHub `main` 通过单独的 README 文档提交更新，不改变发布 tag 或源快照。

## 自动流水线

本次直接推送 `v0.1.3` tag，一次流水线完成全部步骤，没有补发、手工上传、作业重试或移动版本 tag。

| 对象 | 结果 | 完成时间（UTC+08） |
|---|---|---|
| [tag 流水线 #3188931](http://gitlab.qiyi.domain/dba/obtop/pipelines/3188931) | success | 2026-10-25 09:29:07 |
| [build_release #6542718](http://gitlab.qiyi.domain/dba/obtop/-/jobs/6542718) | success | 2026-10-25 09:27:55 |
| [publish_github_release #6542719](http://gitlab.qiyi.domain/dba/obtop/-/jobs/6542719) | success | 2026-10-25 09:29:06 |

发布作业日志确认：

```text
Published and verified: https://github.com/chen8103/obtop/releases/tag/v0.1.3
README synchronized and verified: https://github.com/chen8103/obtop#readme
```

[GitHub Release v0.1.3](https://github.com/chen8103/obtop/releases/tag/v0.1.3) 的 ID 为 `423083924`，`draft=false`、`prerelease=false`，公开时间为 **2026-10-25 09:28:57（UTC+08）**。验收时 GitHub latest Release 已指向 `v0.1.3`。

## 构建与运行检查

- 51 项发版脚本测试、Go 单元测试、`go vet`、race 检查、SDK 示例编译均通过。
- 使用 `CGO_ENABLED=0` 构建 Linux amd64、macOS arm64 两个安装包，实际 Go 版本为 `go1.26.3`。
- 两个包均只包含可执行 `obtop` 和 `README.md`；Linux 为静态 ELF x86-64，macOS 为 Mach-O arm64。
- Linux amd64 在原生 CI 执行 `version` 和 `--help` 通过；macOS arm64 的最终包在本机执行相同检查通过。

最终 macOS 包的版本信息如下；Linux 的 Platform 为 `linux/amd64`，其余版本字段相同：

```text
Version: v0.1.3
Git Commit: 1e6b9eb77ed56df6fb48c718e64aa9d8a8ba2855
Build Time: 2026-10-25T01:27:09Z
Go Version: go1.26.3
Platform: darwin/arm64
```

## 公开附件与独立 SHA-256 校验

使用无认证 `curl` 从公开 URL 下载三个附件，逐字节与原 GitLab CI 制品比较一致，计算所得 SHA-256 与 GitHub asset digest 一致。

| 附件 | GitHub asset ID | 字节数 |
|---|---|---|
| [Linux amd64 安装包](https://github.com/chen8103/obtop/releases/download/v0.1.3/obtop_v0.1.3_linux_amd64.tar.gz) | `673785700` | 5297080 |
| [macOS arm64 安装包](https://github.com/chen8103/obtop/releases/download/v0.1.3/obtop_v0.1.3_darwin_arm64.tar.gz) | `673785935` | 5022269 |
| [SHA256SUMS](https://github.com/chen8103/obtop/releases/download/v0.1.3/SHA256SUMS) | `673786229` | 201 |

| 文件 | SHA-256 |
|---|---|
| `obtop_v0.1.3_linux_amd64.tar.gz` | `3dcdceded4e2de9bae01ac8a77fe582db95048d8eb7b0675c7976c1c143db31c` |
| `obtop_v0.1.3_darwin_arm64.tar.gz` | `3f4e828614f5a466c8c499de87b5f0bd3d4c0cd1e0f4e13d46359938e8bba472` |
| `SHA256SUMS` | `2668e302db7e600419562abb0127e08747692edacc3dec925aad8b68a06d7661` |

可用已登录的 GitHub CLI 下载复核，文件保留在命令创建的临时目录：

```bash
VERIFY_DIR=$(mktemp -d /tmp/obtop-v0.1.3-verify.XXXXXX)
gh release download v0.1.3 --repo chen8103/obtop --dir "$VERIFY_DIR"
(cd "$VERIFY_DIR" && shasum -a 256 -c SHA256SUMS)
```

本次校验输出：

```text
./obtop_v0.1.3_darwin_arm64.tar.gz: OK
./obtop_v0.1.3_linux_amd64.tar.gz: OK
```

`SOURCE_COMMIT` 仅在 GitLab CI 制品中保存，其值与发布源提交一致，没有作为 GitHub 附件上传。GitLab 制品保留 30 天；长期复核可使用上述公开附件和固定哈希。

## GitHub 首页 README 自动同步

- [GitHub 首页](https://github.com/chen8103/obtop#readme) 已自动更新到 `v0.1.3`，两个平台的下载链接、校验、安装目录和 PATH 说明均对应本版本。
- 自动文档提交：[7b8a2c5aa1d7b9bfb07451db38183ebe5d2b7c17](https://github.com/chen8103/obtop/commit/7b8a2c5aa1d7b9bfb07451db38183ebe5d2b7c17)。
- 父提交：`da6dfca856cdaded4778b6d2eaf11347eb4cfd69`；唯一变更文件为 `README.md`。
- README blob：`ebf63aa2bc0fad48ad509106aa64620e40c7941e`。从默认分支回读与发版前计算的期望内容完全一致，下载区块外的原文保留。
- 本地渲染再次确认同版本幂等、旧版本跳过，没有为此重复执行远端写入。

本次验证了发布脚本先公开并校验 Release，再自动更新首页 README 的真实执行链路。

## 旧版本不变性核对

对 `v0.1.0`、`v0.1.1`、`v0.1.2` 保存发版前快照，完成发布后回读比较：

| 对象 | 比较字段 | 结果 |
|---|---|---|
| 3 个旧 GitHub Release | ID、版本名、说明、目标、草稿/预发布状态、创建/更新/公开时间 | 完全一致 |
| 9 个旧附件 | ID、名称、标签、大小、digest、创建/更新时间 | 完全一致 |
| 3 个旧 GitHub tag | 对象类型、对象 SHA | 完全一致 |
| 全部旧 GitLab tag | tag 对象 SHA、peeled commit SHA | 完全一致 |

本次仅增加 `v0.1.3` Release、源码 tag、三个附件及 GitHub `main` 的 README 文档提交；旧版本未覆盖、未补传、未移动。

## 验收范围

本次覆盖 main/tag 对应、测试、双平台构建打包、源码快照、自动上传、公开下载校验、首页 README 同步及旧版本状态比较。发布时没有修改源码、CI 配置、权限或凭据，未连接生产 OceanBase；版本/帮助命令通过不替代数据库现场功能验收。

后续发版按 [最简发版命令](auto-release.md#发布一个新版本最简命令) 使用新版本号。已发布的 `v0.1.3` 不重打、不覆盖。
