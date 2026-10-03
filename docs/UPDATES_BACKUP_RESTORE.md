# 更新、备份与恢复

本页命令适用于 macOS 原生安装。Windows、Linux 和 Docker 的路径、备份格式与参数不同，请使用[完整手册](USER_GUIDE.md)中对应平台章节。

详细操作、覆盖范围与恢复后的检查见[维护指南](web/maintenance.html)和[备份指南](web/backup.html)。
命令中的占位符必须换成真实版本、路径及摘要。

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

更新包通过发布页公布的 **SHA-256** 校验完整性。正式稳定版本会发布安装包与对应摘要，安装前请核对
摘要与发布页一致。摘要校验证明「下载未损坏、不是拿错了文件」，不替代下载来源的可信度——请只从官方
Release 页下载。

当前原生更新脚本还要求对应版本清单；省略 `--manifest` 时默认读取 `<安装包>.manifest.json`。
安装包、摘要与清单版本需对应同一 Release。缺少清单时停止并联系维护者，不猜测内容绕过缺失。

```bash
sudo /Library/Visto/current/scripts/update-visto-server.sh \
  --package '<安装包>' --sha256 '<发布页公布的64位摘要>' \
  --manifest '<该版本清单路径>'
```

在自动更新体验完善之前，请优先按照对应 Release 的安装说明更新，不要使用来源不明的安装包。

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
