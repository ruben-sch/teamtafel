package push_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/ruben-sch/teamtafel/internal/auth"
	"github.com/ruben-sch/teamtafel/internal/dbtest"
	"github.com/ruben-sch/teamtafel/internal/push"
)

func abo(endpoint string) push.Abo {
	k, _ := ecdh.P256().GenerateKey(rand.Reader)
	authKey := make([]byte, 16)
	_, _ = rand.Read(authKey)
	var a push.Abo
	a.Endpoint = endpoint
	a.Keys.P256dh = base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	a.Keys.Auth = base64.RawURLEncoding.EncodeToString(authKey)
	return a
}

func TestAbosJeKonto(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	as := auth.NewStore(pool)
	k1, _ := as.KontoFuer(ctx, fmt.Sprintf("p1-%d@example.org", time.Now().UnixNano()))
	k2, _ := as.KontoFuer(ctx, fmt.Sprintf("p2-%d@example.org", time.Now().UnixNano()))
	s := push.NewStore(pool)
	a := abo(fmt.Sprintf("https://push.example/%d", time.Now().UnixNano()))
	if err := s.Speichern(ctx, k1.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Speichern(ctx, k1.ID, abo("http://unsicher.example/x")); !errors.Is(err, push.ErrUngueltig) {
		t.Errorf("http-endpoint: %v", err)
	}
	// Dasselbe Gerät mit anderem Konto: das Abo wandert.
	if err := s.Speichern(ctx, k2.ID, a); err != nil {
		t.Fatal(err)
	}
	if as1, _ := s.Abos(ctx, k1.ID); len(as1) != 0 {
		t.Errorf("k1 hat noch %d abos", len(as1))
	}
	as2, _ := s.Abos(ctx, k2.ID)
	if len(as2) != 1 || as2[0].Endpoint != a.Endpoint || as2[0].Keys.Auth != a.Keys.Auth {
		t.Fatalf("k2: %+v", as2)
	}
	// Fremdes Konto kann nicht abmelden.
	_ = s.Loeschen(ctx, k1.ID, a.Endpoint)
	if as2, _ := s.Abos(ctx, k2.ID); len(as2) != 1 {
		t.Error("fremdes konto hat abo gelöscht")
	}
	_ = s.Loeschen(ctx, k2.ID, a.Endpoint)
	if as2, _ := s.Abos(ctx, k2.ID); len(as2) != 0 {
		t.Error("abmelden fehlgeschlagen")
	}
}

func TestSendenVerschluesseltMitVAPID(t *testing.T) {
	var status = http.StatusCreated
	var gesehen *http.Request
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gesehen = r
		w.WriteHeader(status)
	}))
	defer srv.Close()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	s := &push.Sender{PublicKey: pub, PrivateKey: priv, Subject: "mailto:test@example.org", Client: srv.Client()}
	a := abo(srv.URL + "/abo")

	if err := s.Senden(context.Background(), a, []byte(`{"titel":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if gesehen.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(gesehen.Header.Get("Authorization"), "vapid ") {
		t.Errorf("header: %v", gesehen.Header)
	}
	for _, st := range []int{http.StatusGone, http.StatusNotFound} {
		status = st
		if err := s.Senden(context.Background(), a, []byte("x")); !errors.Is(err, push.ErrAbgelaufen) {
			t.Errorf("%d: %v", st, err)
		}
	}
	status = http.StatusTooManyRequests
	if err := s.Senden(context.Background(), a, []byte("x")); err == nil || errors.Is(err, push.ErrAbgelaufen) {
		t.Errorf("429: %v", err)
	}
}
