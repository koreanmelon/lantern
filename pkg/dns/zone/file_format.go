package zone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/dns/dnsmessage"
)

const recordFileVersion = 1
const maxRecordFileBytes = 16 << 20

type recordFile struct {
	Version  int          `json:"version"`
	Origin   string       `json:"origin"`
	Revision uint64       `json:"revision"`
	Records  []fileRecord `json:"records"`
}

// Ordinary records have readable metadata and typed data. Wire is a lossless
// base64 fallback for names or TXT chunks containing non-UTF-8 bytes. It is a
// single-record DNS message, never a second competing source of record fields.
type fileRecord struct {
	Name string          `json:"name,omitempty"`
	Type string          `json:"type,omitempty"`
	TTL  *uint32         `json:"ttl,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	Wire []byte          `json:"wire,omitempty"`
}

type mxData struct {
	Preference uint16 `json:"preference"`
	Exchange   string `json:"exchange"`
}

type srvData struct {
	Priority uint16 `json:"priority"`
	Weight   uint16 `json:"weight"`
	Port     uint16 `json:"port"`
	Target   string `json:"target"`
}

type soaData struct {
	Primary string `json:"primary"`
	Mailbox string `json:"mailbox"`
	Serial  uint32 `json:"serial"`
	Refresh uint32 `json:"refresh"`
	Retry   uint32 `json:"retry"`
	Expire  uint32 `json:"expire"`
	Minimum uint32 `json:"minimum"`
}

func decodeJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON must contain valid UTF-8")
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	scanner.UseNumber()
	if err := validateJSONValue(scanner, 0); err != nil {
		return err
	}
	if _, err := scanner.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// encoding/json otherwise accepts duplicate keys and coerces null into zero
// values for scalar fields. Neither is valid in our versioned storage schema.
func validateJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("record JSON is nested too deeply")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("JSON null is not allowed in record files")
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			// encoding/json matches struct fields case-insensitively. Requiring
			// canonical schema keys prevents differently-cased duplicate aliases.
			for _, r := range name {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
					return fmt.Errorf("record JSON field names must be lower-case ASCII")
				}
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
			if err := validateJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = decoder.Token() // Decoder validates the matching closing delimiter.
	return err
}

func decodeObject(data []byte, target any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := decodeJSON(data, &fields); err != nil {
		return err
	}
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return fmt.Errorf("missing record data field %q", key)
		}
	}
	return decodeJSON(data, target)
}

func storedName(value string) (dnsmessage.Name, error) {
	if _, err := FQDN(value); err != nil {
		return dnsmessage.Name{}, err
	}
	return dnsmessage.NewName(value)
}

func encodeRecord(record dnsmessage.Resource) (fileRecord, error) {
	stringsToCheck := []string{record.Header.Name.String()}
	needsWire := false
	var data any
	switch body := record.Body.(type) {
	case *dnsmessage.AResource:
		data = netip.AddrFrom4(body.A).String()
	case *dnsmessage.AAAAResource:
		data = netip.AddrFrom16(body.AAAA).String()
	case *dnsmessage.CNAMEResource:
		data = body.CNAME.String()
		stringsToCheck = append(stringsToCheck, body.CNAME.String())
	case *dnsmessage.NSResource:
		data = body.NS.String()
		stringsToCheck = append(stringsToCheck, body.NS.String())
	case *dnsmessage.PTRResource:
		data = body.PTR.String()
		stringsToCheck = append(stringsToCheck, body.PTR.String())
	case *dnsmessage.TXTResource:
		data = body.TXT
		stringsToCheck = append(stringsToCheck, body.TXT...)
		needsWire = len(body.TXT) == 0
	case *dnsmessage.MXResource:
		data = mxData{body.Pref, body.MX.String()}
		stringsToCheck = append(stringsToCheck, body.MX.String())
	case *dnsmessage.SRVResource:
		data = srvData{body.Priority, body.Weight, body.Port, body.Target.String()}
		stringsToCheck = append(stringsToCheck, body.Target.String())
	case *dnsmessage.SOAResource:
		data = soaData{body.NS.String(), body.MBox.String(), body.Serial, body.Refresh, body.Retry, body.Expire, body.MinTTL}
		stringsToCheck = append(stringsToCheck, body.NS.String(), body.MBox.String())
	default:
		return fileRecord{}, fmt.Errorf("unsupported stored record type %v", record.Header.Type)
	}
	for _, value := range stringsToCheck {
		if !utf8.ValidString(value) {
			needsWire = true
		}
	}
	if needsWire {
		message := dnsmessage.Message{Answers: []dnsmessage.Resource{record}}
		wire, err := message.Pack()
		return fileRecord{Wire: wire}, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fileRecord{}, err
	}
	ttl := record.Header.TTL
	return fileRecord{Name: record.Header.Name.String(), Type: strings.TrimPrefix(record.Header.Type.String(), "Type"), TTL: &ttl, Data: encoded}, nil
}

func decodeRecord(stored fileRecord) (dnsmessage.Resource, error) {
	if len(stored.Wire) != 0 {
		if stored.Name != "" || stored.Type != "" || stored.TTL != nil || len(stored.Data) != 0 {
			return dnsmessage.Resource{}, fmt.Errorf("wire fallback cannot also contain readable record fields")
		}
		var message dnsmessage.Message
		if err := message.Unpack(stored.Wire); err != nil {
			return dnsmessage.Resource{}, err
		}
		if message.Header != (dnsmessage.Header{}) || len(message.Answers) != 1 || len(message.Questions)+len(message.Authorities)+len(message.Additionals) != 0 {
			return dnsmessage.Resource{}, fmt.Errorf("wire fallback must contain exactly one record")
		}
		canonical, err := message.Pack()
		if err != nil || !bytes.Equal(canonical, stored.Wire) {
			return dnsmessage.Resource{}, fmt.Errorf("wire fallback must use the canonical single-record encoding")
		}
		return message.Answers[0], nil
	}
	name, err := storedName(stored.Name)
	if err != nil {
		return dnsmessage.Resource{}, err
	}
	if stored.TTL == nil {
		return dnsmessage.Resource{}, fmt.Errorf("record TTL is required")
	}
	r := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: *stored.TTL}}
	switch stored.Type {
	case "A", "AAAA":
		var text string
		if err := decodeJSON(stored.Data, &text); err != nil {
			return r, err
		}
		ip, err := netip.ParseAddr(text)
		if err != nil || ip.Zone() != "" {
			return r, fmt.Errorf("invalid stored IP address")
		}
		if stored.Type == "A" {
			if !ip.Is4() {
				return r, fmt.Errorf("A record requires IPv4")
			}
			r.Header.Type, r.Body = dnsmessage.TypeA, &dnsmessage.AResource{A: ip.As4()}
		} else {
			if !ip.Is6() {
				return r, fmt.Errorf("AAAA record requires IPv6")
			}
			r.Header.Type, r.Body = dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: ip.As16()}
		}
	case "CNAME", "NS", "PTR":
		var text string
		if err := decodeJSON(stored.Data, &text); err != nil {
			return r, err
		}
		target, err := storedName(text)
		if err != nil {
			return r, err
		}
		switch stored.Type {
		case "CNAME":
			r.Header.Type, r.Body = dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: target}
		case "NS":
			r.Header.Type, r.Body = dnsmessage.TypeNS, &dnsmessage.NSResource{NS: target}
		case "PTR":
			r.Header.Type, r.Body = dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: target}
		}
	case "TXT":
		var chunks []string
		if err := decodeJSON(stored.Data, &chunks); err != nil {
			return r, err
		}
		if len(chunks) == 0 {
			return r, fmt.Errorf("readable TXT data must contain at least one string")
		}
		r.Header.Type, r.Body = dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: chunks}
	case "MX":
		var data mxData
		if err := decodeObject(stored.Data, &data, "preference", "exchange"); err != nil {
			return r, err
		}
		target, err := storedName(data.Exchange)
		if err != nil {
			return r, err
		}
		r.Header.Type, r.Body = dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: data.Preference, MX: target}
	case "SRV":
		var data srvData
		if err := decodeObject(stored.Data, &data, "priority", "weight", "port", "target"); err != nil {
			return r, err
		}
		target, err := storedName(data.Target)
		if err != nil {
			return r, err
		}
		r.Header.Type, r.Body = dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: data.Priority, Weight: data.Weight, Port: data.Port, Target: target}
	case "SOA":
		var data soaData
		if err := decodeObject(stored.Data, &data, "primary", "mailbox", "serial", "refresh", "retry", "expire", "minimum"); err != nil {
			return r, err
		}
		ns, err := storedName(data.Primary)
		if err != nil {
			return r, err
		}
		mailbox, err := storedName(data.Mailbox)
		if err != nil {
			return r, err
		}
		r.Header.Type, r.Body = dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: ns, MBox: mailbox, Serial: data.Serial, Refresh: data.Refresh, Retry: data.Retry, Expire: data.Expire, MinTTL: data.Minimum}
	default:
		return r, fmt.Errorf("unsupported stored record type %q", stored.Type)
	}
	return r, nil
}

func encodeRecordFile(z *Zone, revision uint64) ([]byte, error) {
	if revision == 0 || revision == ^uint64(0) {
		return nil, fmt.Errorf("invalid or exhausted record revision")
	}
	if !utf8.ValidString(z.origin) {
		return nil, fmt.Errorf("JSON storage requires a UTF-8 zone origin")
	}
	records, _ := z.Records()
	file := recordFile{Version: recordFileVersion, Origin: z.origin, Revision: revision, Records: make([]fileRecord, 0, len(records))}
	for _, record := range records {
		stored, err := encodeRecord(record)
		if err != nil {
			return nil, err
		}
		file.Records = append(file.Records, stored)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data)+1 > maxRecordFileBytes {
		return nil, fmt.Errorf("record file exceeds 16 MiB")
	}
	return append(data, '\n'), nil
}

func decodeRecordFile(data []byte, origin string) (*Zone, uint64, error) {
	var file recordFile
	if err := decodeJSON(data, &file); err != nil {
		return nil, 0, err
	}
	if file.Version != recordFileVersion {
		return nil, 0, fmt.Errorf("unsupported record file version %d", file.Version)
	}
	if file.Revision == 0 || file.Revision == ^uint64(0) {
		return nil, 0, fmt.Errorf("invalid or exhausted record revision")
	}
	name, err := FQDN(file.Origin)
	if err != nil || name != origin {
		return nil, 0, fmt.Errorf("record file origin %q does not match %q", file.Origin, origin)
	}
	records := make([]dnsmessage.Resource, 0, len(file.Records))
	for i, stored := range file.Records {
		record, err := decodeRecord(stored)
		if err != nil {
			return nil, 0, fmt.Errorf("record %d: %w", i, err)
		}
		records = append(records, record)
	}
	z, err := NewZone(origin, records)
	return z, file.Revision, err
}
