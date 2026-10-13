package dns

import (
	"bytes"
	"reflect"
	"slices"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestParseQuery(t *testing.T) {
	want := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 1234, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: dnsmessage.MustNewName("printer.home.arpa."),
			Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET,
		}},
	}
	packet, err := want.Pack()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header != want.Header || !slices.Equal(got.Questions, want.Questions) ||
		len(got.Answers) != 0 || len(got.Authorities) != 0 || len(got.Additionals) != 0 {
		t.Fatalf("unexpected query: %s", got.GoString())
	}
}

func TestParseCompressedResponse(t *testing.T) {
	// The answer name points back to the question name at offset 12.
	packet := []byte{
		0x12, 0x34, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0,
		0, 1, 0, 1,
		0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4,
		192, 0, 2, 1,
	}
	message, err := Parse(packet)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != 0x1234 || !message.Response || len(message.Answers) != 1 {
		t.Fatalf("unexpected response: %#v", message)
	}
	answer := message.Answers[0]
	if answer.Header.Name.String() != "example.com." || answer.Header.TTL != 60 {
		t.Fatalf("unexpected answer header: %#v", answer.Header)
	}
	body, ok := answer.Body.(*dnsmessage.AResource)
	if !ok || body.A != [4]byte{192, 0, 2, 1} {
		t.Fatalf("unexpected answer body: %#v", answer.Body)
	}
	// Parsed results must survive reuse of the input buffer.
	clear(packet)
	if answer.Header.Name.String() != "example.com." || body.A != [4]byte{192, 0, 2, 1} {
		t.Fatal("parsed response aliases the input buffer")
	}
}

func TestParseMalformed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		packet []byte
	}{
		{"empty", nil},
		{"short header", make([]byte, 11)},
		{"missing question", []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}},
		{"pointer loop", []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, err := Parse(tc.packet)
			if err == nil {
				t.Fatal("expected parse error")
			}
			if !reflect.DeepEqual(message, dnsmessage.Message{}) {
				t.Fatal("returned a partial message on error")
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0}, 12))
	f.Add([]byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1})
	f.Fuzz(func(t *testing.T, packet []byte) {
		_, _ = Parse(packet)
	})
}
