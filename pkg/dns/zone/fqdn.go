package zone

import (
	"fmt"
	"strings"
)

// FQDN is a domain name that specifies its exact location in the tree
// hierarchy of the Domain Name System. Also called an absolute
// domain name.
func FQDN(name string) (string, error) {
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
