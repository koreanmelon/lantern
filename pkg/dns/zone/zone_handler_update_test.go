package zone

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestReplaceZones(t *testing.T) {
	old := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}))
	next := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}))
	var authority ZoneHandler
	if got := ask(t, &authority, "app.home.arpa.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeRefused {
		t.Fatal("zero value should refuse queries")
	}
	if err := authority.ReplaceZones(old); err != nil {
		t.Fatal(err)
	}
	// Replacing a zone does not change previously returned read-only records.
	previous := ask(t, &authority, "app.home.arpa.", dnsmessage.TypeA)
	input := []*Zone{next}
	if err := authority.ReplaceZones(input...); err != nil {
		t.Fatal(err)
	}
	input[0] = nil
	got := ask(t, &authority, "app.home.arpa.", dnsmessage.TypeA)
	if got.Answers[0].Body.(*dnsmessage.AResource).A[3] != 2 {
		t.Fatal("replacement not visible")
	}
	if previous.Answers[0].Body.(*dnsmessage.AResource).A[3] != 1 {
		t.Fatal("replacement mutated a previously returned answer")
	}

	for _, tc := range []struct {
		name  string
		zones []*Zone
	}{
		{"nil zone", []*Zone{nil}},
		{"uninitialized zone", []*Zone{{}}},
		{"duplicate origins", []*Zone{next, next}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := authority.ReplaceZones(tc.zones...); err == nil {
				t.Fatal("expected validation failure")
			}
			got := ask(t, &authority, "app.home.arpa.", dnsmessage.TypeA)
			if got.Answers[0].Body.(*dnsmessage.AResource).A[3] != 2 {
				t.Fatal("invalid update changed the live zones")
			}
		})
	}
	other := newTestZone(t, "other.arpa.")
	if err := authority.ReplaceZones(other); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, &authority, "app.home.arpa.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeRefused {
		t.Fatal("replacement retained a removed zone")
	}
	if err := authority.ReplaceZones(); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, &authority, "other.arpa.", dnsmessage.TypeSOA); got.RCode != dnsmessage.RCodeRefused {
		t.Fatal("empty replacement did not remove all zones")
	}
}

func TestReplaceZonesValidatesZonesIndependently(t *testing.T) {
	mail := newTestZone(t, "mail.arpa.", record("mail.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: dnsmessage.MustNewName("alias.home.arpa.")}))
	authority, err := NewZoneHandler(mail)
	if err != nil {
		t.Fatal(err)
	}
	aliases := newTestZone(t, "home.arpa.", record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("real.home.arpa.")}))
	if err := authority.ReplaceZones(mail, aliases); err != nil {
		t.Fatalf("individually valid zones were rejected: %v", err)
	}
	if got := ask(t, authority, "alias.home.arpa.", dnsmessage.TypeCNAME); len(got.Answers) != 1 {
		t.Fatal("replacement did not install the new zone")
	}
	if got := ask(t, authority, "mail.arpa.", dnsmessage.TypeMX); len(got.Answers) != 1 {
		t.Fatal("replacement removed the retained zone's records")
	}
}

func TestConcurrentZoneReplacement(t *testing.T) {
	versions := make([][]*Zone, 2)
	for i := range versions {
		target := fmt.Sprintf("v%d.back.arpa.", i+1)
		versions[i] = []*Zone{
			newTestZone(t, "front.arpa.", record("app.front.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)})),
			newTestZone(t, "back.arpa.", record(target, dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, byte(i + 1)}})),
		}
	}
	authority, err := NewZoneHandler(versions[0]...)
	if err != nil {
		t.Fatal(err)
	}
	query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("app.front.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		workers.Go(func() {
			<-start
			for range 200 {
				if err := authority.ReplaceZones(versions[i]...); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 4 {
		workers.Go(func() {
			<-start
			for range 400 {
				got, err := authority.Handle(t.Context(), query)
				if err != nil {
					t.Error(err)
					return
				}
				if got.RCode != dnsmessage.RCodeSuccess || len(got.Answers) != 2 || !got.Authoritative {
					t.Errorf("mixed or incomplete zone set: %s", got.GoString())
					return
				}
				alias := got.Answers[0].Body.(*dnsmessage.CNAMEResource)
				address := got.Answers[1].Body.(*dnsmessage.AResource)
				if alias.CNAME.String() != fmt.Sprintf("v%d.back.arpa.", address.A[3]) {
					t.Error("CNAME and address came from different zone sets")
					return
				}
			}
		})
	}
	close(start)
	workers.Wait()
}

func TestZoneHandlerReleasesReadLock(t *testing.T) {
	handler, err := NewZoneHandler(newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}),
		record("loop.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("loop.home.arpa.")}),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		queryName string
		cancel    bool
	}{
		{"answer", "app.home.arpa.", false},
		{"negative answer", "missing.home.arpa.", false},
		{"outside zone", "example.com.", false},
		{"CNAME loop", "loop.home.arpa.", false},
		{"invalid question", "", false},
		{"canceled", "app.home.arpa.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			var query dnsmessage.Message
			if tc.queryName != "" {
				query.Questions = []dnsmessage.Question{{Name: dnsmessage.MustNewName(tc.queryName), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}
			}
			_, _ = handler.Handle(ctx, query)
			if !handler.mu.TryLock() {
				t.Fatal("Handle left the read lock held")
			}
			handler.mu.Unlock()
		})
	}
}
