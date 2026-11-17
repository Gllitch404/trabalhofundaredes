package router

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"linkstate/internal/protocol"
)

// LoadRoteadores reads roteadores.txt in the spec format: "IPv4;CUSTO" per line.
func LoadRoteadores(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	res := make(map[string]int)
	content := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(content, "\n")
	for idx, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.Split(line, ";")
		if len(parts) != 2 {
			return nil, fmt.Errorf("linha %d inválida em %s: %q", idx+1, path, line)
		}
		ip := strings.TrimSpace(parts[0])
		costStr := strings.TrimSpace(parts[1])
		if !protocol.IsValidIPv4(ip) {
			return nil, fmt.Errorf("IPv4 inválido na linha %d de %s: %q", idx+1, path, ip)
		}
		cost, err := strconv.Atoi(costStr)
		if err != nil || cost <= 0 {
			return nil, fmt.Errorf("custo inválido na linha %d de %s: %q", idx+1, path, costStr)
		}
		res[ip] = cost
	}
	return res, nil
}

// SaveRoteadores writes the configured neighbors map back to roteadores.txt.
func SaveRoteadores(path string, neighbors map[string]int) error {
	keys := make([]string, 0, len(neighbors))
	for ip := range neighbors {
		keys = append(keys, ip)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, ip := range keys {
		fmt.Fprintf(&b, "%s;%d\n", ip, neighbors[ip])
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// LoadSeq reads the last used sequence number from seq.txt.
// If the file does not exist, it returns 0 (so the first LSA uses sequence 1).
func LoadSeq(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if s == "" {
		return 0, nil
	}
	seq, err := strconv.Atoi(s)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("seq.txt inválido: %q", s)
	}
	return seq, nil
}

// SaveSeq persists the sequence number to seq.txt before sending an LSA.
func SaveSeq(path string, seq int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(seq)+"\n"), 0644)
}

// ParsePeersFlag parses "10.0.0.1=6000,10.0.0.2=6001" or "10.0.0.1=127.0.0.1:6000" into a map.
func ParsePeersFlag(raw string) (map[string]string, error) {
	res := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return res, nil
	}
	entries := strings.Split(raw, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		kv := strings.SplitN(entry, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("mapeamento de peer inválido: %q", entry)
		}
		ip := strings.TrimSpace(kv[0])
		target := strings.TrimSpace(kv[1])
		if !protocol.IsValidIPv4(ip) {
			return nil, fmt.Errorf("IPv4 inválido no mapeamento: %q", ip)
		}
		if _, err := strconv.Atoi(target); err == nil {
			target = "127.0.0.1:" + target
		}
		res[ip] = target
	}
	return res, nil
}

// LoadPeersFile reads a local-mode peers.txt file ("IP;PORT" or "IP;HOST:PORT").
func LoadPeersFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	res := make(map[string]string)
	content := strings.TrimPrefix(string(data), "\ufeff")
	for idx, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.SplitN(line, ";", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("linha %d inválida em %s: %q", idx+1, path, line)
		}
		ip := strings.TrimSpace(parts[0])
		target := strings.TrimSpace(parts[1])
		if !protocol.IsValidIPv4(ip) {
			return nil, fmt.Errorf("IPv4 inválido na linha %d de %s: %q", idx+1, path, ip)
		}
		if _, err := strconv.Atoi(target); err == nil {
			target = "127.0.0.1:" + target
		}
		res[ip] = target
	}
	return res, nil
}
