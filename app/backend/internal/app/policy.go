package app

import (
	"fmt"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/store"
)

type PolicyService struct {
	st  *store.Store
	now func() time.Time
}

func NewPolicyService(st *store.Store) *PolicyService {
	return &PolicyService{st: st, now: func() time.Time { return time.Now().UTC() }}
}

type PolicySummary struct {
	Policy     model.PasswordPolicy `json:"policy"`
	LocalUsers int                  `json:"local_users"`
	Expired    int                  `json:"expired_users"`
	Expiring   int                  `json:"expiring_users"`
}

func (s *PolicyService) Get() model.PasswordPolicy {
	var out model.PasswordPolicy
	s.st.Read(func(d *store.Data) { out = d.Settings.Password })
	return out
}

func (s *PolicyService) Age(u model.User) model.PasswordAge {
	if u.Source != model.SourceLocal {
		return model.PasswordAge{}
	}
	age := s.Get().Age(u.PasswordChangedAt, s.now())
	if u.MustChangePassword {
		age.Expired, age.Warning = true, true
		if age.ExpiresAt.IsZero() {
			age.ExpiresAt = u.PasswordChangedAt
		}
	}
	return age
}

func (s *PolicyService) Summary() PolicySummary {
	var out PolicySummary
	s.st.Read(func(d *store.Data) { out = summarize(d, s.now()) })
	return out
}

func (s *PolicyService) Update(actor string, in model.PasswordPolicy) (PolicySummary, error) {
	p, err := in.Normalize()
	if err != nil {
		return PolicySummary{}, invalid("invalid_password_policy", err)
	}
	var out PolicySummary
	s.st.Write(func(d *store.Data) {
		if d.Settings.Password != p {
			d.Settings.Password = p
			d.AddAudit(store.AuditEntry{Actor: actor, Action: "settings.password_policy", Detail: describePolicy(p)})
		}
		out = summarize(d, s.now())
	})
	return out, nil
}

func summarize(d *store.Data, now time.Time) PolicySummary {
	out := PolicySummary{Policy: d.Settings.Password}
	for _, u := range d.Users {
		if u.Source != model.SourceLocal || u.Disabled {
			continue
		}
		out.LocalUsers++
		age := out.Policy.Age(u.PasswordChangedAt, now)
		switch {
		case age.Expired:
			out.Expired++
		case age.Warning:
			out.Expiring++
		}
	}
	return out
}

func describePolicy(p model.PasswordPolicy) string {
	return fmt.Sprintf("length %d, digits %d, special %d, mixed case %t, letters %s, max age %d days, warn %d days",
		p.MinLength, p.MinDigits, p.MinSpecial, p.RequireMixedCase, p.Letters, p.MaxAgeDays, p.WarnDays)
}
