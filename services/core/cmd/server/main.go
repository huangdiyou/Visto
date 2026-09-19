package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"review-studio.local/core/internal/audit"
	"review-studio.local/core/internal/authorization"
	"review-studio.local/core/internal/catalog"
	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/invitation"
	"review-studio.local/core/internal/job"
	"review-studio.local/core/internal/media"
	"review-studio.local/core/internal/mediaruntime"
	"review-studio.local/core/internal/notification"
	"review-studio.local/core/internal/platform/database"
	"review-studio.local/core/internal/platform/httpapi"
	"review-studio.local/core/internal/platform/webassets"
	"review-studio.local/core/internal/projectaccess"
	"review-studio.local/core/internal/projectmember"
	"review-studio.local/core/internal/projectstorage"
	"review-studio.local/core/internal/ratelimit"
	reviewdomain "review-studio.local/core/internal/review"
	"review-studio.local/core/internal/reviewtemplate"
	"review-studio.local/core/internal/secretstore"
	sharedomain "review-studio.local/core/internal/share"
	"review-studio.local/core/internal/storage"
	"review-studio.local/core/internal/systemsettings"
	"review-studio.local/core/internal/workspace"
	"review-studio.local/core/internal/workspacesettings"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	address := envOrDefault("REVIEW_STUDIO_ADDR", "127.0.0.1:8787")
	dataDir := envOrDefault("REVIEW_STUDIO_DATA_DIR", "data")
	hostManagementToken := strings.TrimSpace(os.Getenv("VISTO_HOST_MANAGEMENT_TOKEN"))
	if hostManagementToken == "" {
		logger.Error("VISTO_HOST_MANAGEMENT_TOKEN is required to start Core")
		os.Exit(1)
	}
	trustedProxyCIDRs := splitCommaSeparated(os.Getenv("REVIEW_STUDIO_TRUSTED_PROXIES"))

	db, err := database.Open(context.Background(), database.Config{
		Path: filepath.Join(dataDir, "review-studio.db"),
	})
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	systemSettingsService := systemsettings.NewService(
		systemsettings.NewSQLiteRepository(db),
	)
	networkSettings, networkSettingsErr := systemSettingsService.GetNetwork(context.Background())
	environmentForcesHTTPS := os.Getenv("REVIEW_STUDIO_REQUIRE_HTTPS") == "1"
	storedRequireRemoteHTTPS := networkSettingsErr == nil && networkSettings.RequireRemoteHTTPS
	requireRemoteHTTPS := environmentForcesHTTPS || storedRequireRemoteHTTPS
	if networkSettingsErr != nil {
		// Fail open on purpose: refusing remote plaintext because a settings read
		// failed would lock the Owner out of a LAN deployment with no recovery
		// path other than shell access to the host.
		logger.Warn(
			"system network settings unavailable; remote HTTPS enforcement stays off",
			"error", networkSettingsErr,
		)
	}
	if requireRemoteHTTPS {
		logger.Info(
			"remote access requires HTTPS; non-loopback plaintext API, join, "+
				"and share requests return 426. Loopback is unaffected.",
			"environment_forced", environmentForcesHTTPS,
			"stored_require_remote_https", storedRequireRemoteHTTPS,
		)
	} else {
		logger.Warn(
			"remote access accepts plaintext HTTP (product default). HTTP traffic is "+
				"not encrypted, so devices on the same network can observe management "+
				"cookies, share tokens, and review content. Recommended only on a "+
				"trusted LAN; enable \"remote access requires HTTPS\" in Owner settings "+
				"once a TLS gateway is in place.",
			"environment_forced", environmentForcesHTTPS,
			"stored_require_remote_https", storedRequireRemoteHTTPS,
		)
	}

	identityService := identity.NewService(identity.NewSQLiteRepository(db))
	secretKey, err := secretstore.LoadOrCreateKey(filepath.Join(dataDir, "secrets.key"))
	if err != nil {
		logger.Error("secret key initialization failed", "error", err)
		os.Exit(1)
	}
	secretStore, err := secretstore.NewEncryptedSQLiteStore(db, secretKey)
	if err != nil {
		logger.Error("secret store initialization failed", "error", err)
		os.Exit(1)
	}
	authorizationService := authorization.NewService()
	projectAccessService := projectaccess.NewService(
		projectaccess.NewSQLiteRepository(db),
	)
	projectMemberService := projectmember.NewService(
		projectmember.NewSQLiteRepository(db),
	)
	projectStorageService := projectstorage.NewService(
		projectstorage.NewSQLiteRepository(db),
	)
	memberService := workspace.NewService(workspace.NewSQLiteRepository(db))
	workspaceSettingsService := workspacesettings.NewService(
		workspacesettings.NewSQLiteRepository(db),
	)
	invitationService := invitation.NewService(
		invitation.NewSQLiteRepository(db),
		identityService,
	)
	catalogService := catalog.NewService(catalog.NewSQLiteRepository(db))
	storageRepository := storage.NewSQLiteRepository(db)
	storageService := storage.NewServiceWithDependencies(
		storageRepository,
		storage.NewDefaultProviderRegistry(),
		secretStore,
	)
	warnForeignInstanceStorageRoots(logger, storageRepository, dataDir)
	mediaRepository := media.NewSQLiteRepository(db)
	videoTempDir := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(videoTempDir, 0o700); err != nil {
		logger.Error("media temporary storage initialization failed", "error", err)
		os.Exit(1)
	}
	storageService.SetCopyTempDir(filepath.Join(videoTempDir, "storage-copy"))
	ffmpegCommand, ffprobeCommand, runtimeEncoder := "ffmpeg", "ffprobe", ""
	runtimeSelection, runtimeErr := (mediaruntime.Manager{Root: mediaruntime.DefaultRoot(), ServerVersion: version}).Resolve()
	if runtimeErr == nil {
		ffmpegCommand, ffprobeCommand, runtimeEncoder = runtimeSelection.Probe.FFmpeg, runtimeSelection.Probe.FFprobe, runtimeSelection.Probe.Encoder
	} else if !errors.Is(runtimeErr, os.ErrNotExist) {
		logger.Error("selected media runtime is unavailable; run the host runtime manager", "error", runtimeErr)
		os.Exit(1)
	}
	mediaProber := media.NewFFProberWithTempDir(ffprobeCommand, videoTempDir)
	mediaService := media.NewService(
		mediaRepository,
		storageService,
		mediaProber,
	)
	renditionStore, err := media.NewManagedStore(
		filepath.Join(dataDir, "renditions"),
	)
	if err != nil {
		logger.Error("rendition storage initialization failed", "error", err)
		os.Exit(1)
	}
	sourceStore, err := media.NewManagedSourceStore(
		filepath.Join(dataDir, "sources"),
	)
	if err != nil {
		logger.Error("managed source storage initialization failed", "error", err)
		os.Exit(1)
	}
	libraryService := media.NewLibraryServiceWithSourceStoreAndStorage(
		media.NewSQLiteLibraryRepository(db),
		sourceStore,
		storageService,
	)
	libraryService.SetMalwareScanner(media.NewEnvironmentMalwareScanner())
	videoProcessor := media.NewFFmpegVideoProcessor(ffmpegCommand, videoTempDir)
	videoProcessor.SetSoftwareEncoder(os.Getenv("REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER"))
	videoProcessor.SetAccelerationMode(os.Getenv("REVIEW_STUDIO_VIDEO_ACCELERATION"))
	if runtimeEncoder != "" {
		videoProcessor.SetSoftwareEncoder(runtimeEncoder)
	}
	renditionService := media.NewRenditionService(
		media.NewSQLiteRenditionRepository(db),
		mediaRepository,
		storageService,
		renditionStore,
		media.NewFFmpegImageProcessor(ffmpegCommand),
		videoProcessor,
		mediaProber,
	)
	jobService := job.NewService(job.NewSQLiteRepository(db))
	reviewService := reviewdomain.NewService(reviewdomain.NewSQLiteRepository(db))
	reviewTemplateService := reviewtemplate.NewService(
		reviewtemplate.NewSQLiteRepository(db),
	)
	rateLimitService := ratelimit.New(db)
	shareService := sharedomain.NewServiceWithSecretsAndRateLimits(
		sharedomain.NewSQLiteRepository(db),
		secretStore,
		rateLimitService,
	)
	auditService := audit.NewService(audit.NewSQLiteRepository(db))
	notificationService := notification.NewServiceWithDependencies(
		notification.NewSQLiteRepository(db),
		secretStore,
		notification.NewDefaultSender(),
	)
	worker := job.NewWorker(jobService, job.WorkerConfig{
		NodeID:            "embedded-core",
		NodeName:          "Embedded Core",
		NodeKind:          "embedded",
		SoftwareVersion:   version,
		MaxConcurrentJobs: 1,
		Executors: map[string]job.Executor{
			media.ProbeRootJobType: media.ProbeRootJobExecutor{
				Service: mediaService,
			},
			media.GenerateImageRenditionsJobType: media.GenerateImageRenditionsExecutor{
				Service: renditionService,
			},
			media.GenerateVideoRenditionsJobType: media.GenerateVideoRenditionsExecutor{
				Service: renditionService,
			},
			media.GenerateVideoEnhancementsJobType: media.GenerateVideoEnhancementsExecutor{
				Service: renditionService,
			},
			media.ProcessAssetVersionJobType: media.ProcessAssetVersionExecutor{
				Media:      mediaService,
				Renditions: renditionService,
				Library:    libraryService,
				Jobs:       jobService,
			},
			storage.CopyObjectJobType: storage.CopyTaskExecutor{
				Service: storageService,
			},
		},
		Logger: logger,
	})
	notificationWorker := notification.NewWorker(notificationService, notification.WorkerConfig{
		Logger: logger,
	})

	shutdownContext, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	go runPendingAttachmentCleanup(
		shutdownContext,
		reviewService,
		storageService,
		logger,
	)
	go runRateLimitCleanup(shutdownContext, rateLimitService, logger)
	if err := worker.Start(shutdownContext); err != nil {
		logger.Error("job worker initialization failed", "error", err)
		os.Exit(1)
	}
	notificationWorker.Start(shutdownContext)

	handler := httpapi.NewHandler(httpapi.Config{
		FFmpegCommand:       ffmpegCommand,
		FFprobeCommand:      ffprobeCommand,
		Version:             version,
		Logger:              logger,
		Identity:            identityService,
		Catalog:             catalogService,
		Storage:             storageService,
		Media:               mediaService,
		Library:             libraryService,
		Renditions:          renditionService,
		Jobs:                jobService,
		Reviews:             reviewService,
		ReviewTemplates:     reviewTemplateService,
		Shares:              shareService,
		Audit:               auditService,
		Notifications:       notificationService,
		Authorization:       authorizationService,
		ProjectAccess:       projectAccessService,
		ProjectMembers:      projectMemberService,
		ProjectStorage:      projectStorageService,
		Members:             memberService,
		Invitations:         invitationService,
		WorkspaceSettings:   workspaceSettingsService,
		SystemSettings:      systemSettingsService,
		RequireRemoteHTTPS:  requireRemoteHTTPS,
		VideoAcceleration:   videoProcessor.AccelerationSettings(),
		HostManagementToken: hostManagementToken,
		TrustedProxyCIDRs:   trustedProxyCIDRs,
		RateLimits:          rateLimitService,
		UpdateSources:       splitSemicolonSeparated(os.Getenv("VISTO_SERVER_UPDATE_SOURCES")),
		UpdatePublicKey:     os.Getenv("VISTO_SERVER_UPDATE_PUBLIC_KEY"),
		UpdateRootPublicKey: os.Getenv("VISTO_SERVER_UPDATE_ROOT_PUBLIC_KEY"),
		DeploymentKind:      os.Getenv("VISTO_SERVER_DEPLOYMENT_KIND"),
	})
	if webDir := os.Getenv("REVIEW_STUDIO_WEB_DIR"); webDir != "" {
		assets, openErr := os.OpenRoot(webDir)
		if openErr != nil {
			logger.Error("web asset directory is unavailable", "error", openErr)
			os.Exit(1)
		}
		defer assets.Close()
		handler = webassets.Handler(assets.FS(), handler)
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       90 * time.Second,
	}

	go func() {
		<-shutdownContext.Done()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("server shutdown failed", "error", err)
		}
		if err := worker.Stop(ctx); err != nil {
			logger.Error("job worker shutdown failed", "error", err)
		}
		if err := notificationWorker.Stop(ctx); err != nil {
			logger.Error("notification worker shutdown failed", "error", err)
		}
	}()

	logger.Info("review studio core started", "address", address, "version", version)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

// warnForeignInstanceStorageRoots reports local roots that still point into
// another instance's data directory.
//
// Restore only swaps the data directory; the absolute paths in
// local_path_secrets survive, so a restore into a different directory leaves
// this instance reading and writing the source instance's files with no visible
// symptom. Cross-directory restore is not a supported migration yet, so the
// operator gets an explicit warning at startup instead.
func warnForeignInstanceStorageRoots(
	logger *slog.Logger,
	repository *storage.SQLiteRepository,
	dataDir string,
) {
	locations, err := repository.ListLocalRootLocations(context.Background())
	if err != nil {
		logger.Warn("storage root inspection failed", "error", err)
		return
	}
	foreign := storage.DetectForeignInstanceRoots(
		locations,
		dataDir,
		storage.LooksLikeInstanceRoot,
	)
	if len(foreign) == 0 {
		return
	}
	details := make([]string, 0, len(foreign))
	for _, root := range foreign {
		details = append(details, root.DisplayName+" -> "+root.PathText)
	}
	logger.Warn(
		"local storage roots point into another Visto instance data directory; "+
			"this instance will read and write files owned by that instance. "+
			"Restoring into a different data directory is not a supported "+
			"migration: keep the instance on its original data directory, or "+
			"re-point the roots before serving traffic. Managed upload roots "+
			"self-correct on the next upload; roots created by an owner keep "+
			"the path they were given.",
		"data_directory", dataDir,
		"foreign_instance_root", foreign[0].InstanceRoot,
		"root_count", len(foreign),
		"roots", strings.Join(details, "; "),
	)
}

func runPendingAttachmentCleanup(
	parent context.Context,
	reviews *reviewdomain.Service,
	storageService *storage.Service,
	logger *slog.Logger,
) {
	if reviews == nil || storageService == nil {
		return
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		before := time.Now().UTC().Add(-24 * time.Hour)
		items, err := reviews.ExpiredPendingAttachments(ctx, before, 100)
		if err != nil {
			logger.Warn("list expired pending comment attachments failed", "error", err)
			return
		}
		for _, item := range items {
			adapter, adapterErr := storageService.Adapter(ctx, item.WorkspaceID, item.AuthorizedRootID)
			if adapterErr != nil {
				logger.Warn("resolve expired comment attachment storage failed", "attachment_id", item.ID, "error", adapterErr)
				continue
			}
			if deleteErr := adapter.Delete(ctx, item.ObjectKey); deleteErr != nil {
				logger.Warn("delete expired comment attachment object failed", "attachment_id", item.ID, "error", deleteErr)
				continue
			}
			if deleteErr := reviews.RemoveExpiredPendingAttachment(ctx, item.ID, before); deleteErr != nil {
				logger.Warn("delete expired comment attachment record failed", "attachment_id", item.ID, "error", deleteErr)
			}
		}
	}
	cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

func runRateLimitCleanup(parent context.Context, limits *ratelimit.Service, logger *slog.Logger) {
	if limits == nil {
		return
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(parent, time.Minute)
		defer cancel()
		removed, err := limits.Cleanup(ctx, time.Now().UTC().Add(-48*time.Hour), 1000)
		if err != nil {
			logger.Warn("clean expired rate limit buckets failed", "error", err)
			return
		}
		if removed > 0 {
			logger.Info("cleaned expired rate limit buckets", "count", removed)
		}
	}
	cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

// splitSemicolonSeparated matches the separator used by VISTO_SERVER_UPDATE_SOURCES;
// a comma is a valid character inside a URL path, so sources are split on ";".
func splitSemicolonSeparated(value string) []string {
	parts := strings.Split(value, ";")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
