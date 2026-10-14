package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	LettersLatin         = "latin"
	LettersCyrillic      = "cyrillic"
	LettersLatinCyrillic = "latin_cyrillic"
	LettersAny           = "any"

	PolicyMinLength = 8
	PolicyMaxLength = 128
	PolicyMaxCount  = 16

	PolicyMaxAgeDays  = 3650
	PolicyMaxWarnDays = 90

	MaxPasswordLength = 256

	PolicyMaxHistory         = 24
	PolicyMinLockoutAttempts = 3
	PolicyMaxLockoutAttempts = 100
	PolicyMaxLockoutMinutes  = 1440
)

const (
	ViolationLength   = "length"
	ViolationDigits   = "digits"
	ViolationSpecial  = "special"
	ViolationCase     = "mixed_case"
	ViolationLetters  = "letters"
	ViolationUsername = "username"
	ViolationControl  = "control"
	ViolationCommon   = "common"
	ViolationHistory  = "history"
)

type PasswordPolicy struct {
	MinLength        int    `json:"min_length"`
	RequireDigits    bool   `json:"require_digits"`
	MinDigits        int    `json:"min_digits"`
	RequireSpecial   bool   `json:"require_special"`
	MinSpecial       int    `json:"min_special"`
	RequireMixedCase bool   `json:"require_mixed_case"`
	Letters          string `json:"letters"`
	MaxAgeDays       int    `json:"max_age_days"`
	WarnDays         int    `json:"warn_days"`
	// HistoryCount: a new password must differ from this many previous ones (0 = not checked).
	HistoryCount int `json:"history_count"`
	// BlockCommon rejects the most common passwords, keyboard sequences and the user name with a
	// few characters added.
	BlockCommon bool `json:"block_common"`
	// LockoutAttempts wrong passwords in a row lock the account for LockoutMinutes (0 = the
	// built-in limits only).
	LockoutAttempts int `json:"lockout_attempts"`
	LockoutMinutes  int `json:"lockout_minutes"`
}

func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MinLength: 12, RequireDigits: true, MinDigits: 1, RequireSpecial: true, MinSpecial: 1,
		RequireMixedCase: true, Letters: LettersLatin, HistoryCount: 5, BlockCommon: true, LockoutAttempts: 10,
		LockoutMinutes: 15}
}

func (p PasswordPolicy) Normalize() (PasswordPolicy, error) {
	if p.MinLength < PolicyMinLength || p.MinLength > PolicyMaxLength {
		return p, fmt.Errorf("minimum length must be between %d and %d", PolicyMinLength, PolicyMaxLength)
	}
	if !p.RequireDigits {
		p.MinDigits = 0
	} else if p.MinDigits < 1 || p.MinDigits > PolicyMaxCount {
		return p, fmt.Errorf("number of digits must be between 1 and %d", PolicyMaxCount)
	}
	if !p.RequireSpecial {
		p.MinSpecial = 0
	} else if p.MinSpecial < 1 || p.MinSpecial > PolicyMaxCount {
		return p, fmt.Errorf("number of special characters must be between 1 and %d", PolicyMaxCount)
	}
	switch p.Letters {
	case LettersLatin, LettersCyrillic, LettersLatinCyrillic, LettersAny:
	case "":
		p.Letters = LettersAny
	default:
		return p, errors.New("allowed letters must be latin, cyrillic, latin_cyrillic or any")
	}
	if p.MaxAgeDays < 0 || p.MaxAgeDays > PolicyMaxAgeDays {
		return p, fmt.Errorf("password lifetime must be between 1 and %d days, or 0 for no expiry", PolicyMaxAgeDays)
	}
	if p.MaxAgeDays == 0 {
		p.WarnDays = 0
	} else if p.WarnDays < 0 || p.WarnDays > PolicyMaxWarnDays || p.WarnDays >= p.MaxAgeDays {
		return p, fmt.Errorf("expiry warning must be between 0 and %d days and shorter than the password lifetime", PolicyMaxWarnDays)
	}
	if p.HistoryCount < 0 || p.HistoryCount > PolicyMaxHistory {
		return p, fmt.Errorf("password history must be between 0 and %d", PolicyMaxHistory)
	}
	if p.LockoutAttempts == 0 {
		p.LockoutMinutes = 0
	} else if p.LockoutAttempts < PolicyMinLockoutAttempts || p.LockoutAttempts > PolicyMaxLockoutAttempts {
		return p, fmt.Errorf("lockout threshold must be between %d and %d, or 0 for the built-in limits", PolicyMinLockoutAttempts, PolicyMaxLockoutAttempts)
	} else if p.LockoutMinutes < 1 || p.LockoutMinutes > PolicyMaxLockoutMinutes {
		return p, fmt.Errorf("lockout duration must be between 1 and %d minutes", PolicyMaxLockoutMinutes)
	}
	need := p.MinDigits + p.MinSpecial
	if p.RequireMixedCase {
		need += 2
	}
	if need > p.MinLength {
		return p, errors.New("required digits, special characters and letters do not fit into the minimum length")
	}
	return p, nil
}

func (p PasswordPolicy) letterAllowed(r rune) bool {
	switch p.Letters {
	case LettersLatin:
		return unicode.Is(unicode.Latin, r)
	case LettersCyrillic:
		return unicode.Is(unicode.Cyrillic, r)
	case LettersLatinCyrillic:
		return unicode.Is(unicode.Latin, r) || unicode.Is(unicode.Cyrillic, r)
	}
	return true
}

func IsSpecial(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }

func (p PasswordPolicy) Check(password, username string) []string {
	var out []string
	add := func(v string) {
		for _, x := range out {
			if x == v {
				return
			}
		}
		out = append(out, v)
	}
	runes := []rune(password)
	if len(runes) < p.MinLength || len(password) > MaxPasswordLength {
		add(ViolationLength)
	}
	var digits, special int
	var upper, lower bool
	for _, r := range runes {
		switch {
		case unicode.IsControl(r):
			add(ViolationControl)
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			if !p.letterAllowed(r) {
				add(ViolationLetters)
			}
			upper = upper || unicode.IsUpper(r)
			lower = lower || unicode.IsLower(r)
		case IsSpecial(r):
			special++
		}
	}
	if p.RequireDigits && digits < p.MinDigits {
		add(ViolationDigits)
	}
	if p.RequireSpecial && special < p.MinSpecial {
		add(ViolationSpecial)
	}
	if p.RequireMixedCase && (!upper || !lower) {
		add(ViolationCase)
	}
	if username != "" && strings.EqualFold(password, username) {
		add(ViolationUsername)
	}
	if p.BlockCommon && IsCommonPassword(password, username) {
		add(ViolationCommon)
	}
	return out
}

type PolicyError struct{ Violations []string }

func (e *PolicyError) Error() string {
	return "password does not match the policy: " + strings.Join(e.Violations, ", ")
}

func (p PasswordPolicy) Validate(password, username string) error {
	if v := p.Check(password, username); len(v) > 0 {
		return &PolicyError{Violations: v}
	}
	return nil
}

type PasswordAge struct {
	ExpiresAt time.Time
	Expired   bool
	Warning   bool
}

func (p PasswordPolicy) Age(changedAt, now time.Time) PasswordAge {
	if p.MaxAgeDays <= 0 || changedAt.IsZero() {
		return PasswordAge{}
	}
	expires := changedAt.AddDate(0, 0, p.MaxAgeDays)
	return PasswordAge{
		ExpiresAt: expires,
		Expired:   !now.Before(expires),
		Warning:   !now.Before(expires.AddDate(0, 0, -p.WarnDays)),
	}
}
