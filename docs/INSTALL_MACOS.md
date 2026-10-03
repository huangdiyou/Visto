# Apple Silicon macOS 安装

本页说明 Apple Silicon（M 系列）macOS 原生安装。Windows、Linux 与 Docker 请按[完整手册](USER_GUIDE.md)中的对应平台章节操作。

详细的准备、首次设置字段、视频运行时和完成检查见[图文安装指南](web/install-macos.html)、
[首次设置](web/setup.html)与[运行时安装](web/runtime.html)。离线使用可打开[完整手册](web/Visto-用户手册.html)。
本页对应 [Visto Server 1.0.3](https://github.com/huangdiyou/Visto/releases/tag/v1.0.3)。

## 1. 下载并校验

从 [v1.0.3 发布页](https://github.com/huangdiyou/Visto/releases/tag/v1.0.3)下载安装包和同名摘要。以下示例使用 1.0.3，部署时核对对应发布说明：

- `Visto-Server_1.0.3_macos-arm64.tar.gz`
- 对应的 `.sha256` 文件

在终端校验：

```bash
shasum -a 256 Visto-Server_1.0.3_macos-arm64.tar.gz
```

确认输出与发布页提供的 SHA-256 一致后再继续。

首发包尚无 Developer ID 签名与 Apple 公证。macOS 若阻止已校验的 Visto 程序运行，可在
「系统设置 → 隐私与安全性」对该程序选择「仍要打开」，然后按系统提示确认。服务进程
`visto-core` 和管理命令 `visto-server` 是两个独立程序，首次运行时可能分别需要放行。
只放行来源和哈希都已核对的文件；不要全局关闭 Gatekeeper 或批量移除隔离属性。
手动放行意味着 Apple 尚未验证开发者身份和公证状态。

## 2. 解压

```bash
mkdir -p ~/Downloads/visto-server
tar -xzf Visto-Server_1.0.3_macos-arm64.tar.gz -C ~/Downloads/visto-server
cd ~/Downloads/visto-server
```

## 3. 安装

安装需要管理员权限：

```bash
sudo bash scripts/install-visto-server.sh \
  --version 1.0.3 \
  --source-dir "$PWD"
```

安装器会等待服务端口可访问；若 macOS 此时拦截 `visto-core`，安装器会提示启动未就绪。
按上面的步骤放行 `visto-core` 后，运行：

```bash
sudo launchctl kickstart -k system/com.visto.server
curl -fsS http://127.0.0.1:8787/health/ready
```

看到 `"status":"ready"` 才继续。首次调用 `visto-server` 若再次被拦截，再单独放行该程序并重试命令。

默认监听 `127.0.0.1:8787`，程序安装到 `/Library/Visto`，数据默认位于
`/Library/Application Support/Visto/data`。

安装完成后打开：

```text
http://127.0.0.1:8787/
```

## 4. 首次初始化

先在本机终端读取一次性初始化令牌，不要发送给他人：

```bash
sudo cat /Library/Visto/config/host-management-token
```

按照浏览器中的首次运行向导输入令牌并创建工作空间和 Owner 账号。随后先配置存储位置，再创建第一个项目。

向导还会问一次：

> 是否允许 Owner 在网页端添加本机上的任意目录作为存储位置？
> 开启后，任何能登录 Owner 的账号都能读写本机上的任意目录。
> 仅当你独占管理这台机器时才建议开启。

默认开启，这样"配置存储位置 → 创建第一个项目"可以在网页上直接走通。**这个开关只在首次设置时询问一次，
之后不能在网页上修改**——否则一个被窃取的 Owner 会话就能自行开启主机访问。如需变更，请编辑主机上的
配置文件（`VISTO_ALLOW_WEB_HOST_PATHS=0` 关闭，`=1` 强制开启）并重启服务。当前生效状态可以在
Owner 设置 → 网络与安全 中查看。

## 5. 视频处理

视频转码和预览需要 FFmpeg/ffprobe。Visto 的 macOS 原生 Server 包不包含 FFmpeg；媒体运行时独立分发，
附带许可证、来源与 SBOM。安装前从可信交付取得固定的[媒体运行时验签公钥](../keys/media-runtime-root-public-key.txt)，
不要使用运行时下载包内的公钥。以下命令下载签名清单，让主机管理器验证签名、下载归档并校验内容：

```bash
(
set -eu
mkdir -p "$HOME/Downloads"
RUNTIME_VERSION='ffmpeg-8.1.2-macos-lgpl.2'
RUNTIME_BASE="https://visto-server-updates.pages.dev/media-runtime/$RUNTIME_VERSION"
curl -fL "$RUNTIME_BASE/media-runtime-manifest.json" -o "$HOME/Downloads/media-runtime-manifest.json"
curl -fL "$RUNTIME_BASE/media-runtime-manifest.json.sig" -o "$HOME/Downloads/media-runtime-manifest.json.sig"
curl -fL 'https://dl.819101.xyz/server/candidates/20261001/media-runtime-root-public-key.txt' \
  -o "$HOME/Downloads/media-runtime-root-public-key.txt"
test "$(cat "$HOME/Downloads/media-runtime-root-public-key.txt")" = 'lMcaxJOtCeDaiJgeW0S5Gh/m3PYQ4WG7ijIs9yxVZ2M=' || { echo '公钥不符，停止安装'; exit 1; }
sudo /Library/Visto/current/bin/visto-server --json media-runtime install \
  --media-runtime managed \
  --manifest "$HOME/Downloads/media-runtime-manifest.json" \
  --signature "$HOME/Downloads/media-runtime-manifest.json.sig" \
  --public-key "$(cat "$HOME/Downloads/media-runtime-root-public-key.txt")" \
  --non-interactive --confirm-download
sudo launchctl kickstart -k system/com.visto.server
curl -fsS http://127.0.0.1:8787/health/ready
)
```

就绪结果应包含 `"ffmpeg":true` 和 `"ffprobe":true`。若签名、哈希或能力检查失败，管理器不会切换现有运行时。

### 这份运行时是签名并公开可回读的（2026-09-24 实测）

清单与签名已在未登录状态下回读并验签；固定公钥地址也已独立回读。归档 **14,716,872 字节**（R2），
SHA-256 为 `fc1c67b8bcd9f27ab0a16c6583fe2ed4e0c0807953733a7be87cf27a7d1e789e`，
与清单里声明的 `sha256` 一致；清单签名用本公开仓库的验签公钥验证通过。你可以自己核对：

```bash
curl -fL "https://dl.819101.xyz/media-runtime/ffmpeg-8.1.2-macos-lgpl.2/Visto-Media-Runtime_ffmpeg-8.1.2-macos-lgpl.2_macos-arm64.tar.xz" \
  -o "$HOME/Downloads/visto-media-runtime.tar.xz"
shasum -a 256 "$HOME/Downloads/visto-media-runtime.tar.xz"
```

公钥也随完整公开交付提供；下载失败时停止，核对固定公钥内容后再重试。不要改用视频归档附带的公钥。

## 卸载

默认卸载不会删除用户数据和备份：

```bash
sudo /Library/Visto/current/scripts/uninstall-visto-server.sh --yes
```

只有确认不再需要数据时才使用 `--purge-data`。
