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

	"golang.org/x/sync/errgroup"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/grpcserver"
	"atlas-backend-core/internal/logger"
	"atlas-backend-core/internal/repository"
	"atlas-backend-core/internal/router"
	"atlas-backend-core/internal/server"
	"atlas-backend-core/internal/service"
)

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
	userRepo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(userRepo)

	// REST API for the frontend.
	router.SetMode(cfg.App.Env)
	engine, err := router.InitRouter(router.Deps{
		Config:      cfg,
		Logger:      log,
		DB:          sqlDB,
		UserService: userSvc,
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

	// Run both servers; if either fails, the shared context is cancelled and the other
	// one shuts down gracefully too.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return httpSrv.Run(gctx) })
	if grpcSrv != nil {
		g.Go(func() error { return grpcSrv.Run(gctx) })
	}
	return g.Wait()
}
