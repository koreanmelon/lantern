package zone

import (
	"cmp"
	"fmt"
	"slices"

	"golang.org/x/net/dns/dnsmessage"
)

// Records returns a deterministic name/type-ordered list and its revision.
// Resource bodies are borrowed and must remain read-only. RRset order is preserved.
func (z *Zone) Records() ([]dnsmessage.Resource, uint64) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.recordList(), z.revision
}

// recordList requires a read or write lock when the zone is shared.
func (z *Zone) recordList() []dnsmessage.Resource {
	keys := make([]RecordKey, 0, len(z.records))
	for key := range z.records {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b RecordKey) int {
		if order := cmp.Compare(a.name, b.name); order != 0 {
			return order
		}
		return cmp.Compare(a.typ, b.typ)
	})
	var records []dnsmessage.Resource
	for _, key := range keys {
		records = append(records, z.records[key]...)
	}
	return records
}

// ReplaceRecordsIfRevision validates and installs records only if the zone has
// not changed since revision. False with no error denotes a stale revision.
// Validation errors and stale edits leave the zone unchanged.
func (z *Zone) ReplaceRecordsIfRevision(revision uint64, records []dnsmessage.Resource) (bool, error) {
	if z.origin == "" {
		return false, fmt.Errorf("uninitialized zone")
	}
	replacement, err := NewZone(z.origin, records)
	if err != nil {
		return false, err
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.revision != revision {
		return false, nil
	}
	if err := z.commit(replacement); err != nil {
		return false, err
	}
	return true, nil
}
