# GitLab tag 自动发布到 GitHub Release

入口是本项目 GitLab 的 `.gitlab-ci.yml`，目标固定为 `chen8103/obtop`。推送到 GitLab 的 annotated `vMAJOR.MINOR.PATCH` tag 会触发构建及发布；普通分支推送不发版。配置使用 GitLab 11.11 支持的 `only:refs/variables`，已经通过本实例 CI Lint。

## 首次启用

1. 将 CI 配置、发布脚本及相关修改审核合入 GitLab `main`。tag 所指提交必须在远端 `main` 历史中；现有 `v0.1.0` 不改动、不重推。
2. 在 GitLab 项目 **Settings → Repository → Protected tags** 配置 `v*`，将创建权限限定给发布负责人/维护者。
3. 在 GitHub 创建只授权 **chen8103/obtop** 的 fine-grained Token，授予 **Contents: Read and write**。不要把本机通用凭据复制进 CI，也不要把 Token 写进文件、命令参数或聊天。
4. 在 GitLab **Settings → CI/CD → Variables** 添加 `OBTOP_GITHUB_TOKEN`，勾选 **Protected**、**Masked**。保持 `CI_DEBUG_TRACE` 关闭。Token 的值仅通过该设置页输入；不要提交到仓库。
5. Runner 需要 Linux amd64 Docker 执行环境，能拉取固定的官方 Go 镜像、下载 Go 依赖及官方 GitHub CLI，并能访问 `github.com`、`api.github.com` 和 GitHub 附件存储。当前项目存在可运行无 tag 作业的在线 amd64 共享 Runner；镜像拉取、网络及实际 CI 执行仍以首次流水线为准。

当前代码使用 Go **1.26.2** 的官方镜像并固定镜像 digest，模块兼容性声明仍是 Go 1.17；GitHub CLI **2.103.2** 从官方 Release 获取，Linux amd64/arm64 各自固定 SHA-256。不通过未固定版本的安装脚本引入工具。

GitLab 11.11.3 本身不会提供 GitHub 写入凭据。未配置 Token 或未保护 tag 时，发布会明确失败。首次启用不包含自动改权限、签发 Token 或创建正式版本。

## 发布一个新版本

例如下一版使用 `v0.1.1`，发布前先确认该版本号尚未在两边使用：

```bash
git fetch origin
git switch main
git pull --ff-only origin main
# 如需专属说明，先将 docs/release-v0.1.1.md 合入 main，再创建 tag。
git tag -a v0.1.1 -m 'obtop v0.1.1'
git push origin refs/tags/v0.1.1
```

GitLab tag 所在提交必须包含这套 CI 配置。构建阶段核验 main 归属、annotated tag、干净工作区，运行发布脚本测试、Go 单元测试、vet、race 和 SDK 示例编译，再以 `CGO_ENABLED=0` 构建两个平台。与 Runner 本机系统/架构匹配的可执行文件还会运行 `version`、`--help`；macOS 产物在 Linux Runner 上只做交叉编译及包格式检查。

GitLab 构建制品保留 30 天：

```text
dist/v0.1.1/
  obtop_v0.1.1_linux_amd64.tar.gz
  obtop_v0.1.1_darwin_arm64.tar.gz
  SHA256SUMS
  SOURCE_COMMIT
```

两个安装包均包含可执行的 `obtop` 和 README。`SOURCE_COMMIT` 记录构建提交，仅供发布阶段核对，不作为 GitHub 附件上传。GitHub Release 附加两个安装包及 `SHA256SUMS`，下载页为：

`https://github.com/chen8103/obtop/releases/tag/v0.1.1`

## GitLab 与 GitHub 的源码对应

两边现有历史不同。脚本将发布提交的完整 tracked source tree 创建为独立的 GitHub 源码快照，并只推送新版本 tag；不将 GitLab 历史推到公开仓库，不移动 GitHub `main`。两边的 commit SHA 可以不同，但 source tree SHA 必须完全相同。二进制版本信息及 Release 说明保留原始构建 commit SHA，说明中同时记录 tree SHA。GitHub 自动生成的 Source code 归档因此对应实际构建源码。

## 发布确认与失败恢复

发布前先执行 `--dry-run`，仅查询远端，不创建 tag、Release 或附件。发布阶段随后核对本地产物 SHA-256、源提交及压缩包内容，再同步源码 tag、创建草稿、上传缺少的附件。脚本重新下载三个附件，与本地 SHA-256 逐一比对，并再次核对源码 tag，全部通过才将 Release 公开。

- 构建失败：没有发布作业。修复后发新 tag；不要移动已有版本 tag。
- 上传失败：保留草稿和已有附件，重新运行原 `publish_github_release` 作业，复用原构建制品。脚本会先回读；相同附件复用，缺少的附件补传，不使用 `--clobber`。
- 写入超时：脚本先回读确认结果，不盲目重复写入；未确认时停止并保留草稿。
- 已公开版本：仅回读并下载核对，不修改；源码或附件不一致时停止，需人工核对并选择新版本号。
- 构建制品过期：重新构建可能改变构建时间和压缩包 SHA-256；不要与已有附件混用，应检查草稿并采用新版本号。

作业日志必须出现 `Published and verified`（或重试时的 `Already published and verified`），并检查 GitHub 页面包含全部附件。流水线成功不代表 OceanBase 现场功能验收；生产数据库不参与此流程。撤回有问题的对外版本属于单独操作，本脚本不会自动删除 Release、tag 或附件。

## 本地检查

```bash
python3 -B -m unittest discover -s scripts/tests -p 'test_*.py'
sh -n scripts/build.sh scripts/install-release-gh.sh
```

对已有正式构建可运行以下只读预检（需要 GitHub CLI 已登录，或安全注入专用 Token）：

```bash
python3 -B scripts/publish-release.py --tag v0.1.1 --dry-run
```

真正发布只使用 `OBTOP_GITHUB_TOKEN` 环境变量中的专用凭据。不要为测试创建正式 tag；隔离临时仓库的构建与模拟远端测试即可验证本地逻辑，首次 GitLab → GitHub 完整链路仍须在上述启用项完成后验证。

2026-10-24 本地验证：23 项脚本测试通过；在 Go 1.26.2 Linux arm64 容器中完成 test、vet、race、SDK 示例与双平台打包；实际包格式、源提交和 SHA-256 校验通过。macOS arm64 在本机运行版本/帮助命令通过，Linux amd64 在 Linux 容器的 amd64 仿真环境运行相同命令通过。固定版本 CLI 的下载、SHA-256 与安装验证通过；GitHub 只读预检通过。未创建或更新任何 GitHub tag/Release，尚未执行真实 GitLab 发布流水线。
