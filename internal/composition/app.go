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
	"trackfund/internal/infrastructure/postgres"
	"trackfund/internal/infrastructure/projections"
	redisinfra "trackfund/internal/infrastructure/redis"
    "trackfund/internal/infrastructure/logger"

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
	Commands Commands
	Queries  Queries
	Auth     authapp.Service
	Logger   *logger.Logger
	Closer   func()
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

	app := wireApp(store, cachedReadModel, authRepo, cfg.JWTSecret, func() {
		_ = redisClient.Close()
		db.Close()
	})
	app.Logger = lg
	return app, nil
}

func NewInMemoryApp() *App {
	lg := logger.New()
	store := eventstore.NewInMemoryEventStore()
	authRepo := eventstore.NewInMemoryAuthRepository()
	fundSummaryReadModel := projections.NewInMemoryFundSummaryReadModel()

	app := wireApp(store, fundSummaryReadModel, authRepo, "test-secret", func() {})
	app.Logger = lg
	return app
}

func wireApp(eventStore ports.EventStore, fundSummaryReadModel fundsapp.FundSummaryReadModel, authRepo authapp.Repository, jwtSecret string, closer func()) *App {
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
		Closer: closer,
	}
}
