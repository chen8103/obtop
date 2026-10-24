package obtop

import (
	"context"
	"fmt"
	"time"

	"github.com/chen8103/obtop/internal/config"
	"github.com/chen8103/obtop/internal/obclient"
	"github.com/chen8103/obtop/internal/service"
	"github.com/chen8103/obtop/obmodel"
)

// Client is the public SDK entry for OceanBase monitoring and history queries.
type Client struct {
	inner *obclient.Client
}

// Open connects through OBProxy using cfg. The returned Client owns the
// connection; call Close when finished.
func Open(ctx context.Context, cfg Config) (*Client, error) {
	internalCfg := config.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
		Database: cfg.Database,
		Interval: time.Second,
	}
	if internalCfg.Port == 0 {
		internalCfg.Port = 3306
	}
	if internalCfg.Database == "" {
		internalCfg.Database = "oceanbase"
	}
	if err := internalCfg.Validate(); err != nil {
		return nil, AsDBError("open", err)
	}
	inner, err := obclient.New(ctx, internalCfg)
	if err != nil {
		return nil, AsDBError("open", err)
	}
	return &Client{inner: inner}, nil
}

// Close releases the underlying database connection.
func (c *Client) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

// Snapshot collects one processlist (+ rates) sample.
// Session or metrics query problems are encoded in Snapshot fields
// (SessionsErr, MetricsErr, RatesWarmingUp, RatesStale), not as a hard failure,
// so empty results remain distinguishable from query failures.
func (c *Client) Snapshot(ctx context.Context, opts SnapshotOptions) (Snapshot, error) {
	if err := c.require(); err != nil {
		return Snapshot{}, err
	}
	sampler := service.NewSampler(c.inner)
	return sampler.Sample(ctx, service.SampleOptions{
		Filters:    opts.Filters,
		ActiveOnly: opts.ActiveOnly,
		SortField:  opts.SortField,
	}), nil
}

// Watch repeatedly samples until Duration elapses, context is cancelled, or
// consume returns an error. Samples are delivered synchronously with no queue:
// a slow consumer stretches ActualInterval instead of dropping frames.
func (c *Client) Watch(ctx context.Context, opts WatchOptions, consume func(Snapshot) error) error {
	if err := c.require(); err != nil {
		return err
	}
	return watchLoop(ctx, service.NewSampler(c.inner), opts, consume)
}

func watchLoop(ctx context.Context, sampler *service.Sampler, opts WatchOptions, consume func(Snapshot) error) error {
	if consume == nil {
		return fmt.Errorf("watch: consume callback is required")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = time.Second
	}

	watchCtx := ctx
	cancel := func() {}
	if opts.Duration > 0 {
		watchCtx, cancel = context.WithTimeout(ctx, opts.Duration)
	}
	defer cancel()

	sampleOpts := service.SampleOptions{
		Filters:    opts.Filters,
		ActiveOnly: opts.ActiveOnly,
		SortField:  opts.SortField,
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	contextErr := func() error {
		// Parent cancel/timeout always wins; only our Duration deadline is a clean exit.
		if err := ctx.Err(); err != nil {
			return err
		}
		if opts.Duration > 0 && watchCtx.Err() == context.DeadlineExceeded {
			return nil
		}
		return watchCtx.Err()
	}
	emit := func() error {
		if watchCtx.Err() != nil {
			return contextErr()
		}
		// Keep callback errors separate from context termination: even if the
		// duration expires during the callback, its error must be preserved.
		return consume(sampler.Sample(watchCtx, sampleOpts))
	}

	if err := emit(); err != nil {
		return err
	}
	for {
		select {
		case <-watchCtx.Done():
			return contextErr()
		case <-ticker.C:
			if err := emit(); err != nil {
				return err
			}
		}
	}
}

// QuerySQLHistory reads short SQL history from GV$OB_SQL_AUDIT.
func (c *Client) QuerySQLHistory(ctx context.Context, opts HistoryOptions) (HistoryPage, error) {
	if err := c.require(); err != nil {
		return HistoryPage{}, err
	}
	page, err := c.inner.QuerySQLHistory(ctx, opts)
	if err != nil {
		return page, AsDBError("sql-history", err)
	}
	return page, nil
}

// ExplainSQL estimates a plan for a saved SQL text.
func (c *Client) ExplainSQL(ctx context.Context, req ExplainRequest) (ExplainResult, error) {
	if err := c.require(); err != nil {
		return ExplainResult{}, err
	}
	result, err := c.inner.ExplainStatement(ctx, req)
	if err != nil {
		return ExplainResult{}, AsDBError("explain", err)
	}
	return result, nil
}

// KillSession closes one database session. Callers must authorize the target.
func (c *Client) KillSession(ctx context.Context, sessID int64) error {
	if err := c.require(); err != nil {
		return err
	}
	if err := c.inner.KillSession(ctx, sessID); err != nil {
		return AsDBError("kill", err)
	}
	return nil
}

// DrainUserSessions performs proxy-aware bounded session drain for an exact user.
// Confirmation and account locking remain the caller's responsibility.
func (c *Client) DrainUserSessions(
	ctx context.Context,
	user string,
	report func(SessionDrainProgress),
) (SessionDrainResult, error) {
	if err := c.require(); err != nil {
		return SessionDrainResult{}, err
	}
	result, err := c.inner.DrainUserSessions(ctx, user, report)
	if err != nil {
		return result, AsDBError("drain", err)
	}
	return result, nil
}

func (c *Client) require() error {
	if c == nil || c.inner == nil {
		return fmt.Errorf("client is closed")
	}
	return nil
}

// ActiveProcesses / ApplyFilters / SortProcesses / SessionStatsByUser are
// re-exported helpers for callers that work with Snapshot.Rows.
var (
	ActiveProcesses    = obmodel.ActiveProcesses
	ApplyFilters       = obmodel.ApplyFilters
	SortProcesses      = obmodel.SortProcesses
	SessionStatsByUser = obmodel.SessionStatsByUser
	NextSortField      = obmodel.NextSortField
)
