package httpapi

import (
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"review-studio.local/core/internal/serverupdate"
)

// the Owner update surface is strictly informational. Core never
// downloads an update package, never installs one, and never runs a host
// command on behalf of a web request. The response only explains what an
// administrator has to run on the deployment host.

const (
	updateStatusUnchecked     = "unchecked"
	updateStatusNotConfigured = "not_configured"
	updateStatusUnavailable   = "unavailable"
	updateStatusAvailable     = "available"

	updateCommandPlatformDocker        = "docker"
	updateCommandPlatformDockerWindows = "docker-windows"
	updateCommandPlatformWindowsServer = "windows-server"
	updateCommandPlatformLinuxServer   = "linux-server"
	updateCommandPlatformMacOSServer   = "macos-server"
	updateCommandPlatformSource        = "source"
)

// Native package install prefixes. They match the directory contract the
// platform update scripts are built against.
const (
	linuxServerPrefixPath = "/opt/visto"
	macOSServerPrefixPath = "/Library/Visto"
)

// Placeholders are intentionally explicit: a command with a placeholder must be
// edited on the host, while a command carrying a real digest or URL can be run
// as copied.
const (
	placeholderCoreImage   = "<核心镜像>@sha256:<发布页记录的摘要>"
	placeholderWebImage    = "<Web 镜像>@sha256:<发布页记录的摘要>"
	placeholderPackageURL  = "<发布页记录的 windows-server ZIP 地址>"
	placeholderPackageFile = "Visto-Server_<版本>_windows-x64.zip"

	placeholderLinuxPackageURL = "<发布页记录的 linux-server tar.gz 地址>"
	placeholderMacOSPackageURL = "<发布页记录的 macos-server tar.gz 地址>"
	placeholderUpdateSource    = "<更新源>"
	placeholderPublicKey       = "<发布公钥>"
	placeholderPreviousVersion = "<上一个版本>"
)

// Native packages are staged in one directory that survives a reboot and is not
// shared with other users' temporary files. The update script copies the three
// release files into a private directory under <prefix>/recovery before it
// verifies them, so the staged copies only have to stay put until it runs.
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

type systemUpdateRelease struct {
	Version                 string                 `json:"version"`
	PublishedAt             string                 `json:"publishedAt"`
	MinimumSupportedVersion string                 `json:"minimumSupportedVersion"`
	ReleaseNotes            []string               `json:"releaseNotes"`
	SigningKeyID            string                 `json:"signingKeyId,omitempty"`
	Source                  string                 `json:"source"`
	UpToDate                bool                   `json:"upToDate"`
	Artifacts               []systemUpdateArtifact `json:"artifacts"`
}

type systemUpdateArtifact struct {
	Kind      string `json:"kind"`
	Platform  string `json:"platform"`
	URL       string `json:"url,omitempty"`
	Image     string `json:"image,omitempty"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
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

// currentUpdateCheckState reuses the last signed-manifest fetch until an Owner
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
		Sources:       h.updateSources,
		PublicKey:     h.updatePublicKey,
		RootPublicKey: h.updateRootPublicKey,
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
			message:   "无法从已配置的更新源取得受信任的签名清单。请稍后重试，或改用官方发布页与离线更新包。",
		}
	}
	manifest := result.Manifest
	return updateCheckState{
		status:    updateStatusAvailable,
		checkedAt: time.Now().UTC(),
		message:   "已从受信任的更新源取得签名清单。",
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
		Commands:      h.buildUpdateCommands(state.manifest),
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
	release := &systemUpdateRelease{
		Version:                 manifest.Version,
		PublishedAt:             manifest.PublishedAt.UTC().Format(time.RFC3339),
		MinimumSupportedVersion: manifest.MinimumSupportedVersion,
		ReleaseNotes:            append([]string(nil), manifest.ReleaseNotes...),
		SigningKeyID:            manifest.SigningKeyID,
		Source:                  source,
		UpToDate: strings.TrimSpace(manifest.Version) != "" &&
			strings.TrimSpace(manifest.Version) == strings.TrimSpace(currentVersion),
		Artifacts: make([]systemUpdateArtifact, 0, len(manifest.Artifacts)),
	}
	for _, artifact := range manifest.Artifacts {
		release.Artifacts = append(release.Artifacts, systemUpdateArtifact{
			Kind:      artifact.Kind,
			Platform:  artifact.Platform,
			URL:       artifact.URL,
			Image:     artifact.Image,
			SHA256:    artifact.SHA256,
			SizeBytes: artifact.SizeBytes,
		})
	}
	return release
}

func (h *handler) buildUpdateCommands(
	manifest *serverupdate.Manifest,
) []systemUpdateCommand {
	switch h.deploymentKind {
	case serverupdate.DeploymentDocker:
		return buildDockerUpdateCommands(manifest)
	case serverupdate.DeploymentWindowsServer:
		return buildWindowsServerUpdateCommands(manifest)
	case serverupdate.DeploymentLinuxServer:
		return buildLinuxServerUpdateCommands(manifest)
	case serverupdate.DeploymentMacOSServer:
		return buildMacOSServerUpdateCommands(manifest)
	default:
		return buildSourceUpdateCommands(manifest)
	}
}

const dockerCoreImageKind = "docker-core"
const dockerWebImageKind = "docker-web"

func buildDockerUpdateCommands(manifest *serverupdate.Manifest) []systemUpdateCommand {
	coreImage := artifactImage(manifest, dockerCoreImageKind)
	webImage := artifactImage(manifest, dockerWebImageKind)
	if coreImage == "" {
		coreImage = placeholderCoreImage
	}
	if webImage == "" {
		webImage = placeholderWebImage
	}
	commands := []systemUpdateCommand{
		{
			ID:          "docker-backup",
			Label:       "更新前备份数据卷",
			Platform:    updateCommandPlatformDocker,
			Command:     "./scripts/docker/backup-visto-docker.sh --backup-dir ./backups",
			Description: "在部署主机执行。备份会先停止 Core、归档整个数据卷，再启动 Core 并等待健康检查。",
		},
		{
			ID:       "docker-update",
			Label:    "拉取固定镜像并重建服务",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"./scripts/docker/update-visto-docker.sh \\",
				"  --core-image '" + coreImage + "' \\",
				"  --web-image '" + webImage + "' \\",
				"  --backup-dir ./backups",
			}, "\n"),
			Description: "脚本只接受发布清单记录的 @sha256 摘要，先备份、再拉取、最后等待 Core 健康检查；失败会恢复更新前的数据和镜像。",
		},
		{
			ID:       "docker-manual",
			Label:    "不用脚本的等价命令",
			Platform: updateCommandPlatformDocker,
			Command: strings.Join([]string{
				"docker pull '" + coreImage + "'",
				"docker pull '" + webImage + "'",
				"VISTO_CORE_IMAGE='" + coreImage + "' VISTO_WEB_IMAGE='" + webImage + "' \\",
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
	return append(commands, buildDockerWindowsUpdateCommands(coreImage, webImage)...)
}

func buildDockerWindowsUpdateCommands(coreImage, webImage string) []systemUpdateCommand {
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
				"  -CoreImage '" + coreImage + "' \\",
				"  -WebImage '" + webImage + "' \\",
				"  -BackupDirectory D:\\Visto-backups",
			}, "\n"),
			Description: "同样只接受 @sha256 摘要；失败会自动恢复更新前的数据和镜像。",
		},
	}
}

func buildWindowsServerUpdateCommands(
	manifest *serverupdate.Manifest,
) []systemUpdateCommand {
	packageURL := artifactURL(manifest, "windows-server", "windows-amd64")
	if packageURL == "" {
		packageURL = placeholderPackageURL
	}
	fileName := packageFileName(packageURL, placeholderPackageFile)
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
			Label:    "下载签名更新包与清单",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				"Invoke-WebRequest -Uri '" + packageURL + "' -OutFile '.\\" + fileName + "'",
				"Invoke-WebRequest -Uri '" + placeholderUpdateSource + "/latest.json' -OutFile '.\\latest.json'",
				"Invoke-WebRequest -Uri '" + placeholderUpdateSource + "/latest.json.sig' -OutFile '.\\latest.json.sig'",
			}, "\n"),
			Description: "三个文件必须来自同一次发布。离线环境请改用可移动介质复制，不要改动文件名或内容。",
		},
		{
			ID:       "windows-apply",
			Label:    "校验并应用更新",
			Platform: updateCommandPlatformWindowsServer,
			Command: strings.Join([]string{
				"$env:VISTO_SERVER_UPDATE_PUBLIC_KEY = '" + placeholderPublicKey + "'",
				".\\Update-VistoServer.ps1 \\",
				"  -PackageArchive '.\\" + fileName + "' \\",
				"  -ManifestFile '.\\latest.json' \\",
				"  -ManifestSignatureFile '.\\latest.json.sig'",
			}, "\n"),
			Description: "脚本用已安装包内的 visto-server.exe 复验 Ed25519 签名、大小和 SHA-256，再备份、停服、替换、健康检查；失败自动回滚。",
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

const (
	linuxServerArtifactKind = "linux-server"
	macOSServerArtifactKind = "macos-server"
)

// unixServerProfile carries everything that differs between the Linux and macOS
// native update paths. The release layout, the scripts and their verification
// order are identical, so both hosts share one command builder.
type unixServerProfile struct {
	platform             string
	prefix               string
	artifactKind         string
	artifactPlatform     string
	urlPlaceholder       string
	backupDirArg         string
	serviceStatusCommand string
}

// nativeArtifactPlatform maps the running Core binary onto the manifest platform
// key. Go's GOARCH already spells amd64 and arm64 the way the native update
// scripts do, and the deployment host is the host Core runs on.
func nativeArtifactPlatform(osName string) string {
	return osName + "-" + runtime.GOARCH
}

func buildLinuxServerUpdateCommands(manifest *serverupdate.Manifest) []systemUpdateCommand {
	return buildUnixServerUpdateCommands(manifest, unixServerProfile{
		platform:             updateCommandPlatformLinuxServer,
		prefix:               linuxServerPrefixPath,
		artifactKind:         linuxServerArtifactKind,
		artifactPlatform:     nativeArtifactPlatform("linux"),
		urlPlaceholder:       placeholderLinuxPackageURL,
		backupDirArg:         "/var/backups/visto",
		serviceStatusCommand: "systemctl status visto.service --no-pager",
	})
}

func buildMacOSServerUpdateCommands(manifest *serverupdate.Manifest) []systemUpdateCommand {
	return buildUnixServerUpdateCommands(manifest, unixServerProfile{
		platform:             updateCommandPlatformMacOSServer,
		prefix:               macOSServerPrefixPath,
		artifactKind:         macOSServerArtifactKind,
		artifactPlatform:     nativeArtifactPlatform("macos"),
		urlPlaceholder:       placeholderMacOSPackageURL,
		backupDirArg:         `"/Library/Application Support/Visto/backups"`,
		serviceStatusCommand: "launchctl print system/com.visto.server",
	})
}

func buildUnixServerUpdateCommands(
	manifest *serverupdate.Manifest,
	profile unixServerProfile,
) []systemUpdateCommand {
	scripts := profile.prefix + "/current/scripts"
	packageURL := artifactURL(manifest, profile.artifactKind, profile.artifactPlatform)
	if packageURL == "" {
		packageURL = profile.urlPlaceholder
	}
	// A package whose manifest and signature keep the default names lets the
	// update script find all three from --package alone.
	packageName := packageFileName(
		packageURL,
		"Visto-Server_<版本>_"+profile.artifactPlatform+".tar.gz",
	)
	stagedPackage := unixUpdateStageDir + "/" + packageName
	stagedManifest := stagedPackage + ".manifest.json"
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
			Label:    "下载签名更新包、清单与签名",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"mkdir -p " + unixUpdateStageDir,
				"curl -fL '" + packageURL + "' -o '" + stagedPackage + "'",
				"curl -fL '" + placeholderUpdateSource + "/latest.json' -o '" + stagedManifest + "'",
				"curl -fL '" + placeholderUpdateSource + "/latest.json.sig' -o '" + stagedManifest + ".sig'",
			}, "\n"),
			Description: "三个文件必须来自同一次发布，包名要匹配本机架构（" + profile.artifactPlatform +
				"）。离线环境用可移动介质把三个文件复制到同一目录，命令不变。",
		},
		{
			ID:       profile.platform + "-apply",
			Label:    "校验并应用更新",
			Platform: profile.platform,
			Command: strings.Join([]string{
				"sudo env VISTO_SERVER_UPDATE_PUBLIC_KEY='" + placeholderPublicKey + "' \\",
				"  " + scripts + "/update-visto-server.sh \\",
				"  --package '" + stagedPackage + "'",
			}, "\n"),
			Description: "脚本用已安装的 bin/visto-server 复验 Ed25519 签名、SHA-256、大小和包内目录白名单，再备份数据、停服、把 current 切到新 release、启动并等待健康检查。不需要手工替换程序文件。",
		},
		{
			ID:       profile.platform + "-verify",
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

func buildSourceUpdateCommands(manifest *serverupdate.Manifest) []systemUpdateCommand {
	version := "v<版本>"
	if manifest != nil && strings.TrimSpace(manifest.Version) != "" {
		version = "v" + strings.TrimSpace(manifest.Version)
	}
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
				"git fetch --tags --force",
				"git checkout " + version,
				"# 按部署方式重启 Core，例如 systemd 或进程管理器",
			}, "\n"),
			Description: "只签出官方发布标签，不要直接跟随开发分支。",
		},
	}
}

func buildOfflineUpdateGuidance() systemUpdateOffline {
	return systemUpdateOffline{
		Summary: "离线或受控网络环境不从本页面下载任何文件，管理员手工取得更新包后回到部署主机执行同样的命令。",
		Steps: []string{
			"在可联网的机器打开官方 Releases 页面，下载同一版本的更新包、latest.json 与 latest.json.sig。",
			"用可移动介质或内部文件服务把三个文件复制到部署主机，保持文件名不变。",
			"核对更新包的 SHA-256 与清单记录一致，再执行本页“校验并应用更新”命令。",
			"Docker 离线环境改用内部镜像仓库时，镜像引用必须保持同一 @sha256 摘要。",
			"不要因为离线就跳过签名校验，也不要使用 latest 之类可变标签。",
		},
	}
}

func buildUpdateSecurityNotes() []string {
	return []string{
		"本页面只显示版本信息与命令，不会下载更新包、不会安装更新、也不会在服务器上执行任何命令。",
		"更新检查只读取公开的版本清单，不上传项目名、媒体路径、用户列表、存储凭据或诊断数据。",
		"只信任 HTTPS 官方源和 Ed25519 签名清单；签名或 SHA-256 不通过时更新脚本必须拒绝继续。",
		"更新前必须在部署主机创建备份，并把备份复制到另一块磁盘或可信位置后再继续。",
		"Docker 更新使用发布清单记录的固定 digest，不要依赖 latest 等可变标签。",
		"更新脚本失败会保留上一个可启动版本、数据备份和诊断证据，不要手工删除 recovery 目录。",
	}
}

func artifactImage(manifest *serverupdate.Manifest, kind string) string {
	if manifest == nil {
		return ""
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != kind {
			continue
		}
		if image := strings.TrimSpace(artifact.Image); image != "" {
			return image
		}
	}
	return ""
}

func artifactURL(manifest *serverupdate.Manifest, kind, platform string) string {
	if manifest == nil {
		return ""
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != kind || artifact.Platform != platform {
			continue
		}
		if url := strings.TrimSpace(artifact.URL); url != "" {
			return url
		}
	}
	return ""
}

func packageFileName(packageURL, fallback string) string {
	if packageURL == "" || strings.HasPrefix(packageURL, "<") {
		return fallback
	}
	index := strings.LastIndex(packageURL, "/")
	if index < 0 || index == len(packageURL)-1 {
		return fallback
	}
	return packageURL[index+1:]
}
