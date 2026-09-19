# 更新、备份与恢复

## 更新前

更新前先创建备份。不要直接删除当前程序目录或数据目录。

已安装实例的运维脚本位于：

```text
/Library/Visto/current/scripts/
```

## 备份

```bash
sudo /Library/Visto/current/scripts/backup-visto-server.sh
```

备份脚本会在需要时停止服务，以获得一致的数据快照，并在完成后恢复服务。

## 更新

Visto 使用签名更新元数据验证更新包。正式稳定版本会通过 Visto 的 HTTPS 更新源提供签名清单。

在自动更新体验完善之前，请优先按照对应 Release 的安装说明更新，不要使用来源不明的安装包。

## 恢复

恢复会替换当前数据。先做只读检查：

```bash
sudo /Library/Visto/current/scripts/restore-visto-server.sh \
  --backup-file <备份文件> \
  --check-only
```

确认检查通过后，再使用 `--confirm-restore` 执行恢复。

## 回滚

如果需要回到已安装的旧版本：

```bash
sudo /Library/Visto/current/scripts/rollback-visto-server.sh \
  --to-version <版本> \
  --confirm-rollback
```

回滚前同样会保护当前数据。不要手工修改 `/Library/Visto/current` 符号链接来绕过工具。
