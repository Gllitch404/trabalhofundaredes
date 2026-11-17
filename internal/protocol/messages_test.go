package protocol

import (
	"strings"
	"testing"
)

func TestHelloRoundTrip(t *testing.T) {
	msg, err := FormatHello("192.168.1.1", []string{"192.168.1.3", "192.168.1.2"})
	if err != nil {
		t.Fatalf("FormatHello error: %v", err)
	}
	if msg != "@192.168.1.1*192.168.1.2*192.168.1.3" {
		t.Fatalf("unexpected formatted hello: %q", msg)
	}
	typ, parsed, err := ParsePacket([]byte(msg))
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if typ != TypeHello {
		t.Fatalf("expected TypeHello, got %v", typ)
	}
	hm := parsed.(*HelloMessage)
	if hm.OriginIP != "192.168.1.1" || len(hm.Neighbors) != 2 {
		t.Fatalf("unexpected parsed hello: %+v", hm)
	}
}

func TestLSARoundTripAndValidation(t *testing.T) {
	msg, err := FormatLSA("192.168.1.1", 17, map[string]int{
		"192.168.1.2": 10,
		"192.168.1.3": 5,
	})
	if err != nil {
		t.Fatalf("FormatLSA error: %v", err)
	}
	if msg != "#192.168.1.1;17*192.168.1.2;10*192.168.1.3;5" {
		t.Fatalf("unexpected formatted lsa: %q", msg)
	}
	typ, parsed, err := ParsePacket([]byte(msg))
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if typ != TypeLSA {
		t.Fatalf("expected TypeLSA, got %v", typ)
	}
	lsa := parsed.(*LSAMessage)
	if lsa.Sequence != 17 || lsa.Links["192.168.1.2"] != 10 || lsa.Links["192.168.1.3"] != 5 {
		t.Fatalf("unexpected parsed lsa: %+v", lsa)
	}

	// Zero neighbors LSA
	emptyLSA, err := FormatLSA("192.168.1.1", 1, nil)
	if err != nil {
		t.Fatalf("FormatLSA empty error: %v", err)
	}
	if emptyLSA != "#192.168.1.1;1" {
		t.Fatalf("unexpected empty lsa: %q", emptyLSA)
	}
	if _, _, err := ParsePacket([]byte(emptyLSA)); err != nil {
		t.Fatalf("ParsePacket empty lsa failed: %v", err)
	}
}

func TestDATARoundTripAndSemicolons(t *testing.T) {
	msg, err := FormatDATA("192.168.1.1", "192.168.1.7", 1001, 16, "Olá; pessoal!")
	if err != nil {
		t.Fatalf("FormatDATA error: %v", err)
	}
	typ, parsed, err := ParsePacket([]byte(msg))
	if err != nil {
		t.Fatalf("ParsePacket error: %v", err)
	}
	if typ != TypeDATA {
		t.Fatalf("expected TypeDATA, got %v", typ)
	}
	dm := parsed.(*DataMessage)
	if dm.OriginIP != "192.168.1.1" || dm.DestIP != "192.168.1.7" || dm.ID != 1001 || dm.TTL != 16 || dm.Payload != "Olá; pessoal!" {
		t.Fatalf("unexpected parsed data: %+v", dm)
	}
}

func TestInvalidPacketsRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"trailing_lf", []byte("@192.168.1.1*192.168.1.2\n")},
		{"trailing_crlf", []byte("@192.168.1.1*192.168.1.2\r\n")},
		{"too_large", []byte("!192.168.1.1;192.168.1.2;1;16;" + strings.Repeat("a", 1200))},
		{"invalid_utf8", []byte{0x21, 0xff, 0xfe}},
		{"bad_ipv4", []byte("@999.168.1.1*192.168.1.2")},
		{"zero_cost", []byte("#192.168.1.1;1*192.168.1.2;0")},
		{"negative_cost", []byte("#192.168.1.1;1*192.168.1.2;-5")},
		{"zero_seq", []byte("#192.168.1.1;0*192.168.1.2;5")},
		{"ttl_over_255", []byte("!192.168.1.1;192.168.1.2;1;256;oi")},
		{"unknown_prefix", []byte("?192.168.1.1")},
	}
	for _, tc := range cases {
		if _, _, err := ParsePacket(tc.raw); err == nil {
			t.Errorf("case %s: expected error, got nil", tc.name)
		}
	}
}
