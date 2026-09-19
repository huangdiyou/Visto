# Visto

Visto 是一个本地优先、自托管的媒体交付与审阅平台，用来组织项目素材、生成预览、分享审阅链接、收集评论并管理版本决策。

> 当前公开发布目标首先聚焦 **Apple Silicon macOS**。其他平台的源码或实验性支持不代表已经通过正式发布验收。

## 主要能力

- 项目与成员管理
- 图片、视频素材与版本管理
- 视频预览、时间点评论与附件
- 外部分享链接与访问控制
- 本地托管存储，以及可配置的远程存储
- 备份、恢复、回滚与签名更新

## 安装

Apple Silicon macOS 用户请从 GitHub Releases 下载当前版本，并阅读：

- [macOS 安装](docs/INSTALL_MACOS.md)
- [用户手册](docs/USER_GUIDE.md)
- [更新、备份与恢复](docs/UPDATES_BACKUP_RESTORE.md)
- [故障排查](docs/TROUBLESHOOTING.md)

## 从源码构建

需要 Node.js 22.12+、Go，以及 macOS 系统工具：

```bash
npm ci
npm run check --workspaces --if-present
npm run test --workspaces --if-present
go test ./services/core/...
./scripts/build-server-macos.sh 1.0.0
```

构建产物位于 `dist/server/`。

## 数据边界

Visto Server 运行在你控制的设备上。媒体文件、项目数据库和存储凭据由你的部署主机与配置的存储服务承载。公开仓库不包含 Visto 的发布私钥、内部基础设施凭据或用户数据。

## 开源与贡献

Visto Server 以 Apache License 2.0 发布。欢迎通过 Issue 和 Pull Request 报告问题或贡献改进。

- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)
- [支持说明](SUPPORT.md)
- [更新记录](CHANGELOG.md)
