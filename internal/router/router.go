package router

import (
	"fmt"
	"io"
	"maps"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"linkstate/internal/protocol"
	"linkstate/internal/routing"
)

type Options struct {
	SelfIP          string
	BindIP          string
	Port            int
	LocalMode       bool
	WorkDir         string
	PeerAddrs       map[string]string // logical IP -> "host:port" (used in LocalMode)
	HelloInterval   time.Duration
	LSAInterval     time.Duration
	NeighborTimeout time.Duration
	LSAExpiration   time.Duration
	LogWriter       io.Writer
	QuietLogs       bool
}

func DefaultOptions() Options {
	return Options{
		BindIP:          "0.0.0.0",
		Port:            protocol.DefaultPort,
		WorkDir:         ".",
		PeerAddrs:       make(map[string]string),
		HelloInterval:   5 * time.Second,
		LSAInterval:     10 * time.Second,
		NeighborTimeout: 15 * time.Second,
		LSAExpiration:   30 * time.Second,
	}
}

type NeighborState struct {
	IP        string
	Cost      int
	Disabled  bool
	Active    bool
	LastHello time.Time
}

type Router struct {
	opts      Options
	conn      *net.UDPConn
	mu        sync.Mutex
	neighbors map[string]*NeighborState
	lsdb      map[string]routing.LSARecord
	maxSeq    map[string]int
	routes    map[string]routing.Route
	lastSeq   int
	nextMsgID int
	rng       *rand.Rand

	peerAddrs map[string]string // logical IP -> host:port
	addrToIP  map[string]string // host:port -> logical IP

	roteadoresPath string
	seqPath        string
	logFile        *os.File
	logWriter      io.Writer

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func New(opts Options) (*Router, error) {
	if !protocol.IsValidIPv4(opts.SelfIP) {
		return nil, fmt.Errorf("IP próprio inválido: %q", opts.SelfIP)
	}
	if opts.Port <= 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("porta inválida: %d", opts.Port)
	}
	if opts.WorkDir == "" {
		opts.WorkDir = "."
	}
	if opts.HelloInterval <= 0 {
		opts.HelloInterval = 5 * time.Second
	}
	if opts.LSAInterval <= 0 {
		opts.LSAInterval = 10 * time.Second
	}
	if opts.NeighborTimeout <= 0 {
		opts.NeighborTimeout = 15 * time.Second
	}
	if opts.LSAExpiration <= 0 {
		opts.LSAExpiration = 30 * time.Second
	}
	if opts.BindIP == "" {
		if opts.LocalMode {
			opts.BindIP = "127.0.0.1"
		} else {
			opts.BindIP = "0.0.0.0"
		}
	}

	rotPath := filepath.Join(opts.WorkDir, "roteadores.txt")
	seqPath := filepath.Join(opts.WorkDir, "seq.txt")

	cfgNeighbors, err := LoadRoteadores(rotPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler %s: %w", rotPath, err)
	}
	lastSeq, err := LoadSeq(seqPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler %s: %w", seqPath, err)
	}

	peerAddrs := make(map[string]string)
	addrToIP := make(map[string]string)
	if opts.LocalMode {
		filePeers, err := LoadPeersFile(filepath.Join(opts.WorkDir, "peers.txt"))
		if err != nil {
			return nil, fmt.Errorf("erro ao ler peers.txt: %w", err)
		}
		for k, v := range filePeers {
			peerAddrs[k] = v
			addrToIP[v] = k
		}
		for k, v := range opts.PeerAddrs {
			peerAddrs[k] = v
			addrToIP[v] = k
		}
		selfAddr := fmt.Sprintf("%s:%d", opts.BindIP, opts.Port)
		peerAddrs[opts.SelfIP] = selfAddr
		addrToIP[selfAddr] = opts.SelfIP
	}

	var logFile *os.File
	var lw io.Writer = os.Stdout
	if opts.LogWriter != nil {
		lw = opts.LogWriter
	} else {
		lf, err := os.OpenFile(filepath.Join(opts.WorkDir, "router.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			logFile = lf
			lw = io.MultiWriter(os.Stdout, lf)
		}
	}

	neighbors := make(map[string]*NeighborState, len(cfgNeighbors))
	for ip, cost := range cfgNeighbors {
		if ip == opts.SelfIP {
			continue
		}
		neighbors[ip] = &NeighborState{
			IP:   ip,
			Cost: cost,
		}
	}

	r := &Router{
		opts:           opts,
		neighbors:      neighbors,
		lsdb:           make(map[string]routing.LSARecord),
		maxSeq:         make(map[string]int),
		routes:         make(map[string]routing.Route),
		lastSeq:        lastSeq,
		nextMsgID:      1001,
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
		peerAddrs:      peerAddrs,
		addrToIP:       addrToIP,
		roteadoresPath: rotPath,
		seqPath:        seqPath,
		logFile:        logFile,
		logWriter:      lw,
		stopCh:         make(chan struct{}),
	}
	return r, nil
}

func (r *Router) Start() error {
	bindAddr := &net.UDPAddr{
		IP:   net.ParseIP(r.opts.BindIP),
		Port: r.opts.Port,
	}
	conn, err := net.ListenUDP("udp4", bindAddr)
	if err != nil {
		return fmt.Errorf("erro ao abrir socket UDP em %s:%d: %w", r.opts.BindIP, r.opts.Port, err)
	}
	r.conn = conn

	modeLabel := "ESPECIFICACAO"
	if r.opts.LocalMode {
		modeLabel = "LOCAL_LOOPBACK"
	}
	r.logEvent("INICIALIZACAO", r.opts.SelfIP, fmt.Sprintf("MODO=%s | SOCKET=%s:%d", modeLabel, r.opts.BindIP, r.opts.Port))

	// Generate initial local LSA and run initial Dijkstra immediately.
	r.mu.Lock()
	r.publishLocalLSALocked(false)
	r.runDijkstraLocked()
	r.sendHelloLocked()
	r.mu.Unlock()

	r.wg.Add(2)
	go r.receiveLoop()
	go r.timerLoop()
	return nil
}

func (r *Router) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
		if r.conn != nil {
			_ = r.conn.Close()
		}
		r.wg.Wait()
		if r.logFile != nil {
			_ = r.logFile.Close()
		}
	})
}

func (r *Router) logEvent(eventType, origin, desc string) {
	if r.opts.QuietLogs {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	var line string
	switch {
	case origin != "" && desc != "":
		line = fmt.Sprintf("[%s] %s | %s | %s\n", ts, eventType, origin, desc)
	case origin != "":
		line = fmt.Sprintf("[%s] %s | %s\n", ts, eventType, origin)
	case desc != "":
		line = fmt.Sprintf("[%s] %s | %s\n", ts, eventType, desc)
	default:
		line = fmt.Sprintf("[%s] %s\n", ts, eventType)
	}
	_, _ = io.WriteString(r.logWriter, line)
}

func (r *Router) resolveUDPAddrLocked(targetIP string) (*net.UDPAddr, error) {
	if r.opts.LocalMode {
		hostPort, ok := r.peerAddrs[targetIP]
		if !ok {
			return nil, fmt.Errorf("peer %s sem mapeamento de porta local", targetIP)
		}
		return net.ResolveUDPAddr("udp4", hostPort)
	}
	return &net.UDPAddr{
		IP:   net.ParseIP(targetIP),
		Port: r.opts.Port,
	}, nil
}

func (r *Router) sendRawLocked(targetIP string, payload string) error {
	if len(payload) > protocol.MaxPacketBytes {
		return protocol.ErrPacketTooLarge
	}
	addr, err := r.resolveUDPAddrLocked(targetIP)
	if err != nil {
		return err
	}
	_, err = r.conn.WriteToUDP([]byte(payload), addr)
	return err
}

func (r *Router) receiveLoop() {
	defer r.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, remoteAddr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.stopCh:
				return
			default:
				continue
			}
		}

		raw := make([]byte, n)
		copy(raw, buf[:n])

		msgType, parsed, err := protocol.ParsePacket(raw)
		if err != nil {
			r.logEvent("PACOTE_INVALIDO", remoteAddr.String(), err.Error())
			continue
		}

		r.mu.Lock()
		senderIP := r.identifySenderLocked(remoteAddr, msgType, parsed)
		switch msgType {
		case protocol.TypeHello:
			r.handleHelloLocked(senderIP, parsed.(*protocol.HelloMessage))
		case protocol.TypeLSA:
			r.handleLSALocked(senderIP, parsed.(*protocol.LSAMessage))
		case protocol.TypeDATA:
			r.handleDATALocked(parsed.(*protocol.DataMessage))
		}
		r.mu.Unlock()
	}
}

func (r *Router) identifySenderLocked(remoteAddr *net.UDPAddr, msgType protocol.MessageType, parsed any) string {
	if r.opts.LocalMode {
		if ip, ok := r.addrToIP[remoteAddr.String()]; ok {
			return ip
		}
		if msgType == protocol.TypeHello {
			// Allow learning loopback port mapping if a configured peer sends from its bound socket.
			hm := parsed.(*protocol.HelloMessage)
			if _, isNeighbor := r.neighbors[hm.OriginIP]; isNeighbor {
				r.peerAddrs[hm.OriginIP] = remoteAddr.String()
				r.addrToIP[remoteAddr.String()] = hm.OriginIP
				return hm.OriginIP
			}
		}
		return remoteAddr.String()
	}
	if ip4 := remoteAddr.IP.To4(); ip4 != nil {
		return ip4.String()
	}
	return remoteAddr.IP.String()
}

func (r *Router) timerLoop() {
	defer r.wg.Done()
	helloTicker := time.NewTicker(r.opts.HelloInterval)
	lsaTicker := time.NewTicker(r.opts.LSAInterval)
	checkTicker := time.NewTicker(500 * time.Millisecond)
	defer helloTicker.Stop()
	defer lsaTicker.Stop()
	defer checkTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-helloTicker.C:
			r.mu.Lock()
			r.sendHelloLocked()
			r.mu.Unlock()
		case <-lsaTicker.C:
			r.mu.Lock()
			r.publishLocalLSALocked(true)
			r.mu.Unlock()
		case <-checkTicker.C:
			r.mu.Lock()
			r.checkTimeoutsLocked()
			r.mu.Unlock()
		}
	}
}

func (r *Router) sendHelloLocked() {
	configured := make([]string, 0, len(r.neighbors))
	for ip := range r.neighbors {
		configured = append(configured, ip)
	}
	payload, err := protocol.FormatHello(r.opts.SelfIP, configured)
	if err != nil {
		r.logEvent("ERRO_HELLO", r.opts.SelfIP, err.Error())
		return
	}
	for ip, nb := range r.neighbors {
		if nb.Disabled {
			continue
		}
		_ = r.sendRawLocked(ip, payload)
	}
}

func (r *Router) handleHelloLocked(senderIP string, msg *protocol.HelloMessage) {
	if msg.OriginIP != senderIP {
		// In spec mode, HELLO OriginIP must match sender's IP.
		r.logEvent("HELLO_DESCARTADO", senderIP, fmt.Sprintf("ORIGEM_DIVERGENTE=%s", msg.OriginIP))
		return
	}
	nb, exists := r.neighbors[msg.OriginIP]
	if !exists {
		r.logEvent("HELLO_DESCARTADO", msg.OriginIP, "NAO_CONFIGURADO")
		return
	}
	if nb.Disabled {
		r.logEvent("HELLO_DESCARTADO", msg.OriginIP, "ENLACE_DESATIVADO")
		return
	}

	// Verify mutual neighbor configuration.
	mutual := false
	for _, n := range msg.Neighbors {
		if n == r.opts.SelfIP {
			mutual = true
			break
		}
	}
	if !mutual {
		r.logEvent("HELLO_DESCARTADO", msg.OriginIP, "VIZINHANCA_NAO_MUTUA")
		return
	}

	nb.LastHello = time.Now()
	r.logEvent("HELLO_RECEBIDO", msg.OriginIP, "")

	if !nb.Active {
		nb.Active = true
		r.logEvent("VIZINHO_ATIVO", msg.OriginIP, fmt.Sprintf("CUSTO=%d", nb.Cost))

		// Update local LSA first, store in LSDB, flood to other active neighbors, and sync full LSDB to new neighbor.
		r.publishLocalLSALocked(false)
		r.syncLSDBToNeighborLocked(msg.OriginIP)
		r.floodLocalLSALocked(msg.OriginIP)
		r.runDijkstraLocked()
	}
}

func (r *Router) activeLinksLocked() map[string]int {
	links := make(map[string]int)
	for ip, nb := range r.neighbors {
		if nb.Active && !nb.Disabled {
			links[ip] = nb.Cost
		}
	}
	return links
}

func (r *Router) publishLocalLSALocked(floodAll bool) {
	nextSeq := r.lastSeq + 1
	if err := SaveSeq(r.seqPath, nextSeq); err != nil {
		r.logEvent("ERRO_SEQ", r.opts.SelfIP, err.Error())
		return
	}
	r.lastSeq = nextSeq

	links := r.activeLinksLocked()
	r.lsdb[r.opts.SelfIP] = routing.LSARecord{
		OriginIP:   r.opts.SelfIP,
		Sequence:   nextSeq,
		Links:      links,
		ReceivedAt: time.Now(),
	}
	r.maxSeq[r.opts.SelfIP] = nextSeq

	if floodAll {
		r.floodLocalLSALocked("")
	}
}

func (r *Router) floodLocalLSALocked(exceptIP string) {
	rec, ok := r.lsdb[r.opts.SelfIP]
	if !ok {
		return
	}
	payload, err := protocol.FormatLSA(rec.OriginIP, rec.Sequence, rec.Links)
	if err != nil {
		r.logEvent("ERRO_LSA", r.opts.SelfIP, err.Error())
		return
	}
	for ip, nb := range r.neighbors {
		if nb.Active && !nb.Disabled && ip != exceptIP {
			_ = r.sendRawLocked(ip, payload)
		}
	}
	r.logEvent("LSA_ENVIADO", r.opts.SelfIP, fmt.Sprintf("SEQ=%d", rec.Sequence))
}

func (r *Router) syncLSDBToNeighborLocked(neighborIP string) {
	now := time.Now()
	origins := make([]string, 0, len(r.lsdb))
	for origin := range r.lsdb {
		origins = append(origins, origin)
	}
	sort.Strings(origins)

	for _, origin := range origins {
		rec := r.lsdb[origin]
		if origin != r.opts.SelfIP && now.Sub(rec.ReceivedAt) > r.opts.LSAExpiration {
			continue
		}
		payload, err := protocol.FormatLSA(rec.OriginIP, rec.Sequence, rec.Links)
		if err != nil {
			continue
		}
		_ = r.sendRawLocked(neighborIP, payload)
	}
	r.logEvent("LSDB_SINCRONIZADA", neighborIP, fmt.Sprintf("LSAS=%d", len(origins)))
}

func (r *Router) handleLSALocked(senderIP string, msg *protocol.LSAMessage) {
	// Rule: Discard LSA with our own origin IP without processing or forwarding.
	if msg.OriginIP == r.opts.SelfIP {
		r.logEvent("LSA_DESCARTADO", msg.OriginIP, fmt.Sprintf("SEQ=%d | PROPRIA_ORIGEM", msg.Sequence))
		return
	}

	// Rule: Accept LSA only from an active, non-disabled neighbor.
	nb, exists := r.neighbors[senderIP]
	if !exists || nb.Disabled || !nb.Active {
		r.logEvent("LSA_DESCARTADO", msg.OriginIP, fmt.Sprintf("SEQ=%d | REMETENTE_INATIVO=%s", msg.Sequence, senderIP))
		return
	}

	// Rule: Discard duplicates or older sequence numbers (even after expiration).
	if prevMax, seen := r.maxSeq[msg.OriginIP]; seen && msg.Sequence <= prevMax {
		r.logEvent("LSA_DESCARTADO", msg.OriginIP, fmt.Sprintf("SEQ=%d | ANTIGO_OU_DUPLICADO (MAX=%d)", msg.Sequence, prevMax))
		return
	}

	oldRec, hadInLSDB := r.lsdb[msg.OriginIP]
	topoChanged := !hadInLSDB || !maps.Equal(oldRec.Links, msg.Links)

	r.maxSeq[msg.OriginIP] = msg.Sequence
	r.lsdb[msg.OriginIP] = routing.LSARecord{
		OriginIP:   msg.OriginIP,
		Sequence:   msg.Sequence,
		Links:      maps.Clone(msg.Links),
		ReceivedAt: time.Now(),
	}
	r.logEvent("LSA_ACEITO", msg.OriginIP, fmt.Sprintf("SEQ=%d | VIA=%s", msg.Sequence, senderIP))

	// Flood to all other active neighbors except the sender.
	raw, err := protocol.FormatLSA(msg.OriginIP, msg.Sequence, msg.Links)
	if err == nil {
		for ip, other := range r.neighbors {
			if other.Active && !other.Disabled && ip != senderIP {
				_ = r.sendRawLocked(ip, raw)
			}
		}
	}

	// Only re-run Dijkstra if topology changed (periodic renewal without topology change does not trigger Dijkstra).
	if topoChanged {
		r.runDijkstraLocked()
	}
}

func (r *Router) checkTimeoutsLocked() {
	now := time.Now()
	localChanged := false
	topoChanged := false

	for ip, nb := range r.neighbors {
		if nb.Active && now.Sub(nb.LastHello) > r.opts.NeighborTimeout {
			nb.Active = false
			localChanged = true
			topoChanged = true
			r.logEvent("VIZINHO_INATIVO", ip, "TIMEOUT_HELLO (>15s)")
		}
	}

	for origin, rec := range r.lsdb {
		if origin == r.opts.SelfIP {
			continue
		}
		if now.Sub(rec.ReceivedAt) > r.opts.LSAExpiration {
			delete(r.lsdb, origin)
			topoChanged = true
			r.logEvent("LSA_EXPIRADO", origin, fmt.Sprintf("SEQ=%d", rec.Sequence))
		}
	}

	if localChanged {
		r.publishLocalLSALocked(true)
	}
	if topoChanged {
		r.runDijkstraLocked()
	}
}

func (r *Router) runDijkstraLocked() {
	activeSet := make(map[string]bool)
	for ip, nb := range r.neighbors {
		if nb.Active && !nb.Disabled {
			activeSet[ip] = true
		}
	}

	newTable := routing.ComputeDijkstra(r.opts.SelfIP, r.lsdb, activeSet, r.rng)
	changes := routing.DiffRoutes(r.routes, newTable)
	r.routes = newTable
	r.logEvent("DIJKSTRA_EXECUTADO", "", fmt.Sprintf("ROTAS=%d", len(newTable)))

	for _, ch := range changes {
		switch ch.Kind {
		case routing.RouteAdded:
			r.logEvent("ROTA_INCLUIDA", ch.Destination, fmt.Sprintf("CUSTO=%d | PROX_SALTO=%s", ch.NewRoute.TotalCost, ch.NewRoute.NextHop))
		case routing.RouteRemoved:
			r.logEvent("ROTA_REMOVIDA", ch.Destination, fmt.Sprintf("ANTIGO_CUSTO=%d | ANTIGO_SALTO=%s", ch.OldRoute.TotalCost, ch.OldRoute.NextHop))
		case routing.RouteModified:
			r.logEvent("ROTA_MODIFICADA", ch.Destination, fmt.Sprintf("CUSTO %d->%d | PROX_SALTO %s->%s",
				ch.OldRoute.TotalCost, ch.NewRoute.TotalCost, ch.OldRoute.NextHop, ch.NewRoute.NextHop))
		}
	}
}

func (r *Router) handleDATALocked(msg *protocol.DataMessage) {
	if msg.DestIP == r.opts.SelfIP {
		r.logEvent("DATA_RECEBIDO", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | %s", msg.ID, msg.DestIP, msg.Payload))
		return
	}

	route, hasRoute := r.routes[msg.DestIP]
	if !hasRoute {
		r.logEvent("DATA_DESCARTADA", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | MOTIVO=SEM_ROTA", msg.ID, msg.DestIP))
		return
	}
	if msg.TTL <= 0 {
		r.logEvent("DATA_DESCARTADA", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | MOTIVO=TTL_EXPIRADO", msg.ID, msg.DestIP))
		return
	}

	newTTL := msg.TTL - 1
	payload, err := protocol.FormatDATA(msg.OriginIP, msg.DestIP, msg.ID, newTTL, msg.Payload)
	if err != nil {
		r.logEvent("DATA_DESCARTADA", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | MOTIVO=%s", msg.ID, msg.DestIP, err.Error()))
		return
	}
	if err := r.sendRawLocked(route.NextHop, payload); err != nil {
		r.logEvent("DATA_DESCARTADA", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | MOTIVO=%s", msg.ID, msg.DestIP, err.Error()))
		return
	}
	r.logEvent("DATA_ENCAMINHADA", msg.OriginIP, fmt.Sprintf("ID=%d | DESTINO %s | PROX_SALTO=%s | TTL=%d | %s",
		msg.ID, msg.DestIP, route.NextHop, newTTL, msg.Payload))
}

// --- Interactive / Management Operations ---

func (r *Router) SendData(destIP, text string, ttl int) error {
	if !protocol.IsValidIPv4(destIP) {
		return protocol.ErrInvalidIPv4
	}
	if ttl < 0 || ttl > 255 {
		return protocol.ErrInvalidTTL
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	msgID := r.nextMsgID
	r.nextMsgID++

	if destIP == r.opts.SelfIP {
		r.logEvent("DATA_RECEBIDO", r.opts.SelfIP, fmt.Sprintf("ID=%d | DESTINO %s | %s", msgID, destIP, text))
		return nil
	}

	route, ok := r.routes[destIP]
	if !ok {
		r.logEvent("DATA_DESCARTADA", r.opts.SelfIP, fmt.Sprintf("ID=%d | DESTINO %s | MOTIVO=SEM_ROTA", msgID, destIP))
		return fmt.Errorf("sem rota para %s", destIP)
	}

	payload, err := protocol.FormatDATA(r.opts.SelfIP, destIP, msgID, ttl, text)
	if err != nil {
		return err
	}
	if err := r.sendRawLocked(route.NextHop, payload); err != nil {
		return err
	}
	r.logEvent("DATA_ENCAMINHADA", r.opts.SelfIP, fmt.Sprintf("ID=%d | DESTINO %s | PROX_SALTO=%s | TTL=%d | %s",
		msgID, destIP, route.NextHop, ttl, text))
	return nil
}

func (r *Router) DisableLink(ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nb, ok := r.neighbors[ip]
	if !ok {
		return fmt.Errorf("vizinho %s não configurado", ip)
	}
	nb.Disabled = true
	wasActive := nb.Active
	nb.Active = false
	r.logEvent("ENLACE_DESATIVADO", ip, "")
	if wasActive {
		r.publishLocalLSALocked(true)
		r.runDijkstraLocked()
	}
	return nil
}

func (r *Router) EnableLink(ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nb, ok := r.neighbors[ip]
	if !ok {
		return fmt.Errorf("vizinho %s não configurado", ip)
	}
	nb.Disabled = false
	r.logEvent("ENLACE_ATIVADO", ip, "AGUARDANDO_HELLO")
	r.sendHelloLocked()
	return nil
}

func (r *Router) SetLinkCost(ip string, cost int) error {
	if cost <= 0 {
		return protocol.ErrInvalidCost
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	nb, ok := r.neighbors[ip]
	if !ok {
		return fmt.Errorf("vizinho %s não configurado", ip)
	}
	oldCost := nb.Cost
	nb.Cost = cost
	if err := r.saveRoteadoresLocked(); err != nil {
		return err
	}
	r.logEvent("CUSTO_ALTERADO", ip, fmt.Sprintf("%d->%d", oldCost, cost))
	if nb.Active && !nb.Disabled {
		r.publishLocalLSALocked(true)
		r.runDijkstraLocked()
	}
	return nil
}

func (r *Router) AddNeighbor(ip string, cost int, localPort string) error {
	if !protocol.IsValidIPv4(ip) {
		return protocol.ErrInvalidIPv4
	}
	if ip == r.opts.SelfIP {
		return fmt.Errorf("não é possível adicionar o próprio IP como vizinho")
	}
	if cost <= 0 {
		return protocol.ErrInvalidCost
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.neighbors[ip]; exists {
		return fmt.Errorf("vizinho %s já configurado (use 'custo' para alterar)", ip)
	}
	if r.opts.LocalMode && localPort != "" {
		target := "127.0.0.1:" + localPort
		r.peerAddrs[ip] = target
		r.addrToIP[target] = ip
	}
	r.neighbors[ip] = &NeighborState{
		IP:   ip,
		Cost: cost,
	}
	if err := r.saveRoteadoresLocked(); err != nil {
		return err
	}
	r.logEvent("VIZINHO_ADICIONADO", ip, fmt.Sprintf("CUSTO=%d", cost))
	r.sendHelloLocked()
	return nil
}

func (r *Router) RemoveNeighbor(ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nb, exists := r.neighbors[ip]
	if !exists {
		return fmt.Errorf("vizinho %s não encontrado", ip)
	}
	wasActive := nb.Active
	delete(r.neighbors, ip)
	if err := r.saveRoteadoresLocked(); err != nil {
		return err
	}
	r.logEvent("VIZINHO_REMOVIDO", ip, "")
	if wasActive {
		r.publishLocalLSALocked(true)
		r.runDijkstraLocked()
	}
	return nil
}

func (r *Router) saveRoteadoresLocked() error {
	m := make(map[string]int, len(r.neighbors))
	for ip, nb := range r.neighbors {
		m[ip] = nb.Cost
	}
	return SaveRoteadores(r.roteadoresPath, m)
}

// SendTestLSA resends a stored LSA from originIP to neighborIP with either the original sequence ("dup")
// or a lower sequence ("old") to test duplicate/stale LSA rejection.
func (r *Router) SendTestLSA(neighborIP, originIP, mode string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	nb, ok := r.neighbors[neighborIP]
	if !ok {
		return fmt.Errorf("vizinho %s não encontrado", neighborIP)
	}
	if !nb.Active || nb.Disabled {
		return fmt.Errorf("vizinho %s não está ativo", neighborIP)
	}
	rec, ok := r.lsdb[originIP]
	if !ok {
		return fmt.Errorf("nenhum LSA armazenado para origem %s", originIP)
	}

	seq := rec.Sequence
	switch mode {
	case "dup", "duplicado":
		seq = rec.Sequence
	case "old", "antigo":
		if rec.Sequence > 1 {
			seq = rec.Sequence - 1
		} else {
			seq = 1
		}
	default:
		return fmt.Errorf("modo inválido %q (use 'dup' ou 'old')", mode)
	}

	payload, err := protocol.FormatLSA(rec.OriginIP, seq, rec.Links)
	if err != nil {
		return err
	}
	if err := r.sendRawLocked(neighborIP, payload); err != nil {
		return err
	}
	r.logEvent("LSA_TESTE_ENVIADO", neighborIP, fmt.Sprintf("ORIGEM=%s | SEQ=%d | MODO=%s", originIP, seq, mode))
	return nil
}

func (r *Router) SnapshotLSDB() []routing.LSARecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]string, 0, len(r.lsdb))
	for k := range r.lsdb {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res := make([]routing.LSARecord, 0, len(keys))
	for _, k := range keys {
		rec := r.lsdb[k]
		rec.Links = maps.Clone(rec.Links)
		res = append(res, rec)
	}
	return res
}

func (r *Router) SnapshotRoutes() []routing.Route {
	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]string, 0, len(r.routes))
	for k := range r.routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res := make([]routing.Route, 0, len(keys))
	for _, k := range keys {
		res = append(res, r.routes[k])
	}
	return res
}

func (r *Router) SnapshotNeighbors() []NeighborState {
	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]string, 0, len(r.neighbors))
	for k := range r.neighbors {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res := make([]NeighborState, 0, len(keys))
	for _, k := range keys {
		res = append(res, *r.neighbors[k])
	}
	return res
}
