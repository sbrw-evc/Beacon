package alert

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FeedChannel is the PostgreSQL notification channel that tells every Umbrella instance an
// incident or its timeline changed, whichever instance or worker wrote it.
const FeedChannel = "umbrella_incidents"

// feedSchema notifies FeedChannel once per statement that changes alerts or adds timeline lines;
// PostgreSQL folds equal notifications of one transaction into one.
const feedSchema = `
CREATE OR REPLACE FUNCTION umbrella_incidents_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	PERFORM pg_notify('` + FeedChannel + `', '');
	RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS alerts_feed ON alerts;
CREATE TRIGGER alerts_feed AFTER INSERT OR UPDATE OR DELETE ON alerts
	FOR EACH STATEMENT EXECUTE FUNCTION umbrella_incidents_changed();
DROP TRIGGER IF EXISTS alert_timeline_feed ON alert_timeline;
CREATE TRIGGER alert_timeline_feed AFTER INSERT ON alert_timeline
	FOR EACH STATEMENT EXECUTE FUNCTION umbrella_incidents_changed();
`

// Watch calls changed each time incidents change until ctx ends. It listens on a connection of
// its own; when the connection is lost it calls changed (something may have changed meanwhile)
// and listens again on the pool pool returns then.
func Watch(ctx context.Context, pool func() *pgxpool.Pool, changed func()) {
	for {
		err := watch(ctx, pool(), changed)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("incident change feed lost, listening again", "err", err)
		changed()
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func watch(ctx context.Context, pool *pgxpool.Pool, changed func()) error {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The listening connection never goes back to the pool.
	conn := c.Hijack()
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+FeedChannel); err != nil {
		return err
	}
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		changed()
	}
}
