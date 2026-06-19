package redis

import (
	"context"
	"encoding/json"
	"time"

	goredis "github.com/redis/go-redis/v9"

	fundsapp "trackfund/internal/application/funds"
)

type FundSummaryCache struct {
	client   *goredis.Client
	fallback fundsapp.FundSummaryReadModel
	ttl      time.Duration
}

func NewFundSummaryCache(client *goredis.Client, fallback fundsapp.FundSummaryReadModel, ttl time.Duration) *FundSummaryCache {
	return &FundSummaryCache{
		client:   client,
		fallback: fallback,
		ttl:      ttl,
	}
}

func (c *FundSummaryCache) GetByID(id string) (fundsapp.FundSummary, bool, error) {
	ctx := context.Background()
	key := c.key(id)

	value, err := c.client.Get(ctx, key).Bytes()
	if err == nil {
		var summary fundsapp.FundSummary
		if err := json.Unmarshal(value, &summary); err == nil {
			return summary, true, nil
		}
	}
	if err != nil && err != goredis.Nil {
		return fundsapp.FundSummary{}, false, err
	}

	summary, ok, err := c.fallback.GetByID(id)
	if err != nil || !ok {
		return summary, ok, err
	}

	_ = c.set(ctx, key, summary)
	return summary, true, nil
}

func (c *FundSummaryCache) Save(summary fundsapp.FundSummary) error {
	if err := c.fallback.Save(summary); err != nil {
		return err
	}

	return c.set(context.Background(), c.key(summary.ID), summary)
}

func (c *FundSummaryCache) set(ctx context.Context, key string, summary fundsapp.FundSummary) error {
	value, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, value, c.ttl).Err()
}

func (c *FundSummaryCache) key(id string) string {
	return "fund-summary:" + id
}
