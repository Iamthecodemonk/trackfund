package composition

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"

	authapp "trackfund/internal/application/auth"
	fundsapp "trackfund/internal/application/funds"
	"trackfund/internal/application/ports"
	"trackfund/internal/config"
	"trackfund/internal/infrastructure/eventstore"
	"trackfund/internal/infrastructure/logger"
	monoclient "trackfund/internal/infrastructure/mono"
	"trackfund/internal/infrastructure/postgres"
	"trackfund/internal/infrastructure/projections"
	redisinfra "trackfund/internal/infrastructure/redis"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Commands struct {
	CreateFund         fundsapp.CreateFundHandler
	RecordContribution fundsapp.RecordContributionHandler
}

type Queries struct {
	GetFundSummary fundsapp.GetFundSummaryHandler
}

type App struct {
	Commands   Commands
	Queries    Queries
	Auth       authapp.Service
	Logger     *logger.Logger
	Closer     func()
	MonoRepo   *postgres.MonoRepository
	MonoClient *monoclient.Client
	Config     config.Config
}

func NewApp(ctx context.Context, cfg config.Config) (*App, error) {
	lg := logger.New()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := postgres.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	redisClient := goredis.NewClient(&goredis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPass,
		DB:       cfg.RedisDB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		db.Close()
		return nil, err
	}

	store := postgres.NewEventStore(db)
	authRepo := postgres.NewAuthRepository(db)
	dbReadModel := postgres.NewFundSummaryReadModel(db)
	cachedReadModel := redisinfra.NewFundSummaryCache(redisClient, dbReadModel, 5*time.Minute)

	// create email sender from config; fallback to log sender when SMTP not configured
	var emailSender authapp.EmailSender = authapp.LogEmailSender{}
	if cfg.SMTPHost != "" {
		emailSender = authapp.NewSMTPEmailSender(cfg)
	}

	monoRepo := postgres.NewMonoRepository(db)
	monoClient := monoclient.NewClientWithTimeout(cfg.MonoBaseURL, cfg.MonoSecretKey, cfg.MonoPublicKey, time.Duration(cfg.MonoTimeoutSecs)*time.Second)
	monoClient.Debug = cfg.MonoDebugLogging && cfg.Env != "production"
	// create background context and cancel
	bgCtx, cancel := context.WithCancel(context.Background())
	app := wireApp(store, cachedReadModel, authRepo, monoRepo, monoClient, cfg.JWTSecret, func() {
		cancel()
		_ = redisClient.Close()
		db.Close()
	})
	// start Asynq worker and enqueue background sync every 5 minutes
	if client, err := StartAsynqWorker(bgCtx, cfg.RedisAddr, app); err == nil {
		go startMonoSync(bgCtx, app, client, 5*time.Minute)
	}
	app.Logger = lg
	app.Config = cfg
	// replace auth service with configured email sender
	app.Auth = authapp.NewService(authRepo, emailSender, authapp.Config{
		JWTSecret:         cfg.JWTSecret,
		AccessTokenTTL:    15 * time.Minute,
		RefreshTokenTTL:   30 * 24 * time.Hour,
		OTPTTL:            10 * time.Minute,
		OTPRateLimitCount: 3,
		OTPRateLimitEvery: time.Hour,
	})

	return app, nil
}

func NewInMemoryApp() *App {
	lg := logger.New()
	store := eventstore.NewInMemoryEventStore()
	authRepo := eventstore.NewInMemoryAuthRepository()
	fundSummaryReadModel := projections.NewInMemoryFundSummaryReadModel()

	app := wireApp(store, fundSummaryReadModel, authRepo, nil, nil, "test-secret", func() {})
	app.Logger = lg
	app.Config = config.Config{Env: "test"}
	return app
}

func wireApp(
	eventStore ports.EventStore,
	fundSummaryReadModel fundsapp.FundSummaryReadModel,
	authRepo authapp.Repository, monoRepo *postgres.MonoRepository,
	monoClient *monoclient.Client,
	jwtSecret string, closer func()) *App {
	projector := projections.NewFundSummaryProjector(fundSummaryReadModel)
	projector.Attach(eventStore)

	return &App{
		Commands: Commands{
			CreateFund:         fundsapp.NewCreateFundHandler(eventStore),
			RecordContribution: fundsapp.NewRecordContributionHandler(eventStore),
		},
		Queries: Queries{
			GetFundSummary: fundsapp.NewGetFundSummaryHandler(fundSummaryReadModel),
		},
		Auth: authapp.NewService(authRepo, authapp.LogEmailSender{}, authapp.Config{
			JWTSecret:         jwtSecret,
			AccessTokenTTL:    15 * time.Minute,
			RefreshTokenTTL:   30 * 24 * time.Hour,
			OTPTTL:            10 * time.Minute,
			OTPRateLimitCount: 3,
			OTPRateLimitEvery: time.Hour,
		}),
		MonoRepo:   monoRepo,
		MonoClient: monoClient,
		Closer:     closer,
	}
}
