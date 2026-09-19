# Apple Silicon macOS 安装

当前 Visto Server 的首发目标是 Apple Silicon macOS。

## 1. 下载并校验

从 GitHub Releases 下载：

- `Visto-Server_<版本>_macos-arm64.tar.gz`
- 对应的 `.sha256` 文件

在终端校验：

```bash
shasum -a 256 Visto-Server_<版本>_macos-arm64.tar.gz
```

确认输出与发布页提供的 SHA-256 一致后再继续。

## 2. 解压

```bash
mkdir -p ~/Downloads/visto-server
tar -xzf Visto-Server_<版本>_macos-arm64.tar.gz -C ~/Downloads/visto-server
cd ~/Downloads/visto-server
```

## 3. 安装

安装需要管理员权限：

```bash
sudo bash scripts/install-visto-server.sh \
  --version <版本> \
  --source-dir "$PWD"
```

默认监听 `127.0.0.1:8787`，程序安装到 `/Library/Visto`，数据默认位于
`/Library/Application Support/Visto/data`。

安装完成后打开：

```text
http://127.0.0.1:8787/
```

## 4. 首次初始化

按照浏览器中的首次运行向导创建工作空间和 Owner 账号。随后先配置存储位置，再创建第一个项目。

## 5. 视频处理

视频转码和预览需要 FFmpeg/ffprobe。Visto 的原生 Server 包不把 FFmpeg 直接塞进主程序包；兼容媒体运行时会独立提供并附带自己的许可证与来源说明。

首个公开版本发布前，媒体运行时的最终下载与安装步骤会在这里补齐。

## 卸载

默认卸载不会删除用户数据和备份：

```bash
sudo /Library/Visto/current/scripts/uninstall-visto-server.sh --yes
```

只有确认不再需要数据时才使用 `--purge-data`。
