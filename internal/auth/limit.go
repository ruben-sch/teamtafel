package auth

import (
	"sync"
	"time"
)

// Limiter begrenzt Versuche pro Schlüssel (E-Mail-Adresse, IP) in einem
// festen Zeitfenster. Im Speicher, weil die App als einzelne Instanz läuft.
type Limiter struct {
	max     int
	fenster time.Duration
	now     func() time.Time

	mu     sync.Mutex
	zaehle map[string]*eintrag
}

type eintrag struct {
	start time.Time
	n     int
}

// NewLimiter erlaubt max Versuche je fenster.
func NewLimiter(max int, fenster time.Duration) *Limiter {
	return &Limiter{max: max, fenster: fenster, now: time.Now, zaehle: map[string]*eintrag{}}
}

// Erlaubt zählt einen Versuch und meldet, ob er noch im Limit liegt.
func (l *Limiter) Erlaubt(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.zaehle[key]
	if !ok || now.Sub(e.start) >= l.fenster {
		if len(l.zaehle) > 10000 {
			l.aufraeumen(now)
		}
		l.zaehle[key] = &eintrag{start: now, n: 1}
		return true
	}
	e.n++
	return e.n <= l.max
}

func (l *Limiter) aufraeumen(now time.Time) {
	for k, e := range l.zaehle {
		if now.Sub(e.start) >= l.fenster {
			delete(l.zaehle, k)
		}
	}
}
