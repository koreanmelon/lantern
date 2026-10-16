package zone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func packedRecords(t *testing.T, z *Zone) []byte {
	t.Helper()
	records, _ := z.Records()
	message := dnsmessage.Message{Answers: records}
	data, err := message.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func persistedFixture(t *testing.T) *Zone {
	t.Helper()
	return newTestZone(t, "home.arpa.",
		record("app.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		record("v6.home.arpa.", dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 1, 0xd, 0xb8}}),
		record("alias.home.arpa.", dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("app.home.arpa.")}),
		record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.home.arpa.")}),
		record("_web._tcp.home.arpa.", dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: 1, Weight: 2, Port: 443, Target: dnsmessage.MustNewName("app.home.arpa.")}),
		record("ptr.home.arpa.", dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("app.home.arpa.")}),
		record("text.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"hello", "world\x00\n"}}),
		record("binary.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"\xff\xfe"}}),
		record("empty-text.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{}),
		record("\xff.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}),
	)
}

func TestRecordFileRoundtrip(t *testing.T) {
	z := persistedFixture(t)
	data, err := encodeRecordFile(z, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"data": "192.0.2.1"`)) || !bytes.Contains(data, []byte(`"wire":`)) {
		t.Fatal("expected readable fields and binary fallback")
	}
	loaded, revision, err := decodeRecordFile(data, z.origin)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 7 || !bytes.Equal(packedRecords(t, loaded), packedRecords(t, z)) {
		t.Fatal("persisted data did not roundtrip losslessly")
	}
}

func TestPersistenceAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "records.json")
	z := persistedFixture(t)
	if err := z.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if !z.Persistent() {
		t.Fatal("persistence not enabled")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("record file permissions: %v %v", info, err)
	}
	if err := z.Add(record("added.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"saved"}})); err != nil {
		t.Fatal(err)
	}
	before := packedRecords(t, z)
	_, oldRevision := z.Records()
	restarted := newTestZone(t, "home.arpa.")
	if err := restarted.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packedRecords(t, restarted), before) {
		t.Fatal("startup seed replaced existing records")
	}
	records, newRevision := restarted.Records()
	if newRevision <= oldRevision {
		t.Fatal("restart did not invalidate earlier browser forms")
	}
	if committed, err := restarted.ReplaceRecordsIfRevision(oldRevision, records); err != nil || committed {
		t.Fatal("old process revision committed")
	}
	if committed, err := restarted.ReplaceRecordsIfRevision(newRevision, baseRecords("home.arpa.")); err != nil || !committed {
		t.Fatalf("conditional save: %v", err)
	}
	if err := restarted.EnablePersistence(path); err == nil {
		t.Fatal("enabled persistence twice")
	}
	third := persistedFixture(t)
	if err := third.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if records, _ := third.Records(); len(records) != 2 {
		t.Fatal("conditional edit did not survive restart")
	}
	if err := third.ReplaceRecords(append(baseRecords("home.arpa."), record("final.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{}))); err != nil {
		t.Fatal(err)
	}
	fourth := newTestZone(t, "home.arpa.")
	if err := fourth.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if records, exists := fourth.Lookup("final.home.arpa.", dnsmessage.TypeA); !exists || len(records) != 1 {
		t.Fatal("unconditional replacement was not saved")
	}
}

func TestInvalidRecordFilesAreNotOverwritten(t *testing.T) {
	valid, err := encodeRecordFile(persistedFixture(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"malformed", []byte("{")},
		{"wrong version", bytes.Replace(valid, []byte(`"version": 1`), []byte(`"version": 2`), 1)},
		{"wrong origin", bytes.Replace(valid, []byte(`"origin": "home.arpa."`), []byte(`"origin": "other.arpa."`), 1)},
		{"unknown field", append([]byte(`{"unknown":1,`), valid[1:]...)},
		{"trailing data", append(append([]byte(nil), valid...), []byte("{}")...)},
		{"zero revision", bytes.Replace(valid, []byte(`"revision": 1`), []byte(`"revision": 0`), 1)},
		{"null TXT", bytes.Replace(valid, []byte(`"hello"`), []byte(`null`), 1)},
		{"null numeric field", bytes.Replace(valid, []byte(`"serial": 1`), []byte(`"serial": null`), 1)},
		{"missing numeric field", bytes.Replace(valid, []byte(`"serial": 1,`), nil, 1)},
		{"duplicate key", bytes.Replace(valid, []byte(`"version": 1`), []byte(`"version": 1, "version": 1`), 1)},
		{"case alias key", bytes.Replace(valid, []byte(`"version": 1`), []byte(`"version": 1, "Version": 1`), 1)},
		{"missing SOA", []byte(`{"version":1,"origin":"home.arpa.","revision":1,"records":[]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			z := newTestZone(t, "home.arpa.")
			before := packedRecords(t, z)
			if err := z.EnablePersistence(path); err == nil {
				t.Fatal("invalid file accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, tc.data) || z.Persistent() || !bytes.Equal(before, packedRecords(t, z)) {
				t.Fatal("failed load changed storage or live data")
			}
		})
	}
}

func TestFailedSaveDoesNotPublish(t *testing.T) {
	for _, method := range []string{"add", "replace", "conditional"} {
		t.Run(method, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.json")
			z := newTestZone(t, "home.arpa.")
			if err := z.EnablePersistence(path); err != nil {
				t.Fatal(err)
			}
			oldDisk, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			before, revision := z.Records()
			// A directory at the final path makes rename fail even under root.
			if err := os.Rename(path, path+".backup"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			extra := record("new.home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{})
			switch method {
			case "add":
				err = z.Add(extra)
			case "replace":
				err = z.ReplaceRecords(append(before, extra))
			case "conditional":
				var committed bool
				committed, err = z.ReplaceRecordsIfRevision(revision, append(before, extra))
				if committed {
					t.Fatal("failed save reported a commit")
				}
			}
			if err == nil {
				t.Fatal("disk failure was ignored")
			}
			_, afterRevision := z.Records()
			if _, exists := z.Lookup("new.home.arpa.", dnsmessage.TypeA); exists || afterRevision != revision {
				t.Fatal("failed save changed memory or revision")
			}
			backup, err := os.ReadFile(path + ".backup")
			if err != nil || !bytes.Equal(oldDisk, backup) {
				t.Fatal("failed save damaged previous file")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".lantern-records-") {
					t.Fatal("temporary file leaked")
				}
			}
		})
	}
}

func TestConcurrentPersistentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	z := newTestZone(t, "home.arpa.")
	if err := z.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			r := record(string(rune('a'+i))+".home.arpa.", dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 0, 2, byte(i)}})
			if err := z.Add(r); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, version, err := decodeRecordFile(data, z.origin)
	if err != nil {
		t.Fatal(err)
	}
	_, liveVersion := z.Records()
	if version != liveVersion || !bytes.Equal(packedRecords(t, loaded), packedRecords(t, z)) {
		t.Fatal("concurrent writes diverged on disk and in memory")
	}
}

func TestRecordSchemaRejectsAmbiguousOrInvalidData(t *testing.T) {
	for _, data := range []string{
		`{"name":"a.home.arpa.","type":"A","data":"192.0.2.1"}`,
		`{"name":"a.home.arpa.","type":"A","ttl":60,"data":"::1"}`,
		`{"name":"a.home.arpa.","type":"MX","ttl":60,"data":{"preference":65536,"exchange":"mail.home.arpa."}}`,
		`{"name":"a.home.arpa.","type":"MX","ttl":60,"data":{"preference":null,"exchange":"mail.home.arpa."}}`,
		`{"name":"a.home.arpa.","type":"MX","ttl":60,"data":{"exchange":"mail.home.arpa."}}`,
		`{"name":"a.home.arpa.","type":"TXT","ttl":60,"data":["hello",null]}`,
		`{"name":"a.home.arpa.","type":"SRV","ttl":60,"data":{"unknown":1}}`,
		`{"wire":"AA==","name":"a.home.arpa."}`,
	} {
		var stored fileRecord
		if err := json.Unmarshal([]byte(data), &stored); err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRecord(stored); err == nil {
			t.Fatalf("invalid record accepted: %s", data)
		}
	}
}

func TestWireFallbackRejectsTrailingData(t *testing.T) {
	stored, err := encodeRecord(record("binary.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"\xff"}}))
	if err != nil {
		t.Fatal(err)
	}
	stored.Wire = append(stored.Wire, 0)
	if _, err := decodeRecord(stored); err == nil {
		t.Fatal("wire fallback ignored trailing bytes")
	}
}

func TestReadableDataPreservesExplicitZeros(t *testing.T) {
	for _, r := range []dnsmessage.Resource{
		record("home.arpa.", dnsmessage.TypeMX, &dnsmessage.MXResource{MX: dnsmessage.MustNewName("mail.home.arpa.")}),
		record("_web._tcp.home.arpa.", dnsmessage.TypeSRV, &dnsmessage.SRVResource{Target: dnsmessage.MustNewName("app.home.arpa.")}),
		record("home.arpa.", dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: dnsmessage.MustNewName("ns.home.arpa."), MBox: dnsmessage.MustNewName("hostmaster.home.arpa.")}),
		record("empty.home.arpa.", dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{""}}),
	} {
		stored, err := encodeRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeRecord(stored)
		if err != nil {
			t.Fatal(err)
		}
		before := dnsmessage.Message{Answers: []dnsmessage.Resource{r}}
		after := dnsmessage.Message{Answers: []dnsmessage.Resource{decoded}}
		a, err := before.Pack()
		if err != nil {
			t.Fatal(err)
		}
		b, err := after.Pack()
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal("explicit zero or empty string was changed")
		}
	}
}

func TestRecordFileSizeLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("large record-file boundary")
	}
	t.Run("oversized load", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "records.json")
		data := bytes.Repeat([]byte(" "), maxRecordFileBytes+1)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		z := newTestZone(t, "home.arpa.")
		if err := z.EnablePersistence(path); err == nil {
			t.Fatal("oversized file loaded")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, after) || z.Persistent() {
			t.Fatal("oversized file was changed")
		}
	})
	t.Run("oversized save", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "records.json")
		z := newTestZone(t, "home.arpa.")
		if err := z.EnablePersistence(path); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, version := z.Records()
		chunks := make([]string, 64)
		for i := range chunks {
			chunks[i] = strings.Repeat("x", 255)
		}
		body := &dnsmessage.TXTResource{TXT: chunks}
		records := baseRecords("home.arpa.")
		for i := range 1100 {
			records = append(records, record(fmt.Sprintf("large%d.home.arpa.", i), dnsmessage.TypeTXT, body))
		}
		if err := z.ReplaceRecords(records); err == nil {
			t.Fatal("unreloadable oversized file was saved")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("oversized save changed disk")
		}
		live, current := z.Records()
		if len(live) != 2 || current != version {
			t.Fatal("oversized save changed live data")
		}
	})
}
