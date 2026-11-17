package router

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", msg)
}

func setupLocalRouter(t *testing.T, ip string, port int, neighbors map[string]int, peerAddrs map[string]string, logBuf *syncBuffer) *Router {
	t.Helper()
	dir := t.TempDir()
	if err := SaveRoteadores(filepath.Join(dir, "roteadores.txt"), neighbors); err != nil {
		t.Fatalf("SaveRoteadores error: %v", err)
	}
	opts := Options{
		SelfIP:          ip,
		BindIP:          "127.0.0.1",
		Port:            port,
		LocalMode:       true,
		WorkDir:         dir,
		PeerAddrs:       peerAddrs,
		HelloInterval:   150 * time.Millisecond,
		LSAInterval:     300 * time.Millisecond,
		NeighborTimeout: 600 * time.Millisecond,
		LSAExpiration:   1500 * time.Millisecond,
		LogWriter:       logBuf,
	}
	r, err := New(opts)
	if err != nil {
		t.Fatalf("New router %s error: %v", ip, err)
	}
	if err := r.Start(); err != nil {
		t.Fatalf("Start router %s error: %v", ip, err)
	}
	t.Cleanup(func() { r.Stop() })
	return r
}

func TestFourRoutersConvergenceDataFailAndTestLSA(t *testing.T) {
	// Topology:
	// R1(10.0.0.1:6100) --10-- R2(10.0.0.2:6101) --10-- R4(10.0.0.4:6103)
	//  \--5-- R3(10.0.0.3:6102) --50--/
	// Best R1->R4 initially: R1 -> R2 -> R4 (cost 20, via 10.0.0.2)
	peers := map[string]string{
		"10.0.0.1": "127.0.0.1:6100",
		"10.0.0.2": "127.0.0.1:6101",
		"10.0.0.3": "127.0.0.1:6102",
		"10.0.0.4": "127.0.0.1:6103",
	}
	logs := map[string]*syncBuffer{
		"r1": {}, "r2": {}, "r3": {}, "r4": {},
	}

	r1 := setupLocalRouter(t, "10.0.0.1", 6100, map[string]int{"10.0.0.2": 10, "10.0.0.3": 5}, peers, logs["r1"])
	r2 := setupLocalRouter(t, "10.0.0.2", 6101, map[string]int{"10.0.0.1": 10, "10.0.0.4": 10}, peers, logs["r2"])
	r3 := setupLocalRouter(t, "10.0.0.3", 6102, map[string]int{"10.0.0.1": 5, "10.0.0.4": 50}, peers, logs["r3"])
	r4 := setupLocalRouter(t, "10.0.0.4", 6103, map[string]int{"10.0.0.2": 10, "10.0.0.3": 50}, peers, logs["r4"])

	// 1. Wait for full convergence (all 4 routers know 4 LSAs and R1->R4 = cost 20 via 10.0.0.2)
	waitUntil(t, 4*time.Second, func() bool {
		if len(r1.SnapshotLSDB()) != 4 || len(r4.SnapshotLSDB()) != 4 {
			return false
		}
		for _, rt := range r1.SnapshotRoutes() {
			if rt.Destination == "10.0.0.4" && rt.TotalCost == 20 && rt.NextHop == "10.0.0.2" {
				return true
			}
		}
		return false
	}, "convergência inicial dos 4 roteadores")

	// 2. Send DATA with sufficient TTL=1 (1 intermediate hop R2 -> R4)
	if err := r1.SendData("10.0.0.4", "Oi R4!", 1); err != nil {
		t.Fatalf("SendData error: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(logs["r4"].String(), "DATA_RECEBIDO") && strings.Contains(logs["r4"].String(), "Oi R4!")
	}, "entrega de DATA no R4 com TTL=1")

	// 3. Send DATA with insufficient TTL=0 (R2 must drop it)
	if err := r1.SendData("10.0.0.4", "Vai expirar", 0); err != nil {
		t.Fatalf("SendData TTL=0 error: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(logs["r2"].String(), "TTL_EXPIRADO")
	}, "descarte por TTL_EXPIRADO no R2")

	// 4. Test duplicate & old LSA rejection
	if err := r1.SendTestLSA("10.0.0.2", "10.0.0.4", "dup"); err != nil {
		t.Fatalf("SendTestLSA dup error: %v", err)
	}
	if err := r1.SendTestLSA("10.0.0.2", "10.0.0.4", "old"); err != nil {
		t.Fatalf("SendTestLSA old error: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return strings.Contains(logs["r2"].String(), "ANTIGO_OU_DUPLICADO")
	}, "rejeição de LSA antigo/duplicado no R2")

	// 5. Disable link R1<->R2 -> R1->R4 should reroute via R3 (cost 55)
	if err := r1.DisableLink("10.0.0.2"); err != nil {
		t.Fatalf("DisableLink error: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		for _, rt := range r1.SnapshotRoutes() {
			if rt.Destination == "10.0.0.4" && rt.TotalCost == 55 && rt.NextHop == "10.0.0.3" {
				return true
			}
		}
		return false
	}, "reconvergência após desativar enlace R1-R2")

	// 6. Re-enable link R1<->R2 -> R1->R4 should recover back to cost 20 via R2
	if err := r1.EnableLink("10.0.0.2"); err != nil {
		t.Fatalf("EnableLink error: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		for _, rt := range r1.SnapshotRoutes() {
			if rt.Destination == "10.0.0.4" && rt.TotalCost == 20 && rt.NextHop == "10.0.0.2" {
				return true
			}
		}
		return false
	}, "recuperação da rota após reativar enlace R1-R2")

	// Keep compiler happy about r2/r3/r4 usage
	_ = r2
	_ = r3
	_ = r4
}

func TestSeqPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	seqFile := filepath.Join(dir, "seq.txt")
	if err := SaveRoteadores(filepath.Join(dir, "roteadores.txt"), map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seqFile, []byte("41\n"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		SelfIP:    "10.0.0.10",
		BindIP:    "127.0.0.1",
		Port:      6110,
		LocalMode: true,
		WorkDir:   dir,
		QuietLogs: true,
	}
	r, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()

	recs := r.SnapshotLSDB()
	if len(recs) != 1 || recs[0].Sequence < 42 {
		t.Fatalf("expected sequence >= 42 after restart, got %+v", recs)
	}
	data, err := os.ReadFile(seqFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != fmt.Sprintf("%d", recs[0].Sequence) {
		t.Fatalf("seq.txt not updated before sending LSA: got %q, want %d", string(data), recs[0].Sequence)
	}
}
