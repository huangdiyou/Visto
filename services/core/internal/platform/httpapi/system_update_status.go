package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"review-studio.local/core/internal/serverupdate"
)

// the Owner update surface is strictly informational. Core never
// downloads an update package, never installs one, and never runs a host
// command on behalf of a web request. The response only explains what an
// administrator has to run on the deployment host.
//
// Free tier boundary (docs/FREE_TIER_BOUNDARY_DESIGN.md): the update channel is
// unsigned and carries a version number only. Nothing in the manifest can name
// a download location, so the fetch location below is compiled in.

const (
	updateStatusUnchecked     = "unchecked"
	updateStatusNotConfigured = "not_configured"
	updateStatusUnavailable   = "unavailable"
	updateStatusAvailable     = "available"

	// Version guard verdicts. "unknown" is deliberately distinct from
	// "up_to_date": when a version cannot be compared the page must not claim
	// the instance is current.
	updateStateUpToDate        = "up_to_date"
	updateStateUpdateAvailable = "update_available"
	updateStateUnknown         = "unknown"

	updateCommandPlatformDocker        = "docker"
	updateCommandPlatformDockerWindows = "docker-windows"
	updateCommandPlatformWindowsServer = "windows-server"
	updateCommandPlatformLinuxServer   = "linux-server"
	updateCommandPlatformMacOSServer   = "macos-server"
	updateCommandPlatformSource        = "source"
)

// officialReleasePage is the only download location the free tier points at. It
// is compiled in on purpose: the unsigned manifest must never be able to change
// where an operator is told to fetch a package from.
const officialReleasePage = "https://github.com/huangdiyou/Visto/releases"

// Native package install prefixes. They match the directory contract the
// platform update scripts are built against.
const (
	linuxServerPrefixPath = "/opt/visto"
	macOSServerPrefixPath = "/Library/Visto"
)

// Placeholders are intentionally explicit. The version placeholder is typed by
// the administrator after reading the official release page; it is never
// interpolated from the manifest, so a manifest cannot steer the download.
const (
	placeholderCoreImage       = "<核心镜像>@sha256:<从发布页复制的摘要>"
	placeholderWebImage        = "<Web 镜像>@sha256:<从发布页复制的摘要>"
	placeholderWindowsPackage  = "Visto-Server_<版本>_windows-x64.zip"
	placeholderUnixPackage     = "Visto-Server_<版本>_<平台>.tar.gz"
	placeholderSHA256          = "<从 .sha256 文件复制的 64 位摘要>"
	placeholderPreviousVersion = "<上一个版本>"
)

// Native packages are staged in one directory that survives a reboot and is not
// shared with other users' temporary files.
const unixUpdateStageDir = "/var/tmp/visto-update"

type systemUpdateStatusResponse struct {
	Channel        string                `json:"channel"`
	CurrentVersion string                `json:"currentVersion"`
	Deployment     string                `json:"deployment"`
	Checked        bool                  `json:"checked"`
	Policy         systemUpdatePolicy    `json:"policy"`
	Sources        []string              `json:"sources"`
	Check          systemUpdateCheck     `json:"check"`
	Latest         *systemUpdateRelease  `json:"latest"`
	Commands       []systemUpdateCommand `json:"commands"`
	Offline        systemUpdateOffline   `json:"offline"`
	SecurityNotes  []string              `json:"securityNotes"`
}

type systemUpdatePolicy struct {
	// WebInstallSupported is always false for the free Server: updates are a
	// host administrator action, never a browser action.
	WebInstallSupported bool `json:"webInstallSupported"`
	// SilentUpdate stays false so the page can state that nothing updates in
	// the background without an administrator.
	SilentUpdate         bool `json:"silentUpdate"`
	HostAdminRequired    bool `json:"hostAdminRequired"`
	DownloadsPackage     bool `json:"downloadsPackage"`
	ExecutesHostCommands bool `json:"executesHostCommands"`
}

type systemUpdateCheck struct {
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checkedAt"`
	Message   string     `json:"message"`
}

// systemUpdateRelease carries the version announcement. It has no artifact list
// and no download location: the free tier channel is unsigned, so a location
// taken from it could be rewritten by anyone who can rewrite the source.
type systemUpdateRelease struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt"`
	Source      string `json:"source"`
	State       string `json:"state"`
}

type systemUpdateCommand struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Platform    string `json:"platform"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

type systemUpdateOffline struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
}

type updateCheckState struct {
	status    string
	checkedAt time.Time
	message   string
	source    string
	manifest  *serverupdate.Manifest
}

type updateCheckCache struct {
	mutex sync.Mutex
	state *updateCheckState
}

func (h *handler) handleSystemUpdateStatus(
	response http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := h.requireOwner(response, request); !ok {
		return
	}
	state := h.currentUpdateCheckState(request, request.URL.Query().Get("check") == "1")
	writeJSON(response, http.StatusOK, h.toSystemUpdateStatusResponse(state))
}

// currentUpdateCheckState reuses the last manifest fetch until an Owner
// explicitly asks for a new one. A page load never reaches out to a public
// platform by itself, so air-gapped deployments stay quiet.
func (h *handler) currentUpdateCheckState(request *http.Request, force bool) updateCheckState {
	h.updateCheckCache.mutex.Lock()
	defer h.updateCheckCache.mutex.Unlock()
	if cached := h.updateCheckCache.state; cached != nil && !force {
		return *cached
	}
	state := h.refreshUpdateCheckState(request)
	h.updateCheckCache.state = &state
	return state
}

func (h *handler) refreshUpdateCheckState(request *http.Request) updateCheckState {
	if len(h.updateSources) == 0 {
		// No outbound request happened, so there is no check timestamp to
		// report; the page must not look like a fresh check succeeded.
		return updateCheckState{
			status:  updateStatusNotConfigured,
			message: "当前部署没有配置公共更新源，页面只提供官方发布页、复制命令和离线更新指引。",
		}
	}
	result, err := serverupdate.Check(request.Context(), serverupdate.CheckConfig{
		Sources: h.updateSources,
	})
	if err != nil {
		// The raw error names sources and HTTP status codes; keep it in the log
		// and give the page a stable, non-technical message.
		if h.logger != nil {
			h.logger.Warn(
				"system update check failed",
				"request_id", requestContextID(request),
				"error", err,
			)
		}
		return updateCheckState{
			status:    updateStatusUnavailable,
			checkedAt: time.Now().UTC(),
			message:   "无法从已配置的更新源取得版本清单。请稍后重试，或改用官方发布页与离线更新包。",
		}
	}
	manifest := result.Manifest
	return updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		message:   "已从更新源取得版本清单。",
		source:    result.Source,
		manifest:  &manifest,
	}
}

func (h *handler) toSystemUpdateStatusResponse(
	state updateCheckState,
) systemUpdateStatusResponse {
	response := systemUpdateStatusResponse{
		Channel:        "stable",
		CurrentVersion: h.version,
		Deployment:     h.deploymentKind,
		Checked:        state.status == updateStatusAvailable || state.status == updateStatusUnavailable,
		Policy: systemUpdatePolicy{
			WebInstallSupported:  false,
			SilentUpdate:         false,
			HostAdminRequired:    true,
			DownloadsPackage:     false,
			ExecutesHostCommands: false,
		},
		Sources: append([]string(nil), h.updateSources...),
		Check: systemUpdateCheck{
			Status:  state.status,
			Message: state.message,
		},
		Commands:      h.relevantUpdateCommands(state),
		Offline:       buildOfflineUpdateGuidance(),
		SecurityNotes: buildUpdateSecurityNotes(),
	}
	if !state.checkedAt.IsZero() {
		checkedAt := state.checkedAt.UTC()
		response.Check.CheckedAt = &checkedAt
	}
	if state.manifest != nil {
		response.Latest = toSystemUpdateRelease(*state.manifest, state.source, h.version)
	}
	return response
}

func toSystemUpdateRelease(
	manifest serverupdate.Manifest,
	source string,
	currentVersion string,
) *systemUpdateRelease {
	return &systemUpdateRelease{
		Version:     manifest.Version,
		PublishedAt: manifest.PublishedAt.UTC().Format(time.RFC3339),
		Source:      source,
		State:       versionGuardState(currentVersion, manifest.Version),
	}
}

// versionGuardState classifies the published version against the running one.
//
// Downgrade guard, docs/FREE_TIER_BOUNDARY_DESIGN.md §1.2: only a strictly newer
// published version counts as an available update, and a version that cannot be
// compared on either side is reported as unknown rather than as up to date.
func versionGuardState(currentVersion, publishedVersion string) string {
	comparison, err := serverupdate.CompareVersions(currentVersion, publishedVersion)
	if err != nil {
		return updateStateUnknown
	}
	if comparison < 0 {
		return updateStateUpdateAvailable
	}
	return updateStateUpToDate
}

// relevantUpdateCommands returns actionable update commands only when the
// published version is strictly newer than the running one.
//
// Downgrade guard, docs/FREE_TIER_BOUNDARY_DESIGN.md D1: an instance at or ahead
// of the published version must not be shown a command that would walk it
// backwards, and the page would otherwise say "已是最新" while still prompting an
// update. When no manifest was obtained at all there is no version claim to act
// on, so the generic guidance is kept.
func (h *handler) relevantUpdateCommands(state updateCheckState) []systemUpdateCommand {
	if state.manifest != nil &&
		versionGuardState(h.version, state.manifest.Version) != updateStateUpdateAvailable {
		return nil
	}
	return h.buildUpdateCommands()
}

func (h *handler) buildUpdateCommands() []systemUpdateCommand {
	switch h.deploymentKind {
	case serverupdate.DeploymentDocker:
		return buildDockerUpdateCommands()
	case serverupdate.DeploymentWindowsServer:
		return buildWindowsServerUpdateCommands()
	case serverupdate.DeploymentLinuxServer:
		return buildUnixServerUpdateCommands(linuxServerProfile)
	case serverupdate.DeploymentMacOSServer:
		return buildUnixServerUpdateCommands(macOSServerProfile)
	default:
		return buildSourceUpdateCommands()
	}
}

func buildDockerUpdateCommands() []systemUpdateCommand {
	commands := []systemUpdateCommand{
		{
			ID:          "docker-backup",
			Label:       "更新前备份数据卷",
			Platform:    updateCommandPlatformDocker,
			Command:     "./scripts/docker/backup-visto-docker.sh --backup-dir ./backups",
			Description: "在部署主机执行。备份会先停止 Core、归档整个数据卷，再启动 Core 并等待健康检查。",
		},
		{
			ID:       "docker-download",
			Label:    "从官方发布页取得镜像摘要",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"# 打开官方发布页，记下本版本 core 与 web 镜像的 @sha256 摘要：",
				"# " + officialReleasePage,
			}, "\n"),
			Description: "免费版清单不携带产物摘要。镜像引用必须带 @sha256，不要使用 latest 等可变标签。",
		},
		{
			ID:       "docker-update",
			Label:    "拉取固定镜像并重建服务",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"./scripts/docker/update-visto-docker.sh \\",
				"  --core-image '" + placeholderCoreImage + "' \\",
				"  --web-image '" + placeholderWebImage + "' \\",
				"  --backup-dir ./backups",
			}, "\n"),
			Description: "把两个占位符替换为发布页上的固定摘要后再执行。脚本先备份、再拉取、最后等待 Core 健康检查；失败会恢复更新前的数据和镜像。",
		},
		{
			ID:       "docker-manual",
			Label:    "不用脚本的等价命令",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"docker pull '" + placeholderCoreImage + "'",
				"docker pull '" + placeholderWebImage + "'",
				"VISTO_CORE_IMAGE='" + placeholderCoreImage + "' VISTO_WEB_IMAGE='" + placeholderWebImage + "' \\",
				"  docker compose -f compose.yaml -p visto up -d --no-build",
			}, "\n"),
			Description: "先执行上面的备份命令。只使用 docker compose pull 与 up -d，不要用 down 扩大停机时间。",
		},
		{
			ID:       "docker-health",
			Label:    "确认更新结果",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"docker compose -f compose.yaml -p visto ps",
				"docker compose -f compose.yaml -p visto logs --tail 80 core",
			}, "\n"),
			Description: "Core 容器健康后再继续使用；健康检查失败时按备份文件回滚。",
		},
	}
	return append(commands, buildDockerWindowsUpdateCommands()...)
}

func buildDockerWindowsUpdateCommands() []systemUpdateCommand {
	return []systemUpdateCommand{
		{
			ID:          "docker-windows-backup",
			Label:       "Windows 主机：更新前备份",
			Platform:    updateCommandPlatformDockerWindows,
			Command:     "./scripts/docker/Backup-VistoDocker.ps1 -BackupDirectory D:\\Visto-backups",
			Description: "在装有 Docker Desktop 的 Windows 主机上，用随附 PowerShell 脚本完成同样的备份。",
		},
		{
			ID:       "docker-windows-update",
			Label:    "Windows 主机：更新脚本",
			Platform: updateCommandPlatformDockerWindows,
			Command: strings.Join([]string{
				"./scripts/docker/Update-VistoDocker.ps1 \\",
				"  -CoreImage '" + placeholderCoreImage + "' \\",
				"  -WebImage '" + placeholderWebImage + "' \\",
				"  -BackupDirectory D:\\Visto-backups",
			}, "\n"),
			Description: "镜像摘要同样从官方发布页取得，必须带 @sha256；失败会自动恢复更新前的数据和镜像。",
		},
	}
}

func buildWindowsServerUpdateCommands() []systemUpdateCommand {
	return []systemUpdateCommand{
		{
			ID:          "windows-backup",
			Label:       "更新前备份数据与程序",
			Platform:    updateCommandPlatformWindowsServer,
			Command:     ".\\Backup-VistoServer.ps1 -BackupDirectory \"$env:ProgramData\\Visto\\backups\"",
			Description: "更新脚本本身也会先备份，这里给出独立备份命令，便于在重大版本更新前额外留一份。",
		},
		{
			ID:       "windows-download",
			Label:    "从官方发布页下载更新包与校验文件",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				"# 在浏览器打开官方发布页，把 <版本> 换成页面上的版本号：",
				"# " + officialReleasePage,
				"Invoke-WebRequest -Uri '" + officialReleasePage + "/download/v<版本>/" + placeholderWindowsPackage + "' -OutFile '.\\" + placeholderWindowsPackage + "'",
				"Invoke-WebRequest -Uri '" + officialReleasePage + "/download/v<版本>/" + placeholderWindowsPackage + ".sha256' -OutFile '.\\" + placeholderWindowsPackage + ".sha256'",
			}, "\n"),
			Description: "两个文件必须来自同一次发布、同一个版本。离线环境请改用可移动介质复制，不要改动文件名或内容。",
		},
		{
			ID:       "windows-verify",
			Label:    "校验下载的包",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				".\\bin\\visto-server.exe update verify-package \\",
				"  --artifact '.\\" + placeholderWindowsPackage + "' \\",
				"  --kind windows-server --platform windows-amd64 \\",
				"  --sha256 '" + placeholderSHA256 + "'",
			}, "\n"),
			Description: "只证明下载未损坏、不是拿错了文件；免费版不再做签名校验，因此它不能证明来源可信。",
		},
		{
			ID:       "windows-apply",
			Label:    "备份、停服、应用更新",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				".\\Update-VistoServer.ps1 \\",
				"  -PackageArchive '.\\" + placeholderWindowsPackage + "' \\",
				"  -Sha256 '" + placeholderSHA256 + "'",
			}, "\n"),
			Description: "脚本先备份数据、证明服务已停止，再切换版本、启动并等待健康检查；无法证明停服或健康检查失败时自动回滚。",
		},
		{
			ID:       "windows-health",
			Label:    "确认更新结果",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				".\\bin\\visto-server.exe doctor",
				"Get-ScheduledTask -TaskName 'Visto Server' | Select-Object TaskName, State",
			}, "\n"),
			Description: "服务恢复运行后核对版本与媒体引擎状态；失败时用回滚目录和备份归档恢复。",
		},
	}
}

// unixServerProfile carries everything that differs between the Linux and macOS
// native update paths. The release layout, the scripts and their verification
// order are identical, so both hosts share one command builder.
type unixServerProfile struct {
	platform             string
	prefix               string
	backupDirArg         string
	serviceStatusCommand string
}

var linuxServerProfile = unixServerProfile{
	platform:             updateCommandPlatformLinuxServer,
	prefix:               linuxServerPrefixPath,
	backupDirArg:         "/var/backups/visto",
	serviceStatusCommand: "systemctl status visto.service --no-pager",
}

var macOSServerProfile = unixServerProfile{
	platform:             updateCommandPlatformMacOSServer,
	prefix:               macOSServerPrefixPath,
	backupDirArg:         `"/Library/Application Support/Visto/backups"`,
	serviceStatusCommand: "launchctl print system/com.visto.server",
}

func buildUnixServerUpdateCommands(profile unixServerProfile) []systemUpdateCommand {
	scripts := profile.prefix + "/current/scripts"
	stagedPackage := unixUpdateStageDir + "/" + placeholderUnixPackage
	return []systemUpdateCommand{
		{
			ID:          profile.platform + "-backup",
			Label:       "更新前备份数据与配置",
			Platform:    profile.platform,
			Command:     "sudo " + scripts + "/backup-visto-server.sh --backup-dir " + profile.backupDirArg,
			Description: "更新脚本本身会先备份数据；重大版本更新前建议额外留一份，并把备份复制到另一块磁盘或可信位置。",
		},
		{
			ID:       profile.platform + "-download",
			Label:    "从官方发布页下载更新包与校验文件",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"# 在浏览器打开官方发布页，把 <版本> 与 <平台> 换成页面上的实际值：",
				"# " + officialReleasePage,
				"mkdir -p " + unixUpdateStageDir,
				"curl -fL '" + officialReleasePage + "/download/v<版本>/" + placeholderUnixPackage + "' -o '" + stagedPackage + "'",
				"curl -fL '" + officialReleasePage + "/download/v<版本>/" + placeholderUnixPackage + ".sha256' -o '" + stagedPackage + ".sha256'",
			}, "\n"),
			Description: "两个文件必须来自同一次发布，包名要匹配本机架构。离线环境用可移动介质复制到同一目录，命令不变。",
		},
		{
			ID:       profile.platform + "-verify",
			Label:    "校验下载的包",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"sudo " + profile.prefix + "/current/bin/visto-server update verify-package \\",
				"  --artifact '" + stagedPackage + "' \\",
				"  --kind " + profile.platform + " --platform <平台> \\",
				"  --sha256 '" + placeholderSHA256 + "'",
			}, "\n"),
			Description: "只证明下载未损坏、不是拿错了文件；免费版不再做签名校验，因此它不能证明来源可信。",
		},
		{
			ID:       profile.platform + "-apply",
			Label:    "备份、停服、应用更新",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"sudo " + scripts + "/update-visto-server.sh \\",
				"  --package '" + stagedPackage + "' \\",
				"  --sha256 '" + placeholderSHA256 + "'",
			}, "\n"),
			Description: "脚本先备份数据、证明服务已停止，再切换 current 到新 release、启动并等待健康检查；无法证明停服或健康检查失败时自动回滚。",
		},
		{
			ID:       profile.platform + "-confirm",
			Label:    "确认更新结果",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"readlink " + profile.prefix + "/current",
				profile.prefix + "/current/bin/visto-server doctor",
				profile.serviceStatusCommand,
			}, "\n"),
			Description: "current 指向新版本、doctor 没有阻断项、服务处于运行状态后再继续使用。",
		},
		{
			ID:       profile.platform + "-rollback",
			Label:    "回滚到上一个版本",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"sudo " + scripts + "/rollback-visto-server.sh \\",
				"  --to-version " + placeholderPreviousVersion + " \\",
				"  --confirm-rollback",
			}, "\n"),
			Description: "上一个 release 保留在 " + profile.prefix + "/releases。回滚会先备份数据、切换 current、重启服务并等待健康检查；失败报告写在 " +
				profile.prefix + "/recovery，不要手工删除该目录。",
		},
	}
}

func buildSourceUpdateCommands() []systemUpdateCommand {
	return []systemUpdateCommand{
		{
			ID:          "source-backup",
			Label:       "备份数据与配置",
			Platform:    updateCommandPlatformSource,
			Command:     "cp -a ./data ./backups/data-$(date +%Y%m%d-%H%M%S)",
			Description: "源码部署没有官方更新脚本，先完整备份 data 目录与环境变量再继续。",
		},
		{
			ID:       "source-update",
			Label:    "切换到发布标签并重启",
			Platform: updateCommandPlatformSource,
			Command: strings.Join([]string{
				"# 发布标签以官方发布页为准，把 <版本> 换成页面上的版本号：",
				"# " + officialReleasePage,
				"git fetch --tags --force",
				"git checkout v<版本>",
				"# 按部署方式重启 Core，例如 systemd 或进程管理器",
			}, "\n"),
			Description: "只签出官方发布仓库的发布标签，不要直接跟随开发分支。",
		},
	}
}

func buildOfflineUpdateGuidance() systemUpdateOffline {
	return systemUpdateOffline{
		Summary: "离线或受控网络环境不从本页面下载任何文件，管理员手工取得更新包后回到部署主机执行同样的命令。",
		Steps: []string{
			"在可联网的机器打开官方发布页，下载与目标版本对应的更新包和同名 .sha256 文件。",
			"用可移动介质或内部文件服务把两个文件复制到部署主机，保持文件名不变。",
			"用 update verify-package 核对包的大小与 SHA-256 与 .sha256 文件一致，再执行本页的更新命令。",
			"Docker 离线环境改用内部镜像仓库时，镜像引用必须保持同一 @sha256 摘要。",
			"免费版不对更新包做签名校验，因此更要坚持只从官方发布页取包，不要使用 latest 之类可变标签。",
		},
	}
}

func buildUpdateSecurityNotes() []string {
	return []string{
		"本页面只显示版本信息与命令，不会下载更新包、不会安装更新、也不会在服务器上执行任何命令。",
		"更新检查只读取公开的版本清单，不上传项目名、媒体路径、用户列表、存储凭据或诊断数据。",
		"免费版更新通道不签名，清单只提供版本号，不含下载地址；更新包一律从官方发布页取得。",
		"更新包只做完整性校验（大小与 SHA-256）。它不能证明来源可信，请始终从官方发布页下载。",
		"更新前必须在部署主机创建备份，并把备份复制到另一块磁盘或可信位置后再继续。",
		"更新脚本失败会保留上一个可启动版本、数据备份和诊断证据，不要手工删除 recovery 目录。",
	}
}
