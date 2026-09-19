# Contributing to Visto

感谢你参与 Visto。

## 开始之前

- Bug 请提供可复现步骤、Visto 版本和操作系统版本。
- 功能建议请说明实际使用场景，而不是只描述实现方式。
- 安全漏洞不要公开提交 Issue，请按 [SECURITY.md](SECURITY.md) 报告。

## 本地验证

提交 Pull Request 前，至少运行：

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
