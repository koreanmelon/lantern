package zone

import (
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRecordRevisions(t *testing.T) {
	z := newTestZone(t, "home.arpa.")
	before, revision := z.Records()
	if revision != 1 {
		t.Fatal("incorrect initial revision")
	}
	if err := z.Add(record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{})); err != nil {
		t.Fatal(err)
	}
	if committed, err := z.ReplaceRecordsIfRevision(revision, before); err != nil || committed {
		t.Fatal("stale update overwrote Add")
	}
	current, version := z.Records()
	if version != revision+1 || len(current) != 3 {
		t.Fatal("Add did not update revision")
	}
	originalTTL := current[0].Header.TTL
	current[0].Header.TTL = 999
	unchanged, _ := z.Records()
	if unchanged[0].Header.TTL != originalTTL {
		t.Fatal("record headers alias the live index")
	}
	current[0].Header.TTL = originalTTL
	for i := range current {
		if i > 0 && current[i-1].Header.Name.String() > current[i].Header.Name.String() {
			t.Fatal("record order is not deterministic")
		}
	}
	if committed, err := z.ReplaceRecordsIfRevision(version, nil); err == nil || committed {
		t.Fatal("invalid update committed")
	}
	_, after := z.Records()
	if after != version {
		t.Fatal("invalid update changed revision")
	}
	var workers sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		workers.Go(func() {
			committed, err := z.ReplaceRecordsIfRevision(version, before)
			if err != nil {
				t.Error(err)
			}
			results <- committed
		})
	}
	workers.Wait()
	close(results)
	commits := 0
	for committed := range results {
		if committed {
			commits++
		}
	}
	if commits != 1 {
		t.Fatalf("got %d commits for one revision", commits)
	}
	if err := z.ReplaceRecords(current); err != nil {
		t.Fatal(err)
	}
	_, last := z.Records()
	if last != version+2 {
		t.Fatal("unconditional replacement did not advance revision")
	}
}

func TestRecordsOrdering(t *testing.T) {
	z := newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}),
		record("app.home.arpa.", dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{}),
	)
	records, _ := z.Records()
	if len(records) != 5 || records[0].Body.(*dnsmessage.AResource).A[3] != 1 || records[1].Body.(*dnsmessage.AResource).A[3] != 2 || records[2].Header.Type != dnsmessage.TypeAAAA || records[3].Header.Type != dnsmessage.TypeNS || records[4].Header.Type != dnsmessage.TypeSOA {
		t.Fatal("records were not sorted by owner/type while preserving RRset order")
	}
}
