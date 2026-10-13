package web

import (
	"context"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"github.com/ruben-sch/teamtafel/internal/auth"
)

// kennung zeigt in der Kopfzeile jeder Seite, wer angemeldet ist und mit welchen Rollen.
type kennung struct {
	Name   string
	Rollen []string
}

// kopfDaten sind die Daten des gemeinsamen Seitenkopfs.
type kopfDaten struct {
	Titel   string
	Kennung *kennung
}

type kennungKey struct{}

// kennungen ermittelt die Kennung eines Kontos im Verein der Anfrage.
type kennungen struct {
	rollen *rollen
	// team ist optional; ohne gibt es keine Trainer- oder Elternrolle.
	team Team
}

// mitKennung legt die Quelle der Kennung in den Kontext. Ermittelt wird sie
// erst beim Rendern, damit Weiterleitungen und Formularposts keine Abfragen kosten.
func mitKennung(q *kennungen, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), kennungKey{}, q)))
	})
}

// kennungAus liefert die Kennung des angemeldeten Kontos, sonst nil.
// Ein Ladefehler soll keine Seite verhindern, deshalb wird er nur protokolliert.
func kennungAus(ctx context.Context) *kennung {
	q, ok := ctx.Value(kennungKey{}).(*kennungen)
	k, angemeldet := kontoAus(ctx)
	if !ok || !angemeldet {
		return nil
	}
	kn, err := q.ermitteln(ctx, k)
	if err != nil {
		slog.Error("kennung ermitteln", "err", err)
		return &kennung{Name: anzeigeName(k, nil)}
	}
	return kn
}

// ermitteln: Admin (Plattform oder Verein), Trainer, Spieler und „Eltern von …“, in dieser Reihenfolge.
func (q *kennungen) ermitteln(ctx context.Context, k auth.Konto) (*kennung, error) {
	kn := &kennung{}
	admin, err := q.rollen.darfVerwalten(ctx, k)
	if err != nil {
		return nil, err
	}
	if admin {
		kn.Rollen = append(kn.Rollen, "Admin")
	}
	var eigene, kinder []string
	if v, ok := vereinAus(ctx); ok && q.team != nil {
		ms, err := q.team.TrainerMannschaften(ctx, v.ID, k.ID)
		if err != nil {
			return nil, err
		}
		if len(ms) > 0 {
			kn.Rollen = append(kn.Rollen, "Trainer")
		}
		sp, err := q.team.MeineSpieler(ctx, v.ID, k.ID)
		if err != nil {
			return nil, err
		}
		for _, s := range sp {
			if s.Selbst {
				eigene = append(eigene, s.Vorname)
			} else {
				kinder = append(kinder, s.Vorname)
			}
		}
		if len(eigene) > 0 {
			kn.Rollen = append(kn.Rollen, "Spieler")
		}
		// Eltern sagen für alle ihre Kinder zu oder ab, deshalb stehen alle Kinder in der Rolle.
		if len(kinder) > 0 {
			kn.Rollen = append(kn.Rollen, "Eltern von "+strings.Join(kinder, " und "))
		}
	}
	kn.Name = anzeigeName(k, eigene)
	return kn, nil
}

// anzeigeName: Name, sonst eigener Vorname als Spieler, sonst die E-Mail-Adresse.
// Die Kinder von Eltern stehen in der Rolle „Eltern von …“.
func anzeigeName(k auth.Konto, eigene []string) string {
	switch {
	case strings.TrimSpace(k.Name) != "":
		return k.Name
	case len(eigene) > 0:
		return eigene[0]
	default:
		return k.Email
	}
}

// titelAus liest das Feld Titel der Seitendaten für den Seitenkopf.
func titelAus(data any) string {
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	if f := v.FieldByName("Titel"); f.IsValid() && f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}
