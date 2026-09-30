# Contributing to Visto

感谢你参与 Visto。

## 开始之前

- Bug 请提供可复现步骤、Visto 版本和操作系统版本。
- 功能建议请说明实际使用场景，而不是只描述实现方式。
- 安全漏洞不要公开提交 Issue，请按 [SECURITY.md](SECURITY.md) 报告。

## 本地验证

代码改动按影响范围选择以下检查；纯说明文档检查链接、内容与差异即可：

```bash
npm ci
npm run format:check
npm run check --workspaces --if-present
npm run test --workspaces --if-present
npm run build --workspace @review-studio/web
go test ./services/core/...
```

涉及 macOS Server 打包时再运行：

```bash
./scripts/build-server-macos.sh 1.0.0-dev
```

请保持改动聚焦，并为行为变化补充相应测试。


## 检查范围与验证责任

本仓库不在每次 push 或 PR 上自动重复运行全量测试。纯文档改动检查链接、内容与差异；
代码改动由维护者在隔离环境验证受影响范围后再合并。共享契约、权限、数据库、备份恢复等改动需扩大回归范围。

Actions 的 `checks` 可手动触发：默认验证 Web、Core 与主机 CLI 能独立构建，勾选 `tests` 后再执行业务测试。
维护者同步已验证源码时可以复用匹配的业务测试证据，但最终仓库仍需独立构建，确认没有遗漏文件或依赖。
未运行的检查不能记录为通过，发布前仍需对最终安装包完成对应平台验收。
