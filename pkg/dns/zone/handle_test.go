package zone

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/koreanmelon/lantern/pkg/dns/server"
	"golang.org/x/net/dns/dnsmessage"
)

func request(name string, typ dnsmessage.Type) dnsserver.Request {
	return dnsserver.Request{Message: dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 42, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}},
	}}
}

func ask(t *testing.T, z *Zone, name string, typ dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	req := request(name, typ)
	response, err := z.Handle(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Response || response.ID != req.Message.ID || !response.RecursionDesired || response.RecursionAvailable ||
		len(response.Questions) != 1 || response.Questions[0] != req.Message.Questions[0] {
		t.Fatalf("incorrect response envelope: %s", response.GoString())
	}
	return response
}

func TestHandleLookup(t *testing.T) {
	z := newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}),
		record("api.apps.home.arpa.", dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 1, 0xd, 0xb8}}),
	)
	for _, tc := range []struct {
		name     string
		typ      dnsmessage.Type
		code     dnsmessage.RCode
		answers  int
		aa       bool
		negative bool
	}{
		{"app.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 2, true, false},
		{"APP.Home.ARPA.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 2, true, false},
		{"api.apps.home.arpa.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 1, true, false},
		{"app.home.arpa.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess, 0, true, true},
		{"apps.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 0, true, true},
		{"missing.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0, true, true},
		{"*.home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeNameError, 0, true, true},
		{"home.arpa.", dnsmessage.TypeSOA, dnsmessage.RCodeSuccess, 1, true, false},
		{"home.arpa.", dnsmessage.TypeNS, dnsmessage.RCodeSuccess, 1, true, false},
		{"home.arpa.", dnsmessage.TypeA, dnsmessage.RCodeSuccess, 0, true, true},
		{"app.home.arpa.", 65000, dnsmessage.RCodeSuccess, 0, true, true},
		{"example.com.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"nothome.arpa.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"*.example.com.", dnsmessage.TypeA, dnsmessage.RCodeRefused, 0, false, false},
		{"app.home.arpa.", dnsmessage.TypeALL, dnsmessage.RCodeRefused, 0, false, false},
		{"home.arpa.", 252, dnsmessage.RCodeRefused, 0, false, false},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.name, tc.typ), func(t *testing.T) {
			got := ask(t, z, tc.name, tc.typ)
			if got.RCode != tc.code || got.Authoritative != tc.aa || len(got.Answers) != tc.answers {
				t.Fatalf("unexpected answer: %s", got.GoString())
			}
			if tc.negative {
				if len(got.Authorities) != 1 || got.Authorities[0].Header.Type != dnsmessage.TypeSOA || got.Authorities[0].Header.TTL != 60 {
					t.Fatal("incorrect negative SOA")
				}
			} else if len(got.Authorities) != 0 {
				t.Fatal("unexpected authority records")
			}
		})
	}
}

func TestHandleCNAME(t *testing.T) {
	alias := func(name, target string) dnsmessage.Resource {
		return record(name+".home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)})
	}
	extra := []dnsmessage.Resource{
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}),
		alias("alias", "APP.home.arpa."), alias("chain", "alias.home.arpa."),
		alias("external", "app.other.arpa."), alias("missing", "absent.home.arpa."),
		alias("loop1", "loop2.home.arpa."), alias("loop2", "loop1.home.arpa."),
	}
	for i := range 18 {
		extra = append(extra, alias(fmt.Sprintf("long%d", i), fmt.Sprintf("long%d.home.arpa.", i+1)))
	}
	extra = append(extra, record("long18.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))
	z := newTestZone(t, "home.arpa.", extra...)
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
			got := ask(t, z, tc.name+".home.arpa.", tc.typ)
			if got.RCode != tc.code || len(got.Answers) != tc.answers || !got.Authoritative || (len(got.Authorities) == 1) != tc.negative {
				t.Fatalf("unexpected alias result: %s", got.GoString())
			}
		})
	}
}

func TestHandleRecordTypesAndEncoding(t *testing.T) {
	txt := &dnsmessage.TXTResource{TXT: []string{"hello", "world"}}
	z := newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeTXT, txt),
		record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.home.arpa.")}),
		record("_web._tcp.home.arpa.", dnsmessage.TypeSRV, &dnsmessage.SRVResource{Port: 443, Target: dnsmessage.MustNewName("app.home.arpa.")}),
		record("ptr.home.arpa.", dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("app.home.arpa.")}),
	)
	for _, tc := range []struct {
		name string
		typ  dnsmessage.Type
	}{
		{"app.home.arpa.", dnsmessage.TypeTXT}, {"home.arpa.", dnsmessage.TypeMX},
		{"_web._tcp.home.arpa.", dnsmessage.TypeSRV}, {"ptr.home.arpa.", dnsmessage.TypePTR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ask(t, z, tc.name, tc.typ)
			if len(got.Answers) != 1 || got.Answers[0].Header.Type != tc.typ {
				t.Fatal("missing record")
			}
			if tc.typ == dnsmessage.TypeTXT && got.Answers[0].Body != txt {
				t.Fatal("record body was deep-copied")
			}
			if _, err := got.Pack(); err != nil {
				t.Fatal(err)
			}
		})
	}
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() {
			for i := range 120 {
				names := []string{"app.home.arpa.", "home.arpa.", "missing.home.arpa."}
				types := []dnsmessage.Type{dnsmessage.TypeTXT, dnsmessage.TypeSOA, dnsmessage.TypeA}
				response, err := z.Handle(t.Context(), request(names[i%3], types[i%3]))
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
				if i%3 == 2 {
					if decoded.RCode != dnsmessage.RCodeNameError || len(decoded.Authorities) != 1 || decoded.Authorities[0].Header.TTL != 60 {
						t.Error("incorrect negative SOA")
						return
					}
				} else if decoded.RCode != dnsmessage.RCodeSuccess || len(decoded.Answers) != 1 || decoded.Answers[0].Header.TTL != 300 {
					t.Error("incorrect positive TTL")
					return
				}
			}
		})
	}
	workers.Wait()
	if z.SOA().Header.TTL != 300 || txt.TXT[0] != "hello" || txt.TXT[1] != "world" {
		t.Fatal("encoding changed stored data")
	}
}

func TestHandleValidationAndLockRelease(t *testing.T) {
	z := newTestZone(t, "home.arpa.")
	for _, tc := range []struct {
		name   string
		modify func(*dnsserver.Request)
		code   dnsmessage.RCode
		cancel bool
	}{
		{"missing question", func(r *dnsserver.Request) { r.Message.Questions = nil }, dnsmessage.RCodeFormatError, false},
		{"response packet", func(r *dnsserver.Request) { r.Message.Response = true }, dnsmessage.RCodeFormatError, false},
		{"opcode", func(r *dnsserver.Request) { r.Message.OpCode = 2 }, dnsmessage.RCodeNotImplemented, false},
		{"class", func(r *dnsserver.Request) { r.Message.Questions[0].Class = dnsmessage.ClassCHAOS }, dnsmessage.RCodeRefused, false},
		{"invalid name", func(r *dnsserver.Request) { r.Message.Questions[0].Name = dnsmessage.MustNewName("relative") }, dnsmessage.RCodeFormatError, false},
		{"negative", func(*dnsserver.Request) {}, dnsmessage.RCodeSuccess, false},
		{"canceled", func(*dnsserver.Request) {}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request("home.arpa.", dnsmessage.TypeA)
			tc.modify(&req)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			got, err := z.Handle(ctx, req)
			if tc.cancel {
				if err != context.Canceled {
					t.Fatalf("got %v", err)
				}
			} else if err != nil || got.RCode != tc.code {
				t.Fatalf("got RCode=%v, error=%v", got.RCode, err)
			}
			if !z.mu.TryLock() {
				t.Fatal("read lock was not released")
			}
			z.mu.Unlock()
		})
	}
	var empty Zone
	if got := ask(t, &empty, "home.arpa.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeRefused || got.Authoritative {
		t.Fatal("zero zone should refuse queries")
	}
}

func TestHandleRootZoneAndNegativeTTL(t *testing.T) {
	z, err := NewZone(".", []dnsmessage.Resource{
		record(".", dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns."), MBox: dnsmessage.MustNewName("hostmaster."), MinTTL: 60}),
		record(".", dnsmessage.TypeNS, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns.")}),
		record("app.example.", dnsmessage.TypeA, &dnsmessage.AResource{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ask(t, z, "example.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeSuccess || len(got.Authorities) != 1 {
		t.Fatal("missing intermediate root-zone name")
	}
	if got := ask(t, z, "absent.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeNameError || !got.Authoritative {
		t.Fatal("root zone not authoritative")
	}
	records := baseRecords("home.arpa.")
	records[0].Header.TTL = 30
	z, err = NewZone("home.arpa.", records)
	if err != nil {
		t.Fatal(err)
	}
	if got := ask(t, z, "missing.home.arpa.", dnsmessage.TypeA); got.Authorities[0].Header.TTL != 30 {
		t.Fatal("negative TTL exceeds SOA TTL")
	}
}

func TestReplaceRecordsAndAdd(t *testing.T) {
	z := newTestZone(t, "home.arpa.", record("old.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}))
	previous := ask(t, z, "old.home.arpa.", dnsmessage.TypeA)
	records := append(baseRecords("home.arpa."), record("new.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}))
	if err := z.ReplaceRecords(records); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, z, "old.home.arpa.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeNameError {
		t.Fatal("old name retained")
	}
	if got := ask(t, z, "new.home.arpa.", dnsmessage.TypeA); got.Answers[0].Body.(*dnsmessage.AResource).A[3] != 2 {
		t.Fatal("replacement missing")
	}
	if previous.Answers[0].Body.(*dnsmessage.AResource).A[3] != 1 {
		t.Fatal("replacement changed old answer")
	}
	for _, tc := range []struct {
		name    string
		records []dnsmessage.Resource
	}{
		{"empty", nil},
		{"outside owner", append(baseRecords("home.arpa."), record("outside.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))},
		{"invalid alias target", append(baseRecords("home.arpa."), record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: dnsmessage.MustNewName("alias.home.arpa.")}), record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("target.home.arpa.")}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := z.ReplaceRecords(tc.records); err == nil {
				t.Fatal("accepted invalid records")
			}
			if got := ask(t, z, "new.home.arpa.", dnsmessage.TypeA); len(got.Answers) != 1 {
				t.Fatal("invalid replacement changed records")
			}
		})
	}
	if err := z.Add(record("added.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"added"}})); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, z, "added.home.arpa.", dnsmessage.TypeTXT); len(got.Answers) != 1 {
		t.Fatal("Add not visible")
	}
	if err := z.Add(record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: dnsmessage.MustNewName("alias.home.arpa.")})); err != nil {
		t.Fatal(err)
	}
	if err := z.Add(record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("target.home.arpa.")})); err == nil {
		t.Fatal("Add bypassed complete-zone validation")
	}
	if got := ask(t, z, "alias.home.arpa.", dnsmessage.TypeA); got.RCode != dnsmessage.RCodeNameError {
		t.Fatal("failed Add left a name behind")
	}
	var empty Zone
	if err := empty.Add(record("a.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{})); err == nil {
		t.Fatal("Add initialized an originless zone")
	}
	if err := empty.ReplaceRecords(records); err == nil {
		t.Fatal("replacement initialized an originless zone")
	}
}

func TestConcurrentRecordUpdates(t *testing.T) {
	versions := make([][]dnsmessage.Resource, 2)
	for i := range versions {
		target := fmt.Sprintf("v%d.home.arpa.", i+1)
		versions[i] = append(baseRecords("home.arpa."),
			record("app.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)}),
			record(target, dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, byte(i + 1)}}),
		)
	}
	z, err := NewZone("home.arpa.", versions[0])
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		workers.Go(func() {
			<-start
			for range 100 {
				if err := z.ReplaceRecords(versions[i]); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 4 {
		workers.Go(func() {
			<-start
			for range 200 {
				got, err := z.Handle(t.Context(), request("app.home.arpa.", dnsmessage.TypeA))
				if err != nil {
					t.Error(err)
					return
				}
				if got.RCode != dnsmessage.RCodeSuccess || len(got.Answers) != 2 {
					t.Error("inconsistent record set")
					return
				}
				alias := got.Answers[0].Body.(*dnsmessage.CNAMEResource)
				address := got.Answers[1].Body.(*dnsmessage.AResource)
				if alias.CNAME.String() != fmt.Sprintf("v%d.home.arpa.", address.A[3]) {
					t.Error("mixed CNAME generations")
					return
				}
				if err := z.Validate(); err != nil {
					t.Error(err)
					return
				}
				_, _ = z.Lookup("app.home.arpa.", dnsmessage.TypeCNAME)
				_ = z.SOA()
				_ = z.Origin()
			}
		})
	}
	close(start)
	workers.Wait()
}

func TestConcurrentAdd(t *testing.T) {
	z := newTestZone(t, "home.arpa.")
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Go(func() {
			name := fmt.Sprintf("app%d.home.arpa.", i)
			if err := z.Add(record(name, dnsmessage.TypeA, &dnsmessage.AResource{})); err != nil {
				t.Error(err)
				return
			}
			if _, err := z.Handle(t.Context(), request(name, dnsmessage.TypeA)); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	for i := range 12 {
		if got := ask(t, z, fmt.Sprintf("app%d.home.arpa.", i), dnsmessage.TypeA); len(got.Answers) != 1 {
			t.Fatal("concurrent Add lost a record")
		}
	}
}
