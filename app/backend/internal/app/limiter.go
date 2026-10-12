package app

import (
	"strings"
	"sync"
	"time"
)

type Limiter struct {
	max    int
	window time.Duration
	block  time.Duration

	mu    sync.Mutex
	marks map[string]*attempts
}

type attempts struct {
	count int
	first time.Time
	until time.Time
}

func NewLimiter(max int, window, block time.Duration) *Limiter {
	return &Limiter{max: max, window: window, block: block, marks: map[string]*attempts{}}
}

func (l *Limiter) Locked(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		if at := l.marks[k]; at != nil && now.Before(at.until) {
			return true
		}
	}
	return false
}

func (l *Limiter) Fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		at := l.marks[k]
		if at == nil || now.Sub(at.first) > l.window {
			at = &attempts{first: now}
			l.marks[k] = at
		}
		at.count++
		if at.count >= l.max {
			at.until, at.count, at.first = now.Add(l.block), 0, now
		}
	}
	if len(l.marks) > 10000 {
		for k, at := range l.marks {
			if now.Sub(at.first) > l.window && now.After(at.until) {
				delete(l.marks, k)
			}
		}
	}
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.marks, key)
}

// loginGuard counts wrong passwords. The strict limit is per account and address, so one
// attacker cannot guess a password quickly, while the wrong passwords of one person do not lock
// out colleagues behind the same proxy or NAT address. Looser limits per address (password
// spraying over many accounts) and per account (guessing from many addresses) stay.
type loginGuard struct {
	pair, addr, account *Limiter
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		pair:    NewLimiter(maxFailures, failWindow, lockout),
		addr:    NewLimiter(maxLooseFailures, failWindow, lockout),
		account: NewLimiter(maxLooseFailures, failWindow, lockout),
	}
}

func loginAccount(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

func loginPair(username, ip string) string { return loginAccount(username) + "\x00" + ip }

// Locked tells whether a sign-in of username from ip is refused now.
func (g *loginGuard) Locked(username, ip string) bool {
	return g.pair.Locked(loginPair(username, ip)) || g.addr.Locked(ip) || g.account.Locked(loginAccount(username))
}

// Fail counts a wrong password.
func (g *loginGuard) Fail(username, ip string) {
	g.pair.Fail(loginPair(username, ip))
	g.addr.Fail(ip)
	g.account.Fail(loginAccount(username))
}

// Reset forgets the wrong passwords of the account after a successful sign-in; the address
// keeps its count, so signing in to an own account does not reset spraying.
func (g *loginGuard) Reset(username, ip string) {
	g.pair.Reset(loginPair(username, ip))
	g.account.Reset(loginAccount(username))
}
