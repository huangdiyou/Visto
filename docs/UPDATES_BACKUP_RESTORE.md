# 更新、备份与恢复

本页命令适用于 macOS 原生安装。Windows、Linux 和 Docker 的路径、备份格式与参数不同，请使用[完整手册](USER_GUIDE.md)中对应平台章节。

详细操作、覆盖范围与恢复后的检查见[维护指南](web/maintenance.html)和[备份指南](web/backup.html)。
示例中的文件路径需要换成实际位置。

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

备份归档与同名 `<归档>.json` 元数据必须一起保留。原生备份归档数据目录；数据目录外的
本机 / 外接盘媒体、WebDAV / S3 实际文件和主机部署配置需要独立备份，不能仅凭数据库内有记录
就认为全部原片已经归档。服务无法证明停止时脚本会中止，不绕过保护。

## 更新

从官方发布入口下载完整更新文件，把安装包、同名校验文件和版本清单放在一起。新版工具自动读取配套文件并校验，你只需要指定安装包：

```bash
sudo bash /Library/Visto/current/scripts/update-from-package.sh \
  --package '/实际路径/下载的安装包.tar.gz'
```

新工具随新版安装包提供。旧版实例没有该工具时，请按原版本的更新说明操作。更新失败时保留备份与错误，不手动覆盖正在使用的程序。

## 恢复

恢复会替换当前数据。先做只读检查：

```bash
sudo /Library/Visto/current/scripts/restore-visto-server.sh \
  --backup-file <备份文件> \
  --check-only
```

确认检查通过后，再使用 `--confirm-restore` 执行恢复。

恢复前先备份当前实例，并确认备份时间之后的更改会被替换。只读检查与确认恢复不能同时使用。
当前恢复保护要求原数据目录一致，路径或元数据不匹配时不手工覆盖数据绕过拒绝。恢复后检查项目、
版本、原片和外部存储，不能只看服务启动。

## 回滚

如果需要回到已安装的旧版本：

```bash
sudo /Library/Visto/current/scripts/rollback-visto-server.sh \
  --to-version <版本> \
  --confirm-rollback
```

回滚前同样会保护当前数据。不要手工修改 `/Library/Visto/current` 符号链接来绕过工具。
