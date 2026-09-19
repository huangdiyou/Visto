const modeLabels: Record<string, string> = {
  local: "本地运行",
  desktop: "桌面节点",
  docker: "私有部署",
  cloud: "官方云",
};

const databaseLabels: Record<string, string> = {
  sqlite: "SQLite",
  postgres: "PostgreSQL",
};

export function formatRuntimeLabel(mode: string, database: string): string {
  return `${modeLabels[mode] ?? mode} · ${databaseLabels[database] ?? database}`;
}
