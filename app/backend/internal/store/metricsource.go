package store

import "github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"

// MetricSource returns what RED and USE rules query by a source ID: a metric source, or a
// Prometheus monitoring system, which serves as a metric source with its own address and
// credential, so one Prometheus is set up once. Nil when there is neither. The result is a
// copy.
func (d *Data) MetricSource(id string) *model.MetricSource {
	if s := d.MetricSources[id]; s != nil {
		cp := *s
		return &cp
	}
	if m := d.MonitoringSources[id]; m != nil && m.Kind == model.MonitoringPrometheus {
		return &model.MetricSource{ID: m.ID, Name: m.Name, URL: m.URL, CredentialID: m.CredentialID, SkipVerify: m.SkipVerify,
			CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt, UpdatedBy: m.UpdatedBy, UpdatedAt: m.UpdatedAt}
	}
	return nil
}
