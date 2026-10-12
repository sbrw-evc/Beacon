package notify

import (
	"strings"
	"testing"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Every level of the alert scale has a word in every language, led by its priority.
func TestSeverityWords(t *testing.T) {
	for _, locale := range []string{model.LocaleEN, model.LocaleRU} {
		for _, s := range model.Severities {
			w := Word(locale, s.Name)
			if !strings.HasPrefix(w, s.Priority+" · ") || len(w) <= len(s.Priority)+4 {
				t.Errorf("%s word of %s = %q, want %q and a word", locale, s.Name, w, s.Priority+" · …")
			}
		}
	}
}
