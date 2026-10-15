package hosted

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/koreanmelon/lantern/pkg/dns/zone"
	"golang.org/x/net/dns/dnsmessage"
)

func ask(t *testing.T, handler *ZoneHandler, name string, typ dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	query := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}},
	}
	response, err := handler.Handle(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Response || response.ID != query.ID || !response.RecursionDesired || response.RecursionAvailable ||
		len(response.Questions) != 1 || response.Questions[0] != query.Questions[0] {
		t.Fatalf("incorrect response envelope: %s", response.GoString())
	}
	return response
}

func TestZoneHandlerLookup(t *testing.T) {
	z := newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}),
		record("api.apps.home.arpa.", dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 1, 0xd, 0xb8}}),
	)
	handler, err := NewZoneHandler(z)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		typ           dnsmessage.Type
		code          dnsmessage.RCode
		answers       int
		authoritative bool
		negative      bool
	}{
		{"app.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 2, true, false},
		{"APP.Home.ARPA.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 2, true, false},
		{"api.apps.home.arpa.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 1, true, false},
		{"app.home.arpa.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 0, true, true},
		{"apps.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 0, true, true},
		{"missing.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0, true, true},
		{"*.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0, true, true},
		{"*.example.com.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"home.arpa.", dnsmessage.TypeSOA, dnsmessage.RCodeSuccess, 1, true, false},
		{"home.arpa.", dnsmessage.TypeNS, dnsmessage.RCodeSuccess, 1, true, false},
		{"home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 0, true, true},
		{"app.home.arpa.", 65000, dnsmessage.RCodeSuccess, 0, true, true},
		{"example.com.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"nothome.arpa.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"app.home.arpa.", dnsmessage.TypeALL, dnsmessage.RCodeRefused, 0, false, false},
		{"home.arpa.", 252, dnsmessage.RCodeRefused, 0, false, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.typ), func(t *testing.T) {
			got := ask(t, handler, tc.name, tc.typ)
			if got.RCode != tc.code || got.Authoritative != tc.authoritative || len(got.Answers) != tc.answers {
				t.Fatalf("unexpected response: %s", got.GoString())
			}
			if tc.negative {
				if len(got.Authorities) != 1 || got.Authorities[0].Header.Type != dnsmessage.TypeSOA || got.Authorities[0].Header.TTL != 60 {
					t.Fatalf("incorrect negative SOA: %s", got.GoString())
				}
			} else if len(got.Authorities) != 0 {
				t.Fatal("unexpected authority records")
			}
		})
	}
}

func TestZoneHandlerCNAME(t *testing.T) {
	alias := func(name, target string) dnsmessage.Resource {
		return record(name+".home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)})
	}
	extra := []dnsmessage.Resource{
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		alias("alias", "APP.home.arpa."), alias("chain", "alias.home.arpa."),
		alias("external", "example.com."), alias("missing", "absent.home.arpa."),
		alias("loop1", "loop2.home.arpa."), alias("loop2", "loop1.home.arpa."),
	}
	for i := range 18 {
		extra = append(extra, alias(fmt.Sprintf("long%d", i), fmt.Sprintf("long%d.home.arpa.", i+1)))
	}
	extra = append(extra, record("long18.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))
	handler, err := NewZoneHandler(newTestZone(t, "home.arpa.", extra...))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		typ      dnsmessage.Type
		code     dnsmessage.RCode
		answers  int
		negative bool
	}{
		{"alias", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 2, false},
		{"chain", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 3, false},
		{"alias", dnsmessage.TypeCNAME, dnsmessage.RCodeSuccess, 1, false},
		{"alias", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 1, true},
		{"external", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 1, false},
		{"missing", dnsmessage.TypeA, dnsmessage.RCodeNameError, 1, true},
		{"loop1", dnsmessage.TypeA, dnsmessage.RCodeServerFailure, 0, false},
		{"long0", dnsmessage.TypeA, dnsmessage.RCodeServerFailure, 0, false},
		{"long1", dnsmessage.TypeA, dnsmessage.RCodeServerFailure, 0, false},
		{"long2", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 17, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.typ), func(t *testing.T) {
			got := ask(t, handler, tc.name+".home.arpa.", tc.typ)
			if got.RCode != tc.code || len(got.Answers) != tc.answers || !got.Authoritative || (len(got.Authorities) == 1) != tc.negative {
				t.Fatalf("unexpected alias result: %s", got.GoString())
			}
		})
	}
}

func TestZoneHandlerRecordTypes(t *testing.T) {
	txt := &dnsmessage.TXTResource{TXT: []string{"hello", "world"}}
	z := newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeTXT, txt),
		record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.home.arpa.")}),
		record("_web._tcp.home.arpa.", dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: 1, Weight: 2, Port: 443, Target: dnsmessage.MustNewName("app.home.arpa.")}),
		record("ptr.home.arpa.", dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("app.home.arpa.")}),
	)
	handler, err := NewZoneHandler(z)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		typ  dnsmessage.Type
	}{
		{"app.home.arpa.", dnsmessage.TypeTXT}, {"home.arpa.", dnsmessage.TypeMX},
		{"_web._tcp.home.arpa.", dnsmessage.TypeSRV}, {"ptr.home.arpa.", dnsmessage.TypePTR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ask(t, handler, tc.name, tc.typ)
			if len(got.Answers) != 1 || got.Answers[0].Header.Type != tc.typ {
				t.Fatal("missing record")
			}
		})
	}
	got := ask(t, handler, "app.home.arpa.", dnsmessage.TypeTXT)
	body := got.Answers[0].Body.(*dnsmessage.TXTResource)
	if body != txt || len(body.TXT) != 2 || body.TXT[0] != "hello" || body.TXT[1] != "world" || got.Answers[0].Header.TTL != 300 {
		t.Fatal("TXT record was cloned or its contents were changed")
	}
}

func TestZoneHandlerMultipleZones(t *testing.T) {
	parent := newTestZone(t, "home.arpa.", record("app.child.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}))
	child := newTestZone(t, "child.home.arpa.", record("app.child.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}))
	handler, err := NewZoneHandler(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	got := ask(t, handler, "app.child.home.arpa.", dnsmessage.TypeA)
	if got.Answers[0].Body.(*dnsmessage.AResource).A[3] != 2 {
		t.Fatal("did not select child zone")
	}
	if _, err := NewZoneHandler(parent, parent); err == nil {
		t.Fatal("accepted duplicate zones")
	}
	if _, err := NewZoneHandler(nil); err == nil {
		t.Fatal("accepted nil zone")
	}
	if _, err := NewZoneHandler(&zone.Zone{}); err == nil {
		t.Fatal("accepted uninitialized zone")
	}
}

func TestZoneHandlerCancellationAndValidation(t *testing.T) {
	handler, err := NewZoneHandler(newTestZone(t, "home.arpa."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := handler.Handle(ctx, dnsmessage.Message{}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
	response, err := handler.Handle(t.Context(), dnsmessage.Message{})
	if err != nil || response.RCode != dnsmessage.RCodeFormatError {
		t.Fatal("accepted empty question")
	}
	query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassCHAOS}}}
	response, err = handler.Handle(t.Context(), query)
	if err != nil || response.RCode != dnsmessage.RCodeRefused {
		t.Fatal("accepted unsupported class")
	}
}

func TestZoneHandlerCrossZoneCNAME(t *testing.T) {
	parent := newTestZone(t, "home.arpa.", record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("app.other.arpa.")}))
	other := newTestZone(t, "other.arpa.", record("app.other.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 5}}))
	handler, err := NewZoneHandler(parent, other)
	if err != nil {
		t.Fatal(err)
	}
	got := ask(t, handler, "alias.home.arpa.", dnsmessage.TypeA)
	if got.RCode != dnsmessage.RCodeSuccess || len(got.Answers) != 2 {
		t.Fatal("cross-zone alias did not resolve")
	}

}

func TestZoneHandlerNegativeTTL(t *testing.T) {
	records := baseRecords("home.arpa.")
	records[0].Header.TTL = 30 // Lower than SOA.MinTTL (60).
	z, err := zone.NewZone("home.arpa.", records)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewZoneHandler(z)
	if err != nil {
		t.Fatal(err)
	}
	got := ask(t, handler, "absent.home.arpa.", dnsmessage.TypeA)
	if got.Authorities[0].Header.TTL != 30 {
		t.Fatal("negative TTL exceeds SOA TTL")
	}
}

func TestZoneHandlerRootZone(t *testing.T) {
	z, err := zone.NewZone(".", []dnsmessage.Resource{
		record(".", dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns."), MBox: dnsmessage.MustNewName("hostmaster."), MinTTL: 60}),
		record(".", dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.")}),
		record("app.example.", dnsmessage.TypeA, &dnsmessage.AResource{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewZoneHandler(z)
	if err != nil {
		t.Fatal(err)
	}
	got := ask(t, handler, "example.", dnsmessage.TypeA)
	if got.RCode != dnsmessage.RCodeSuccess || len(got.Authorities) != 1 {
		t.Fatal("root-zone intermediate name missing")
	}
	got = ask(t, handler, "absent.", dnsmessage.TypeA)
	if got.RCode != dnsmessage.RCodeNameError || !got.Authoritative {
		t.Fatal("root zone not authoritative")
	}
}

func TestConcurrentBorrowedRecordEncoding(t *testing.T) {
	z := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"hello", "world"}}))
	handler, err := NewZoneHandler(z)
	if err != nil {
		t.Fatal(err)
	}
	questions := []dnsmessage.Question{
		{Name: dnsmessage.MustNewName("app.home.arpa."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET},
		{Name: dnsmessage.MustNewName("home.arpa."), Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET},
		{Name: dnsmessage.MustNewName("missing.home.arpa."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
	}
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() {
			for i := range 120 {
				question := questions[i%len(questions)]
				response, err := handler.Handle(t.Context(), dnsmessage.Message{Questions: []dnsmessage.Question{question}})
				if err != nil {
					t.Error(err)
					return
				}
				packet, err := response.Pack()
				if err != nil {
					t.Error(err)
					return
				}
				var decoded dnsmessage.Message
				if err := decoded.Unpack(packet); err != nil {
					t.Error(err)
					return
				}
				if question.Type == dnsmessage.TypeA {
					if decoded.RCode != dnsmessage.RCodeNameError || len(decoded.Authorities) != 1 || decoded.Authorities[0].Header.TTL != 60 {
						t.Error("incorrect negative SOA")
						return
					}
				} else if decoded.RCode != dnsmessage.RCodeSuccess || len(decoded.Answers) != 1 || decoded.Answers[0].Header.TTL != 300 {
					t.Error("incorrect positive answer after encoding")
					return
				} else if question.Type == dnsmessage.TypeTXT {
					body := decoded.Answers[0].Body.(*dnsmessage.TXTResource)
					if len(body.TXT) != 2 || body.TXT[0] != "hello" || body.TXT[1] != "world" {
						t.Error("encoding changed TXT data")
						return
					}
				}
			}
		})
	}
	workers.Wait()
	if z.SOA().Header.TTL != 300 {
		t.Fatal("negative answers changed the stored SOA TTL")
	}
}

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
	input := []*zone.Zone{next}
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
		zones []*zone.Zone
	}{
		{"nil zone", []*zone.Zone{nil}},
		{"uninitialized zone", []*zone.Zone{{}}},
		{"duplicate origins", []*zone.Zone{next, next}},
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

func TestReplaceZonesRejectsInvalidZoneEdits(t *testing.T) {
	current := newTestZone(t, "home.arpa.", record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}))
	handler, err := NewZoneHandler(current)
	if err != nil {
		t.Fatal(err)
	}
	candidate := newTestZone(t, "new.arpa.")
	for _, r := range []dnsmessage.Resource{
		record("new.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: dnsmessage.MustNewName("alias.new.arpa.")}),
		record("alias.new.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("mail.new.arpa.")}),
	} {
		if err := candidate.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := handler.ReplaceZones(current, candidate); err == nil {
		t.Fatal("installed a zone with an invalid local target")
	}
	if got := ask(t, handler, "app.home.arpa.", dnsmessage.TypeA); len(got.Answers) != 1 || got.Answers[0].Body.(*dnsmessage.AResource).A[3] != 1 {
		t.Fatal("invalid replacement changed existing data")
	}
	if got := ask(t, handler, "new.arpa.", dnsmessage.TypeMX); got.RCode != dnsmessage.RCodeRefused {
		t.Fatal("invalid replacement added the candidate zone")
	}
}

func TestConcurrentZoneReplacement(t *testing.T) {
	versions := make([][]*zone.Zone, 2)
	for i := range versions {
		target := fmt.Sprintf("v%d.back.arpa.", i+1)
		versions[i] = []*zone.Zone{
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
