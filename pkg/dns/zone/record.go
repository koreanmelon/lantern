package zone

import (
	"golang.org/x/net/dns/dnsmessage"
)

// RecordKey identifies an RRset by its canonical owner name and record type.
type RecordKey struct {
	name string
	typ  dnsmessage.Type
}
