当前发布版本为 **@VERSION@**，属于试用阶段，SDK 和 CLI 后续可能调整。源码快照见 [@VERSION@ tag](https://github.com/chen8103/obtop/tree/@VERSION@)。

### 下载与安装 @VERSION@

[GitHub Release @VERSION@](https://github.com/chen8103/obtop/releases/tag/@VERSION@) 提供以下附件，无需安装 Go 即可使用：

| 平台 | 安装包 |
|---|---|
| Linux amd64（x86-64） | [obtop_@VERSION@_linux_amd64.tar.gz](https://github.com/chen8103/obtop/releases/download/@VERSION@/obtop_@VERSION@_linux_amd64.tar.gz) |
| macOS arm64（Apple Silicon） | [obtop_@VERSION@_darwin_arm64.tar.gz](https://github.com/chen8103/obtop/releases/download/@VERSION@/obtop_@VERSION@_darwin_arm64.tar.gz) |

校验文件：[SHA256SUMS](https://github.com/chen8103/obtop/releases/download/@VERSION@/SHA256SUMS)。以下命令需要 `curl`、`tar`、`shasum`；每个平台只核对所下载安装包的校验记录，输出 `OK` 后才解压安装。命令在子 Shell 中执行，失败即停止，下载文件保留在创建的临时目录。

Linux amd64：

```bash
(
set -eu
PACKAGE=obtop_@VERSION@_linux_amd64.tar.gz
BASE_URL=https://github.com/chen8103/obtop/releases/download/@VERSION@
DOWNLOAD_DIR=$(mktemp -d /tmp/obtop-install.XXXXXX)
cd "$DOWNLOAD_DIR"
curl -fL --connect-timeout 15 --max-time 120 -o "$PACKAGE" "$BASE_URL/$PACKAGE"
curl -fL --connect-timeout 15 --max-time 120 -o SHA256SUMS "$BASE_URL/SHA256SUMS"
awk -v name="$PACKAGE" '$2 == name || $2 == "./" name' SHA256SUMS | shasum -a 256 -c -
mkdir -p "$HOME/.local/opt/obtop/@VERSION@"
tar -xzf "$PACKAGE" -C "$HOME/.local/opt/obtop/@VERSION@"
"$HOME/.local/opt/obtop/@VERSION@/obtop" version
"$HOME/.local/opt/obtop/@VERSION@/obtop" --help
)
```

macOS arm64：

```bash
(
set -eu
PACKAGE=obtop_@VERSION@_darwin_arm64.tar.gz
BASE_URL=https://github.com/chen8103/obtop/releases/download/@VERSION@
DOWNLOAD_DIR=$(mktemp -d /tmp/obtop-install.XXXXXX)
cd "$DOWNLOAD_DIR"
curl -fL --connect-timeout 15 --max-time 120 -o "$PACKAGE" "$BASE_URL/$PACKAGE"
curl -fL --connect-timeout 15 --max-time 120 -o SHA256SUMS "$BASE_URL/SHA256SUMS"
awk -v name="$PACKAGE" '$2 == name || $2 == "./" name' SHA256SUMS | shasum -a 256 -c -
mkdir -p "$HOME/.local/opt/obtop/@VERSION@"
tar -xzf "$PACKAGE" -C "$HOME/.local/opt/obtop/@VERSION@"
"$HOME/.local/opt/obtop/@VERSION@/obtop" version
"$HOME/.local/opt/obtop/@VERSION@/obtop" --help
)
```

安装后在当前终端将该版本加入 PATH：

```bash
export PATH="$HOME/.local/opt/obtop/@VERSION@:$PATH"
obtop version
```

如需长期使用，可将上述 `export PATH` 加入 Shell 配置文件。需要恢复旧版本时，将 PATH 切回保留的旧版本目录。
