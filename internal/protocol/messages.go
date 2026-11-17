package protocol

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultPort    = 6000
	MaxPacketBytes = 1200
)

var (
	ErrPacketTooLarge  = errors.New("pacote excede 1200 bytes")
	ErrInvalidUTF8     = errors.New("mensagem não é UTF-8 válida")
	ErrTrailingNewline = errors.New("pacote contém quebra de linha ao final")
	ErrMalformed       = errors.New("mensagem malformada")
	ErrInvalidIPv4     = errors.New("endereço IPv4 inválido")
	ErrInvalidCost     = errors.New("custo inválido (deve ser inteiro positivo)")
	ErrInvalidSeq      = errors.New("número de sequência inválido")
	ErrInvalidTTL      = errors.New("TTL inválido (deve estar entre 0 e 255)")
)

type MessageType int

const (
	TypeHello MessageType = iota + 1
	TypeLSA
	TypeDATA
)

type HelloMessage struct {
	OriginIP  string
	Neighbors []string
}

type LSAMessage struct {
	OriginIP string
	Sequence int
	Links    map[string]int
}

type DataMessage struct {
	OriginIP string
	DestIP   string
	ID       int
	TTL      int
	Payload  string
}

// IsValidIPv4 checks whether s is a canonical IPv4 dotted-decimal address.
func IsValidIPv4(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4.String() == s
}

// ValidateRawPacket enforces UTF-8, max 1200 bytes, and no trailing newline.
func ValidateRawPacket(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", ErrMalformed
	}
	if len(raw) > MaxPacketBytes {
		return "", ErrPacketTooLarge
	}
	if !utf8.Valid(raw) {
		return "", ErrInvalidUTF8
	}
	if raw[len(raw)-1] == '\n' || raw[len(raw)-1] == '\r' {
		return "", ErrTrailingNewline
	}
	return string(raw), nil
}

// ParsePacket validates and decodes a raw UDP payload into HelloMessage, LSAMessage, or DataMessage.
func ParsePacket(raw []byte) (MessageType, any, error) {
	s, err := ValidateRawPacket(raw)
	if err != nil {
		return 0, nil, err
	}
	switch s[0] {
	case '@':
		msg, err := ParseHello(s)
		return TypeHello, msg, err
	case '#':
		msg, err := ParseLSA(s)
		return TypeLSA, msg, err
	case '!':
		msg, err := ParseDATA(s)
		return TypeDATA, msg, err
	default:
		return 0, nil, ErrMalformed
	}
}

func ParseHello(s string) (*HelloMessage, error) {
	if len(s) < 2 || s[0] != '@' {
		return nil, ErrMalformed
	}
	parts := strings.Split(s[1:], "*")
	origin := parts[0]
	if !IsValidIPv4(origin) {
		return nil, ErrInvalidIPv4
	}
	neighbors := make([]string, 0, len(parts)-1)
	seen := make(map[string]struct{}, len(parts)-1)
	for _, n := range parts[1:] {
		if !IsValidIPv4(n) {
			return nil, ErrInvalidIPv4
		}
		if _, dup := seen[n]; !dup {
			seen[n] = struct{}{}
			neighbors = append(neighbors, n)
		}
	}
	return &HelloMessage{
		OriginIP:  origin,
		Neighbors: neighbors,
	}, nil
}

func FormatHello(originIP string, neighbors []string) (string, error) {
	if !IsValidIPv4(originIP) {
		return "", ErrInvalidIPv4
	}
	sorted := make([]string, 0, len(neighbors))
	for _, n := range neighbors {
		if !IsValidIPv4(n) {
			return "", ErrInvalidIPv4
		}
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	var b strings.Builder
	b.WriteByte('@')
	b.WriteString(originIP)
	for _, n := range sorted {
		b.WriteByte('*')
		b.WriteString(n)
	}
	out := b.String()
	if len(out) > MaxPacketBytes {
		return "", ErrPacketTooLarge
	}
	return out, nil
}

func ParseLSA(s string) (*LSAMessage, error) {
	if len(s) < 4 || s[0] != '#' {
		return nil, ErrMalformed
	}
	sections := strings.Split(s[1:], "*")
	header := strings.Split(sections[0], ";")
	if len(header) != 2 {
		return nil, ErrMalformed
	}
	origin := header[0]
	if !IsValidIPv4(origin) {
		return nil, ErrInvalidIPv4
	}
	seq, err := strconv.Atoi(header[1])
	if err != nil || seq <= 0 {
		return nil, ErrInvalidSeq
	}

	links := make(map[string]int, len(sections)-1)
	for _, sec := range sections[1:] {
		kv := strings.Split(sec, ";")
		if len(kv) != 2 {
			return nil, ErrMalformed
		}
		nb := kv[0]
		if !IsValidIPv4(nb) {
			return nil, ErrInvalidIPv4
		}
		cost, err := strconv.Atoi(kv[1])
		if err != nil || cost <= 0 {
			return nil, ErrInvalidCost
		}
		links[nb] = cost
	}

	return &LSAMessage{
		OriginIP: origin,
		Sequence: seq,
		Links:    links,
	}, nil
}

func FormatLSA(originIP string, seq int, links map[string]int) (string, error) {
	if !IsValidIPv4(originIP) {
		return "", ErrInvalidIPv4
	}
	if seq <= 0 {
		return "", ErrInvalidSeq
	}
	keys := make([]string, 0, len(links))
	for nb, cost := range links {
		if !IsValidIPv4(nb) {
			return "", ErrInvalidIPv4
		}
		if cost <= 0 {
			return "", ErrInvalidCost
		}
		keys = append(keys, nb)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('#')
	b.WriteString(originIP)
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(seq))
	for _, nb := range keys {
		b.WriteByte('*')
		b.WriteString(nb)
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(links[nb]))
	}
	out := b.String()
	if len(out) > MaxPacketBytes {
		return "", ErrPacketTooLarge
	}
	return out, nil
}

func ParseDATA(s string) (*DataMessage, error) {
	if len(s) < 8 || s[0] != '!' {
		return nil, ErrMalformed
	}
	parts := strings.SplitN(s[1:], ";", 5)
	if len(parts) != 5 {
		return nil, ErrMalformed
	}
	origin := parts[0]
	dest := parts[1]
	if !IsValidIPv4(origin) || !IsValidIPv4(dest) {
		return nil, ErrInvalidIPv4
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil || id < 0 {
		return nil, ErrMalformed
	}
	ttl, err := strconv.Atoi(parts[3])
	if err != nil || ttl < 0 || ttl > 255 {
		return nil, ErrInvalidTTL
	}
	return &DataMessage{
		OriginIP: origin,
		DestIP:   dest,
		ID:       id,
		TTL:      ttl,
		Payload:  parts[4],
	}, nil
}

func FormatDATA(originIP, destIP string, id, ttl int, payload string) (string, error) {
	if !IsValidIPv4(originIP) || !IsValidIPv4(destIP) {
		return "", ErrInvalidIPv4
	}
	if id < 0 {
		return "", ErrMalformed
	}
	if ttl < 0 || ttl > 255 {
		return "", ErrInvalidTTL
	}
	if strings.HasSuffix(payload, "\n") || strings.HasSuffix(payload, "\r") {
		return "", ErrTrailingNewline
	}
	out := fmt.Sprintf("!%s;%s;%d;%d;%s", originIP, destIP, id, ttl, payload)
	if len(out) > MaxPacketBytes {
		return "", ErrPacketTooLarge
	}
	return out, nil
}
