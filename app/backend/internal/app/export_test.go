package app

import "github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/alert"

// AlertEngine lets tests drive the engine's clock-based work.
func (a *App) AlertEngine() *alert.Engine { return a.alerts }
