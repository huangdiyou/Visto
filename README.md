# Visto Server

**把素材、反馈和版本，放回同一个项目。**

Visto 是一个本地优先、自托管的媒体交付与审阅平台。你可以在自己的设备上组织视频和图片，为团队或客户创建审阅链接，在具体时间点或画面位置收集反馈，并保留每个版本的讨论与审阅结论。

适合视频制作、设计交付，以及需要围绕素材反复沟通的小团队。媒体和项目数据由你控制的部署主机与存储服务承载。

[使用手册](docs/USER_GUIDE.md) · [macOS 安装](docs/INSTALL_MACOS.md) · [发布页](https://github.com/huangdiyou/Visto/releases) · [贡献指南](CONTRIBUTING.md)

> **发布状态：首个正式版本正在准备中，当前尚无可下载的 GitHub Release。** 本仓库现有安装说明聚焦 Apple Silicon macOS。其他平台的源码和构建工具不代表已通过正式发布验收；支持范围、安装包和已知限制以实际 Release 为准。

## 从交付到确认

1. **建立项目**：为一次制作或交付组织素材，并按项目分配成员权限。
2. **上传素材**：添加图片、视频或新版本，在媒体处理能力可用时生成预览。
3. **发出审阅链接**：根据需要设置访问密码、有效期及评论、下载权限。
4. **收集具体反馈**：在视频时间点或图片位置留言，用评论线程继续讨论。
5. **推进新版本**：上传修改后的素材，处理反馈并记录审阅结论，让下一轮沟通有据可查。

## 核心能力

| 能力 | 可以做什么 |
| --- | --- |
| 项目协作 | 按项目组织媒体、管理成员和访问权限 |
| 媒体与版本 | 上传视频和图片，查看处理状态、预览和版本记录 |
| 精确反馈 | 视频时间点与时间区间评论，图片点位、区域与绘制批注 |
| 讨论与确认 | 回复评论、处理反馈，保留审阅结论和历史 |
| 外部审阅 | 通过受控分享链接邀请访客参与，设置密码、有效期与访问权限 |
| 存储连接 | 使用本地托管存储，配置 WebDAV 或 S3 兼容存储 |
| 主机运维 | 使用主机工具备份、恢复和回滚，查看故障与运行信息 |

视频探测、转码和预览依赖 FFmpeg/FFprobe。原生 Server 主程序包与媒体运行时分别分发；运行时的可用性会影响媒体处理能力。

## 开始使用

### 部署用户

正式安装包发布后，从 [GitHub Releases](https://github.com/huangdiyou/Visto/releases) 下载对应版本和 SHA-256 校验文件，按该版本的说明安装。

当前 Apple Silicon macOS 安装流程见 [安装指南](docs/INSTALL_MACOS.md)。安装后的默认访问地址是 `http://127.0.0.1:8787/`，首次打开时创建工作空间和 Owner 账号，再配置存储位置并建立项目。

安装指南中的媒体运行时下载与安装步骤仍待补齐。当前请将源码构建用于开发与评估，生产部署以正式发布说明为准。

Visto Server 通过浏览器提供工作界面，运行 Server 无需安装 Desktop 应用。

### 开发者：从源码构建

在 Apple Silicon macOS 上准备以下工具：

- Node.js **22.12.0 或以上**及 npm。
- Go **1.26.6 或以上**，与仓库的 `go.mod`、`go.work` 要求一致。
- Git，以及系统自带的 Bash、tar 和 SHA-256 校验工具。

克隆仓库并安装前端依赖：

```bash
git clone https://github.com/huangdiyou/Visto.git
cd Visto
npm ci
```

在仓库根目录验证并构建开发包：

```bash
npm run format:check
npm run check --workspaces --if-present
npm run test --workspaces --if-present
go test ./services/core/...
bash scripts/build-server-macos.sh 1.0.0-dev
```

构建脚本会编译 Core 与主机管理工具、构建 Web 静态资源，并生成安装包和 SHA-256 校验文件。产物位于 `dist/server/`。构建成功不等于安装与运行验收通过。

## 数据与访问

Visto 的业务数据存放在部署主机与配置的存储服务中。接入 WebDAV、S3 或向访客开放分享时，相应服务和获得授权的访问者会按你的配置访问媒体。

Owner 管理工作空间与全局设置；项目成员按项目权限协作。上传和审阅围绕具体项目展开，避免通过共享 Owner 账号进行日常协作。

建议先以本机访问完成初始化。向局域网或公网开放前，配置适当的网络访问控制与 HTTPS，并检查分享权限、存储权限及备份策略。

## 文档与支持

| 需要了解 | 文档 |
| --- | --- |
| 下载、校验与安装 | [macOS 安装](docs/INSTALL_MACOS.md) |
| 创建项目、上传、分享与审阅 | [用户手册](docs/USER_GUIDE.md) |
| 维护实例与保护已有数据 | [更新、备份与恢复](docs/UPDATES_BACKUP_RESTORE.md) |
| 处理安装和运行问题 | [故障排查](docs/TROUBLESHOOTING.md) |
| 提交问题需要哪些信息 | [支持说明](SUPPORT.md) |
| 查看版本变更 | [更新记录](CHANGELOG.md) |

报告使用问题时，请提供版本、操作系统、复现步骤与错误信息。不要附上数据库、原始媒体、访问令牌或存储凭据。安全漏洞请遵循 [安全策略](SECURITY.md)，避免在公开 Issue 中披露细节。

## 许可与贡献

Visto Server 源码采用 [Apache License 2.0](LICENSE)。第三方组件遵循各自许可证，相关声明见 [NOTICE](NOTICE) 与发行包中的第三方声明。

欢迎提出使用场景、报告可复现的问题，或提交聚焦的 Pull Request。开始前请阅读 [贡献指南](CONTRIBUTING.md)。
