<p align="center">
  <img src="docs/assets/visto-icon.svg" width="72" alt="Visto 标志">
</p>

<h1 align="center">Visto</h1>

<p align="center"><strong>把素材、版本和反馈，放在同一个项目里。</strong></p>

<p align="center">
  本地优先 · 自托管 · 媒体交付与审阅
</p>

<p align="center">
  <a href="https://visto.akahxh.top/docs/">阅读用户文档</a>
</p>

Visto Server 是一个免费、开源的媒体协作平台，适合视频制作、设计交付和需要反复审阅作品的团队。将它部署在自己的电脑或服务器上，即可通过浏览器组织项目素材、邀请他人审阅、收集定位到画面的反馈，并保留每次修改的版本与结论。

从初稿到确认版，文件、讨论和交付记录都围绕同一个项目展开。数据由你控制的部署主机与所连接的存储服务承载。

![Visto 外部审阅界面：查看作品、标记画面位置、提交评论和审阅结论](docs/assets/readme-review.png)

*真实产品界面，使用虚构项目与演示素材。*

## 为什么使用 Visto

### 反馈能找到具体位置

视频评论可以关联时间点或时间区间，图片可以使用点位、区域和绘制批注。讨论围绕选定的版本进行，修改时能回到具体画面查看上下文。

### 新版本保留旧记录

在同一资产下上传修订版本，保留历史文件、评论与审阅结论。每一轮审阅明确对应一个版本，方便核对修改，也方便回看之前的决定。

### 团队协作与外部审阅各有入口

团队成员按项目权限参与协作；客户或合作方通过分享链接进入审阅。根据交付需要设置访问密码、有效期以及评论、下载权限。

### 数据部署由你决定

使用部署主机的本地存储，也可以连接 WebDAV 或 S3 兼容服务。项目数据库与媒体的保存位置由部署配置决定；启用远程存储或远程访问时，相应数据会经过你选择的服务与网络。

## 从第一份素材到最终交付

1. **建立项目**：选择存储位置，添加成员，明确协作范围。
2. **上传素材**：添加视频或图片，等待处理完成后预览。
3. **发起审阅**：选择版本和参与者，需要外部反馈时创建分享链接。
4. **收集反馈**：围绕时间点或画面位置评论、回复，并提交审阅结论。
5. **提交新版**：在原资产下上传修订文件，以新版本继续下一轮审阅。
6. **确认与归档**：核对交付权限，保留版本和讨论记录，并按需备份。

首次使用可以跟随[第一个项目指南](https://visto.akahxh.top/docs/quick-start.html)，用一份演示素材走完这条流程。

## 核心能力

| 能力 | 用途 |
| --- | --- |
| 项目与成员 | 按项目组织素材、成员和协作权限 |
| 图片与视频 | 上传、预览、查看处理状态，管理同一资产的多个版本 |
| 画面反馈 | 视频时间点与时间区间评论，图片点位、区域和绘制批注 |
| 审阅结论 | 设置审阅参与者与通过条件，记录通过、需修改或拒绝 |
| 受控分享 | 配置访问密码、有效期以及评论、下载权限 |
| 存储连接 | 本地托管存储、WebDAV、S3 兼容存储 |
| 实例维护 | 主机侧更新、备份、恢复与回滚工具 |

## 开始使用

Visto Server 提供浏览器工作台，团队成员和审阅者通过浏览器访问。部署机器需要安装 Server；使用工作台无需安装 Desktop 应用。

| 部署方式 | 平台 | 安装文档 |
| --- | --- | --- |
| macOS 原生 | Apple Silicon | [macOS 安装](https://visto.akahxh.top/docs/install-macos.html) |
| Windows 原生 | x64 | [Windows 安装](https://visto.akahxh.top/docs/windows-install.html) |
| Linux 原生 | x86_64 / ARM64，使用 systemd | [Linux 安装](https://visto.akahxh.top/docs/linux-install.html) |
| Docker | Linux amd64 / ARM64 | [Docker 部署](https://visto.akahxh.top/docs/docker.html) |

安装方式、下载入口、系统要求和视频组件准备步骤统一维护在用户文档中。请先阅读[安装准备](https://visto.akahxh.top/docs/prepare.html)，再选择对应平台。

原生安装的默认访问地址为 `http://127.0.0.1:8787/`。首次打开后创建工作空间和 Owner 账号，再配置存储位置、建立项目并邀请成员。图片与视频的处理能力取决于媒体运行时状态，安装后可按文档检查 FFmpeg 与 ffprobe 是否就绪。

## 使用与维护文档

| 你想做什么 | 从这里开始 |
| --- | --- |
| 了解工作空间、项目、资产和版本 | [先理解 Visto](https://visto.akahxh.top/docs/start.html) |
| 上传文件、管理版本 | [上传素材](https://visto.akahxh.top/docs/upload.html) · [版本管理](https://visto.akahxh.top/docs/versions.html) |
| 发起审阅、收集客户反馈 | [审阅与反馈](https://visto.akahxh.top/docs/reviews.html) · [外部审阅指南](https://visto.akahxh.top/docs/guest.html) |
| 连接存储、分配成员权限 | [存储设置](https://visto.akahxh.top/docs/storage.html) · [成员与权限](https://visto.akahxh.top/docs/members.html) |
| 更新实例、保护已有数据 | [更新、备份与恢复](https://visto.akahxh.top/docs/backup.html) |
| 处理安装或运行问题 | [故障排查](https://visto.akahxh.top/docs/troubleshooting.html) |

## 部署与数据

Owner 负责工作空间和全局设置，项目主管与成员根据项目权限开展日常协作。建议为成员分配独立账号，避免共用 Owner 账号。

向局域网或公网开放实例前，请按[网络与安全说明](https://visto.akahxh.top/docs/network.html)配置访问入口、HTTPS 和权限。分享链接只开放交付所需的能力；项目归档保留协作记录，重要数据仍需单独备份。

## 参与项目

欢迎提交使用建议、报告可复现的问题，或贡献聚焦的改进。开发环境和提交方式见[贡献指南](CONTRIBUTING.md)。

报告问题时，请附上 Visto 版本、操作系统、复现步骤和错误信息。不要公开数据库、原始媒体、访问令牌或存储凭据；涉及安全漏洞时请遵循[安全策略](SECURITY.md)。

## 开源许可

Visto Server 源码采用 [Apache License 2.0](LICENSE)。FFmpeg 等第三方组件遵循各自许可证，相关声明见 [NOTICE](NOTICE) 和发行包中的第三方声明。
