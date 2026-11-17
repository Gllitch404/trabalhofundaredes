package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"linkstate/internal/protocol"
	"linkstate/internal/router"
)

func detectLocalIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ip4 := ipNet.IP.To4(); ip4 != nil {
					return ip4.String()
				}
			}
		}
	}
	return ""
}

func main() {
	ipFlag := flag.String("ip", "", "IPv4 identificador do roteador (obrigatório no modo -local; no modo padrão tenta detectar automaticamente)")
	portFlag := flag.Int("port", protocol.DefaultPort, "Porta UDP de escuta (padrão 6000)")
	bindFlag := flag.String("bind", "", "IP de bind do socket UDP (padrão 0.0.0.0 no modo normal, 127.0.0.1 no modo -local)")
	localFlag := flag.Bool("local", false, "Ativa modo de teste local em loopback (127.0.0.1) com múltiplas portas")
	dirFlag := flag.String("dir", ".", "Diretório de trabalho com roteadores.txt, seq.txt e peers.txt")
	peersFlag := flag.String("peers", "", "Mapeamento local IP=PORTA separado por vírgula (ex: 10.0.0.1=6000,10.0.0.2=6001)")
	flag.Parse()

	selfIP := *ipFlag
	if selfIP == "" {
		if *localFlag {
			fmt.Fprintln(os.Stderr, "Erro: no modo -local informe -ip (ex: -ip 10.0.0.1)")
			os.Exit(1)
		}
		selfIP = detectLocalIPv4()
		if selfIP == "" {
			fmt.Fprintln(os.Stderr, "Erro: não foi possível detectar IPv4 local; informe -ip <IPv4>")
			os.Exit(1)
		}
	}

	peerMap, err := router.ParsePeersFlag(*peersFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro em -peers: %v\n", err)
		os.Exit(1)
	}

	opts := router.DefaultOptions()
	opts.SelfIP = selfIP
	opts.Port = *portFlag
	opts.BindIP = *bindFlag
	opts.LocalMode = *localFlag
	opts.WorkDir = *dirFlag
	opts.PeerAddrs = peerMap

	r, err := router.New(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao inicializar roteador: %v\n", err)
		os.Exit(1)
	}
	if err := r.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao iniciar roteador: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nEncerrando roteador...")
		r.Stop()
		os.Exit(0)
	}()

	router.RunCLI(r, os.Stdin, os.Stdout)
	r.Stop()
}
