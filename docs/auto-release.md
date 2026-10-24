# GitLab tag 自动发布到 GitHub Release

入口是本项目 GitLab 的 `.gitlab-ci.yml`，目标固定为 `chen8103/obtop`。推送到 GitLab 的 annotated `vMAJOR.MINOR.PATCH` tag 会触发构建及发布；普通分支推送只运行检查。另提供显式的受保护 main 补发入口。配置使用 GitLab 11.11 支持的 `only:refs/variables`，已经通过本实例 CI Lint。

## 首次启用

1. 将 CI 配置、发布脚本及相关修改审核合入 GitLab `main`。tag 所指提交必须在远端 `main` 历史中；现有 `v0.1.0` 不改动、不重推。
2. 在 GitLab 项目 **Settings → Repository → Protected tags** 配置 `v*`，将创建权限限定给发布负责人/维护者。
3. 在 GitHub 创建只授权 **chen8103/obtop** 的 fine-grained Token，授予 **Contents: Read and write**。不要把本机通用凭据复制进 CI，也不要把 Token 写进文件、命令参数或聊天。
4. 在 GitLab **Settings → CI/CD → Variables** 添加 `OBTOP_GITHUB_TOKEN`，勾选 **Protected**，实例支持 **Masked** 时同时勾选。当前 11.11.3 自建实例 API 不返回 masked 标志，不能据此确认掩码状态。保持 `CI_DEBUG_TRACE` 关闭；脚本禁止调试追踪，不输出工具原始认证错误。Token 的值仅通过该设置页输入；不要提交到仓库。
5. Runner 需要 Linux amd64 Docker 执行环境，使用平台登记的标准基础镜像 `docker-registry.qiyi.virtual/library/golang:1.26-iqiyi-2`，能下载 Go 依赖及官方 GitHub CLI，并能访问 `github.com`、`api.github.com` 和 GitHub 附件存储。共享 Runner 拒绝直接使用 Docker Hub 官方 Go 镜像。本项目分支探测流水线已验证内部 Go 镜像可启动并包含完整工具链。

当前代码固定使用平台维护的 Go 镜像版本 `1.26-iqiyi-2`；2026-10-24 实测 Go **1.26.3**、Python **3.13.5**，包含 gcc、Git、curl、shasum。模块兼容性声明仍是 Go 1.17，产物记录实际工具链版本。镜像依据为 [iQIYI Docker 标准基础镜像](https://iq.feishu.cn/wiki/FTlGwHQXRibnBSkK4awc22TInFh)。GitHub CLI **2.103.2** 从官方 Release 获取，Linux amd64/arm64 各自固定 SHA-256。

GitLab 11.11.3 本身不会提供 GitHub 写入凭据。未配置 Token 或未保护 tag 时，发布会明确失败。首次启用不包含自动改权限、签发 Token 或创建正式版本。

## 发布一个新版本（最简命令）

首次启用完成后，每次发版只需修改 `VERSION`。以下以 `v0.1.2` 举例：先将改动审核合入 GitLab `main`，确认工作区干净、本地 `main` 没有未推送提交，且该版本号尚未在 GitLab 和 GitHub 使用。逐条执行，任一步报错就停止。

### 1. 从最新 main 打 tag 并推送

```bash
cd /Users/mingchen/work/obtop
VERSION=v0.1.2

git fetch origin --tags
git switch main
git merge --ff-only origin/main
git tag -a "$VERSION" -m "obtop $VERSION"
git push origin "refs/tags/$VERSION"
```

仓库路径按本机位置调整。如需专属版本说明，先将 `docs/release-v0.1.2.md` 合入 `main`，再创建 tag。推送 tag 后，流水线自动构建 Linux amd64、macOS arm64 安装包并上传 GitHub Release。

### 2. 检查 GitLab 流水线

打开 [obtop Pipelines](http://gitlab.qiyi.domain/dba/obtop/pipelines)，找到该 tag，确认 `build_release` 和 `publish_github_release` 两个作业均为绿色。发布日志应出现 `Published and verified`；重试已发布版本时为 `Already published and verified`。

### 3. 查看 GitHub Release，下载并校验

以下命令沿用同一终端中的 `VERSION`，需要本机已安装 GitHub CLI 且 `gh` 已登录：

```bash
gh release view "$VERSION" --repo chen8103/obtop
gh release view "$VERSION" --repo chen8103/obtop --web

VERIFY_DIR=$(mktemp -d /tmp/obtop-release.XXXXXX)
gh release download "$VERSION" --repo chen8103/obtop --dir "$VERIFY_DIR"
(cd "$VERIFY_DIR" && shasum -a 256 -c SHA256SUMS)
```

页面应显示已公开发布，并包含两个 `.tar.gz` 安装包和 `SHA256SUMS`；校验输出应为两个 `OK`。下载文件保留在 `$VERIFY_DIR`，可用于安装或进一步检查。

上传失败时，重试原流水线的 `publish_github_release` 作业以复用原制品。不要移动或重推已有版本 tag；代码需要修复时使用新版本号。其他恢复情况见下文「发布确认与失败恢复」。

### 构建内容与产物

GitLab tag 所在提交必须包含这套 CI 配置。构建阶段核验 main 归属、annotated tag、干净工作区，运行发布脚本测试、Go 单元测试、vet、race 和 SDK 示例编译，再以 `CGO_ENABLED=0` 构建两个平台。与 Runner 本机系统/架构匹配的可执行文件还会运行 `version`、`--help`；macOS 产物在 Linux Runner 上只做交叉编译及包格式检查。

GitLab 构建制品保留 30 天：

```text
dist/v0.1.2/
  obtop_v0.1.2_linux_amd64.tar.gz
  obtop_v0.1.2_darwin_arm64.tar.gz
  SHA256SUMS
  SOURCE_COMMIT
```

两个安装包均包含可执行的 `obtop` 和 README。`SOURCE_COMMIT` 记录构建提交，仅供发布阶段核对，不作为 GitHub 附件上传。GitHub Release 附加两个安装包及 `SHA256SUMS`，下载页为：

`https://github.com/chen8103/obtop/releases/tag/v0.1.2`（示例，实际发布成功后可访问）。

## GitLab 与 GitHub 的源码对应

两边现有历史不同。脚本将发布提交的完整 tracked source tree 创建为独立的 GitHub 源码快照，并只推送新版本 tag；不将 GitLab 历史推到公开仓库，不移动 GitHub `main`。两边的 commit SHA 可以不同，但 source tree SHA 必须完全相同。二进制版本信息及 Release 说明保留原始构建 commit SHA，说明中同时记录 tree SHA。GitHub 自动生成的 Source code 归档因此对应实际构建源码。

## 发布确认与失败恢复

发布前先执行 `--dry-run`，仅查询远端，不创建 tag、Release 或附件。发布阶段随后核对本地产物 SHA-256、源提交及压缩包内容，再同步源码 tag、创建草稿、上传缺少的附件。脚本重新下载三个附件，与本地 SHA-256 逐一比对，并再次核对源码 tag，全部通过才将 Release 公开。

如果失败发生在 CI 容器准备等基础设施阶段，原版本源码无需改变，但 tag 中的旧 CI 配置已经不能运行：先将 CI 修复合入 main，再在 GitLab **CI/CD → Pipelines → Run pipeline** 选择 **main**，显式添加变量 `OBTOP_RELEASE_TAG=v0.1.1`。流水线使用 main 中的 CI 配置，在构建和发布前检出该版本 tag；源提交、源码 tree、二进制和 GitHub 源码快照仍对应原 tag，不移动现有 tag。补发入口只接受受保护的 main、规范 annotated tag，且 tag 的源提交必须属于远端 main。普通分支无法通过此入口发布，也不覆盖 GitLab 的 CI 身份变量。

当前 GitLab 11.11.3 实例不能使用 CI_JOB_TOKEN 经项目 REST API 读取原流水线制品。控制器未变化时，应重试同一流水线的 publish 作业，使用 Runner 自动传递的原构建制品。控制器需要修复时，只有确认草稿还没有附件、版本尚未公开，才能通过 main 补发入口重新构建同一源码 tag；新的构建时间和包 SHA-256 会改变。如草稿已有附件或版本已公开，先停止核对，不混用新旧构建，不自动复制更大权限的用户凭据到 CI。

发布控制器在切换源码 tag 前保存到忽略的 build 目录，使用 `--source-dir` 指向实际 release worktree。因此补发可以使用修复后的草稿查询和上传逻辑，而不修改 release tag 中的源码。GitHub 的按 tag 查询可能看不到草稿，控制器会分页查询当前凭据可见的 Release 列表后继续核对，确认已有草稿或附件前不重复创建、不覆盖。

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

真正发布只使用 `OBTOP_GITHUB_TOKEN` 环境变量中的专用凭据。不要为测试创建正式 tag；隔离临时仓库的构建与模拟远端测试即可验证本地逻辑。新环境的首次 GitLab → GitHub 完整链路须在上述启用项完成后验证。

2026-10-24 已完成 [v0.1.1 真实发布](https://github.com/chen8103/obtop/releases/tag/v0.1.1)：经受保护 main 补发入口，原 tag 源码的双平台构建、上传及公开下载校验通过，[流水线 #3188866](http://gitlab.qiyi.domain/dba/obtop/pipelines/3188866) 最终成功。后续新 tag 将使用 main 中修复后的 CI 配置；该次验收没有额外创建新版本来重复测试单次 tag 路径。2026-10-25 重置项目 Runner 注册令牌后，[核对作业 #6542520](http://gitlab.qiyi.domain/dba/obtop/-/jobs/6542520) 复用原制品并验证已发布附件成功，没有重新构建或发布新版本。
