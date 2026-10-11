package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ruben-sch/teamtafel/internal/termin"
)

func TestTrainerSiehtAnwesenheitsstatistik(t *testing.T) {
	w := neueTerminWelt(t)
	ctx := context.Background()
	m := "/m/" + w.bambini.ID
	gestern := time.Now().In(termin.Zeitzone).AddDate(0, 0, -1)
	tm, err := w.termine.Anlegen(ctx, w.verein.ID, w.bambini.ID, "", termin.Daten{
		Typ: termin.TypTraining, Beginn: gestern, Ende: gestern.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	kader, err := w.team.Kader(ctx, w.verein.ID, w.bambini.ID)
	if err != nil || len(kader) != 1 {
		t.Fatalf("kader: %+v %v", kader, err)
	}
	trainer, _ := w.team.Trainer(ctx, w.verein.ID, w.bambini.ID)
	if err := w.termine.Rueckmelden(ctx, w.verein.ID, tm.ID, kader[0].ID, trainer[0].KontoID, termin.Zu, "", true); err != nil {
		t.Fatal(err)
	}

	if body := w.do(http.MethodGet, m, nil, w.trainer).Body.String(); !strings.Contains(body, m+"/statistik") {
		t.Fatalf("mannschaftsseite ohne link zur statistik: %s", body)
	}
	rec := w.do(http.MethodGet, m+"/statistik", nil, w.trainer)
	if rec.Code != http.StatusOK {
		t.Fatalf("statistik: %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Ball, Ben") || !strings.Contains(body, "100 %") {
		t.Fatalf("statistik ohne ben: %s", body)
	}
	for name, c := range map[string]*http.Cookie{"eltern": w.eltern, "fremd": w.fremd} {
		if rec := w.do(http.MethodGet, m+"/statistik", nil, c); rec.Code != http.StatusForbidden {
			t.Errorf("%s sieht statistik: %d", name, rec.Code)
		}
	}
}
