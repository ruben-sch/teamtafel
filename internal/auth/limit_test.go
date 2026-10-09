package auth

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	jetzt := time.Unix(0, 0)
	l := NewLimiter(3, 15*time.Minute)
	l.now = func() time.Time { return jetzt }

	for i := 0; i < 3; i++ {
		if !l.Erlaubt("a") {
			t.Fatalf("versuch %d muss erlaubt sein", i+1)
		}
	}
	if l.Erlaubt("a") {
		t.Fatal("vierter versuch muss gesperrt sein")
	}
	if !l.Erlaubt("b") {
		t.Fatal("anderer schlüssel ist unabhängig")
	}
	jetzt = jetzt.Add(15 * time.Minute)
	if !l.Erlaubt("a") {
		t.Fatal("nach ablauf des fensters wieder erlaubt")
	}
}
