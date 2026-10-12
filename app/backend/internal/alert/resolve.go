package alert

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

const (
	// TestIDLabel tells the alerts of different tests apart.
	TestIDLabel  = "umbrella_test_id"
	TestLifetime = 5 * time.Minute
)

// eventCIName is the name an event gives its configuration item: the ci label wins over the
// ci field.
func eventCIName(name string, labels map[string]string) string {
	if v := strings.TrimSpace(labels["ci"]); v != "" {
		return v
	}
	return strings.TrimSpace(name)
}

// HostKeys are the names a host of a monitoring system is known by, in the forms events are
// compared in (EventKeys): its technical and visible name, DNS names, IP addresses and the
// Prometheus instances (host:port) of its targets.
func HostKeys(h model.MonitoringHost) []string {
	var out []string
	values := append(append(append([]string{h.Host, h.Name}, h.DNS...), h.IPs...), h.Endpoints...)
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		for _, k := range EventKeys(v) {
			if k != "" && !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}

// indexHostLinks indexes the hosts linked by hand on the monitoring systems page: every name of
// a linked host leads to its configuration item, or to model.HostNoCI for a host said to be no
// item. A name two links disagree on is left to the automatic match. Links to items that no
// longer exist are ignored.
func (w *world) indexHostLinks(d *store.Data) {
	w.hostLinks = map[string]string{}
	conflict := map[string]bool{}
	for _, src := range d.MonitoringSources {
		if len(src.Links) == 0 {
			continue
		}
		for _, h := range src.Hosts {
			target, ok := src.Links[h.Key]
			if !ok || (target != model.HostNoCI && d.ConfigItems[target] == nil) {
				continue
			}
			for _, k := range HostKeys(h) {
				if prev, ok := w.hostLinks[k]; ok && prev != target {
					conflict[k] = true
				}
				w.hostLinks[k] = target
			}
		}
	}
	for k := range conflict {
		delete(w.hostLinks, k)
	}
}

// exclude applies the «Не является КЕ» mark of the host to the alert. An excluded alert is
// suppressed: nothing is sent to PagerDuty and no backup notification goes out, the interface
// shows it with the reason. It reports whether the alert is excluded. An alert whose host is
// no longer excluded keeps the PagerDuty state skipped, so the caller sends the trigger.
func (e *Engine) exclude(c *change, excluded bool, now time.Time) bool {
	a := c.a
	if !excluded {
		if a.Excluded {
			a.Excluded, a.Suppressed = false, false
			c.log(now, KindRoute, "host_included", map[string]string{"ci": a.CIName}, "")
		}
		return false
	}
	if !a.Excluded {
		c.log(now, KindRoute, "host_excluded", map[string]string{"ci": cmp.Or(a.EventCI, a.CIName)}, "")
	}
	a.Excluded, a.Suppressed, a.MaintenanceID = true, true, ""
	if !pdHas(a) {
		a.PD.State, a.PD.Retry = PDSkipped, ""
	}
	c.dirty = true
	return true
}

// Reresolve finds the configuration item of every active alert again, after the hand-made
// links of monitoring hosts or the catalog changed: an alert moves to the item its host is
// linked to now (or loses the item), an alert of a host marked «Не является КЕ» is
// suppressed, and one whose mark was taken off is sent on. An alert is not moved onto an item
// another active alert of the same signal already has.
func (e *Engine) Reresolve(ctx context.Context) error {
	rows, err := e.db.Query(ctx, "SELECT doc FROM alerts WHERE "+sqlActive+" ORDER BY seq")
	if err != nil {
		return err
	}
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*Alert, error) { return scanAlert(row) })
	if err != nil {
		return err
	}
	w := e.world()
	now := e.now()
	// Only the alerts whose item or exclusion would change are locked and written.
	var ids []string
	for _, a := range docs {
		if a != nil && w.resolutionChanged(a) {
			ids = append(ids, a.ID)
		}
	}
	for _, id := range ids {
		var cmd *Command
		err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
			// The same lock as folding events: dedup keys change here.
			if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(lockKey)); err != nil {
				return err
			}
			a, err := lockByID(ctx, tx, id)
			if err != nil || a == nil || !Active(a.Status) {
				return err
			}
			c := &change{a: a}
			cmd, err = e.reresolve(ctx, tx, c, w, now)
			if err != nil {
				return err
			}
			return c.save(ctx, tx)
		})
		if err != nil {
			return err
		}
		if cmd != nil {
			e.pd.Send(*cmd)
		}
	}
	return nil
}

// resolutionChanged: the item or the exclusion of the alert is not what its events resolve to now.
func (w *world) resolutionChanged(a *Alert) bool {
	ci, excluded := w.resolveCI(cmp.Or(a.EventCI, a.CIName), a.Labels)
	id := ""
	if ci != nil {
		id = ci.ID
	}
	return id != a.CIID || excluded != a.Excluded
}

func (e *Engine) reresolve(ctx context.Context, tx pgx.Tx, c *change, w *world, now time.Time) (*Command, error) {
	a := c.a
	name := cmp.Or(a.EventCI, a.CIName)
	ci, excluded := w.resolveCI(name, a.Labels)
	newID := ""
	if ci != nil {
		newID = ci.ID
	}
	if newID != a.CIID {
		key := dedupKey(ci, name, a.Signal)
		other, err := activeByKey(ctx, tx, key)
		if err != nil {
			return nil, err
		}
		if other != nil && other.ID != a.ID {
			// The item (or, when the item is gone, the name) already has an alert of this
			// signal: this one joins it, otherwise it would stay bound to a stale item.
			target := name
			if ci != nil {
				target = ci.Name
			}
			return e.mergeInto(ctx, tx, c, other, target, "", now)
		}
		if other == nil || other.ID == a.ID {
			a.DedupKey = key
			if a.EventCI == "" {
				a.EventCI = name
			}
			if ci != nil {
				e.bind(c, w, ci, now)
			} else {
				prev := a.CIName
				a.CIID, a.CIName, a.CIKind = "", name, ""
				a.Route = w.route(nil, now)
				c.log(now, KindRoute, "ci_unbound", map[string]string{"ci": name, "was": prev}, "")
			}
		}
	}
	wasExcluded := a.Excluded
	if e.exclude(c, excluded, now) || !wasExcluded {
		return nil, nil
	}
	// The host is no longer excluded: the alert goes on as a new one would.
	if m := w.maintenanceFor(a, now); m != nil {
		suppress(c, m, now)
		return nil, nil
	}
	if a.PD.State == PDSkipped {
		a.PD.State, a.PD.AttemptAt = PDPending, nil
	}
	if pdHas(a) {
		return nil, nil
	}
	return e.pdCmd(a, PDTrigger, now), nil
}

// expireTests resolves the alerts of test events once they have lasted their lifetime
// (TestLifetime unless the alerting policy sets one), with the connector events that fed them.
func (e *Engine) expireTests(ctx context.Context, now time.Time, lifetime time.Duration) error {
	rows, err := e.db.Query(ctx, `SELECT id FROM alerts WHERE `+sqlActive+` AND doc->'labels'->>'`+TestLabel+`' = 'true'
		AND first_seen <= $1 ORDER BY seq`, now.Add(-lifetime))
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		return err
	}
	var events bool
	if err := e.db.QueryRow(ctx, "SELECT to_regclass('connector_events') IS NOT NULL").Scan(&events); err != nil {
		return err
	}
	for _, id := range ids {
		err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
			a, err := lockByID(ctx, tx, id)
			if err != nil || a == nil || !Active(a.Status) || !a.IsTest() {
				return err
			}
			c := &change{a: a}
			for _, s := range a.Sources {
				s.Status, s.LastSeen = SourceResolved, now
				if events {
					if _, err := tx.Exec(ctx, "UPDATE connector_events SET status = '"+SourceResolved+"', last_seen = now() WHERE connector_id = $1 AND key = $2",
						s.ConnectorID, s.Key); err != nil {
						return err
					}
				}
			}
			e.resolve(c, now, "test_expired", "")
			return c.save(ctx, tx)
		})
		if err != nil {
			slog.Error("test alert not resolved", "alert", id, "err", err)
		}
	}
	return nil
}
