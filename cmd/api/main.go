// Command api runs the ATLAS backend: the REST API (for the frontend) and the gRPC server
// (for other backend services) in one process, sharing the same services.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // the distroless image has no zoneinfo; dashboards use IANA time zones

	"golang.org/x/sync/errgroup"

	"atlas-backend-core/internal/aiclient"
	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/grpcclient"
	"atlas-backend-core/internal/grpcserver"
	"atlas-backend-core/internal/logger"
	"atlas-backend-core/internal/repository"
	"atlas-backend-core/internal/router"
	"atlas-backend-core/internal/server"
	"atlas-backend-core/internal/service"
	"atlas-backend-core/internal/worker"
)

// evidenceLimit is the number of representative opinions put in a consultation snapshot.
const evidenceLimit = 20

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logger.New(os.Stdout, cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	log = log.With(slog.String("app", cfg.App.Name), slog.String("env", cfg.App.Env))
	slog.SetDefault(log)

	if cfg.DB.AutoMigrate {
		if err := database.MigrateUp(cfg.DB.URL, log); err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
	}

	db, err := database.Open(ctx, cfg.DB, log)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer func() { err = errors.Join(err, database.Close(db)) }()
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	log.Info("database connected")

	// Wire dependencies: repository -> service -> transports (REST handlers, gRPC servers).
	tx := database.NewTransactor(db)
	userRepo := repository.NewUserRepository(db)
	shopRepo := repository.NewShopRepository(db)
	feedbackRepo := repository.NewFeedbackRepository(db)
	insightRepo := repository.NewInsightRepository(db)
	consultRepo := repository.NewConsultationRepository(db)
	memoryRepo := repository.NewMemoryRepository(db)
	tokens := auth.NewTokenManager(cfg.Auth)

	// Python AI service (Path A worker and Path B consultations).
	var ai service.AIClient = aiclient.Disabled{}
	if cfg.AI.Enabled() {
		conn, err := grpcclient.New(grpcclient.Config{Target: cfg.AI.Addr, AuthToken: cfg.AI.AuthToken, TLS: cfg.AI.TLS})
		if err != nil {
			return fmt.Errorf("connect ai service: %w", err)
		}
		defer func() { err = errors.Join(err, conn.Close()) }()
		ai = aiclient.New(conn, cfg.AI.FeedbackTimeout, cfg.AI.ConsultationTimeout)
		log.Info("ai service configured", slog.String("addr", cfg.AI.Addr))
	} else {
		log.Warn("AI_GRPC_ADDR is empty: feedbacks stay queued and consultations answer 503 until it is set")
	}

	processor := service.NewFeedbackProcessor(feedbackRepo, shopRepo, insightRepo, ai, tx, cfg.Worker.CandidateLimit)
	feedbackWorker := worker.NewFeedbackWorker(feedbackRepo, processor, cfg.Worker, log)
	var jobNotifier service.JobNotifier
	if cfg.AI.Enabled() {
		jobNotifier = feedbackWorker
	}

	userSvc := service.NewUserService(userRepo)
	authSvc := service.NewAuthService(userRepo, shopRepo, tx, tokens)
	shopSvc := service.NewShopService(shopRepo, userRepo, tx)
	feedbackSvc := service.NewFeedbackService(feedbackRepo, shopRepo, tx, jobNotifier)
	insightSvc := service.NewInsightService(insightRepo, shopRepo, consultRepo)
	consultSvc := service.NewConsultationService(consultRepo, insightRepo, shopRepo, shopRepo, memoryRepo, ai, tx,
		service.ConsultationConfig{
			AIEnabled:     cfg.AI.Enabled(),
			TurnTimeout:   cfg.AI.ConsultationTimeout + config.ConsultationOverhead,
			EvidenceLimit: evidenceLimit,
		})
	memorySvc := service.NewMemoryService(memoryRepo, shopRepo)

	// REST API for the frontend.
	router.SetMode(cfg.App.Env)
	engine, err := router.InitRouter(router.Deps{
		Config:              cfg,
		Logger:              log,
		DB:                  sqlDB,
		Tokens:              tokens,
		AuthService:         authSvc,
		ShopService:         shopSvc,
		FeedbackService:     feedbackSvc,
		InsightService:      insightSvc,
		ConsultationService: consultSvc,
		MemoryService:       memorySvc,
	})
	if err != nil {
		return fmt.Errorf("init router: %w", err)
	}
	// Walking the route table once at startup forces the routing trees to be fully built.
	log.Info("routes registered", slog.Int("count", len(engine.Routes())))
	httpSrv := server.New(cfg.HTTP, engine, log)

	// gRPC API for other backend services.
	var grpcSrv *grpcserver.Server
	if cfg.GRPC.Enabled {
		grpcSrv, err = grpcserver.New(grpcserver.Deps{
			Config:      cfg.GRPC,
			Production:  cfg.App.IsProduction(),
			Logger:      log,
			UserService: userSvc,
		})
		if err != nil {
			return fmt.Errorf("init grpc server: %w", err)
		}
	}

	// Run the servers and the worker; if one fails, the shared context is cancelled and the
	// others shut down gracefully too.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return httpSrv.Run(gctx) })
	if grpcSrv != nil {
		g.Go(func() error { return grpcSrv.Run(gctx) })
	}
	if cfg.AI.Enabled() {
		g.Go(func() error { return feedbackWorker.Run(gctx) })
	}
	return g.Wait()
}
