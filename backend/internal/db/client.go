package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
	Pool *pgxpool.Pool
}

func NewClient(ctx context.Context, databaseURL string) (*Client, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Client{Pool: pool}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.Pool.Ping(ctx)
}

func (c *Client) Close() {
	c.Pool.Close()
}
