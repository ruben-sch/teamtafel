package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var kalenderLink = regexp.MustCompile(`https://[^/<]+(/kalender/[A-Za-z0-9_-]+\.ics)`)

func TestKalenderAbo(t *testing.T) {
	w := neueTerminWelt(t)
	tm := w.termin(t, nil)

	body := w.do(http.MethodGet, "/einstellungen", nil, w.eltern).Body.String()
	if !strings.Contains(body, "Kalender-Link erzeugen") {
		t.Fatalf("kein knopf:\n%s", body)
	}
	rec := w.do(http.MethodPost, "/einstellungen/kalender", url.Values{}, w.eltern)
	m := kalenderLink.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil || !strings.Contains(rec.Body.String(), "webcal://"+w.host) {
		t.Fatalf("erneuern: %d\n%s", rec.Code, rec.Body.String())
	}

	// Kalender-Apps haben keine Sitzung: der Link allein reicht.
	feed := w.do(http.MethodGet, m[1], nil, nil)
	if feed.Code != http.StatusOK || !strings.HasPrefix(feed.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: %d %q", feed.Code, feed.Header().Get("Content-Type"))
	}
	ics := strings.ReplaceAll(feed.Body.String(), "\r\n ", "") // Faltung aufheben
	if !strings.Contains(ics, "UID:"+tm.ID+"@"+w.host) || !strings.Contains(ics, "SUMMARY:Bambini: Training") {
		t.Errorf("termin fehlt im feed:\n%s", feed.Body.String())
	}

	// Danach wird der Link nicht mehr angezeigt; ein neuer macht den alten ungültig.
	if body := w.do(http.MethodGet, "/einstellungen", nil, w.eltern).Body.String(); kalenderLink.MatchString(body) ||
		!strings.Contains(body, "Neuen Kalender-Link erzeugen") {
		t.Errorf("link erneut sichtbar oder kein erneuern-knopf")
	}
	w.do(http.MethodPost, "/einstellungen/kalender", url.Values{}, w.eltern)
	if rec := w.do(http.MethodGet, m[1], nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("alter link: %d", rec.Code)
	}
	for _, pfad := range []string{"/kalender/unsinn.ics", "/kalender/" + strings.TrimSuffix(strings.TrimPrefix(m[1], "/kalender/"), ".ics")} {
		if rec := w.do(http.MethodGet, pfad, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", pfad, rec.Code)
		}
	}
	if rec := w.do(http.MethodPost, "/einstellungen/kalender", url.Values{}, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("ohne login: %d", rec.Code)
	}
}
