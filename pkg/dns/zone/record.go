package zone

import (
	"fmt"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

// RecordKey identifies an RRset by its canonical owner name and record type.
type RecordKey struct {
	name string
	typ  dnsmessage.Type
}

func fqdn(name string) (string, error) {
	if name == "." {
		return name, nil
	}
	if !strings.HasSuffix(name, ".") || len(name) > 254 {
		return "", fmt.Errorf("invalid absolute DNS name %q", name)
	}
	for label := range strings.SplitSeq(name[:len(name)-1], ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("invalid or unsupported label in %q", name)
		}
	}
	// DNS case folding is ASCII-only, not Unicode case conversion.
	result := []byte(name)
	for i, b := range result {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + ('a' - 'A')
		}
	}
	return string(result), nil
}
