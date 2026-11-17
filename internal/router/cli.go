package router

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func PrintHelp(out io.Writer) {
	fmt.Fprintln(out, "=== Comandos Disponíveis ===")
	fmt.Fprintln(out, "  lsdb                                   - Exibe a Base de Dados de Estado de Enlace (LSDB)")
	fmt.Fprintln(out, "  rotas                                  - Exibe a Tabela de Roteamento")
	fmt.Fprintln(out, "  vizinhos                               - Exibe os vizinhos configurados e seus estados")
	fmt.Fprintln(out, "  desativar <ip>                         - Desativa enlace lógico com o vizinho")
	fmt.Fprintln(out, "  ativar <ip>                            - Ativa enlace lógico com o vizinho")
	fmt.Fprintln(out, "  custo <ip> <custo>                     - Altera o custo do enlace e atualiza roteadores.txt")
	fmt.Fprintln(out, "  add <ip> <custo> [porta]               - Adiciona vizinho em roteadores.txt (porta opcional no modo local)")
	fmt.Fprintln(out, "  rem <ip>                               - Remove vizinho de roteadores.txt")
	fmt.Fprintln(out, "  data <dest_ip> [ttl=N] <mensagem>      - Envia mensagem DATA (TTL padrão 16, 0..255)")
	fmt.Fprintln(out, "  testlsa <vizinho> <origem> <dup|old>   - Reenvia LSA armazenado (duplicado ou antigo) para teste")
	fmt.Fprintln(out, "  ajuda / help                           - Mostra esta ajuda")
	fmt.Fprintln(out, "  sair / exit                            - Encerra o roteador")
}

func RunCLI(r *Router, in io.Reader, out io.Writer) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	PrintHelp(out)
	scanner := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "\nroteador> ")
		if !scanner.Scan() {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		cmd := strings.ToLower(fields[0])

		switch cmd {
		case "help", "ajuda":
			PrintHelp(out)

		case "sair", "exit", "quit":
			return

		case "lsdb":
			printLSDB(r, out)

		case "rotas", "routes":
			printRoutes(r, out)

		case "vizinhos", "neighbors":
			printNeighbors(r, out)

		case "desativar", "disable":
			if len(fields) != 2 {
				fmt.Fprintln(out, "Uso: desativar <ip>")
				continue
			}
			if err := r.DisableLink(fields[1]); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		case "ativar", "enable":
			if len(fields) != 2 {
				fmt.Fprintln(out, "Uso: ativar <ip>")
				continue
			}
			if err := r.EnableLink(fields[1]); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		case "custo", "cost":
			if len(fields) != 3 {
				fmt.Fprintln(out, "Uso: custo <ip> <novo_custo>")
				continue
			}
			c, err := strconv.Atoi(fields[2])
			if err != nil {
				fmt.Fprintln(out, "Erro: custo deve ser inteiro positivo")
				continue
			}
			if err := r.SetLinkCost(fields[1], c); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		case "add":
			if len(fields) < 3 || len(fields) > 4 {
				fmt.Fprintln(out, "Uso: add <ip> <custo> [porta_local]")
				continue
			}
			c, err := strconv.Atoi(fields[2])
			if err != nil {
				fmt.Fprintln(out, "Erro: custo deve ser inteiro positivo")
				continue
			}
			localPort := ""
			if len(fields) == 4 {
				localPort = fields[3]
			}
			if err := r.AddNeighbor(fields[1], c, localPort); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		case "rem":
			if len(fields) != 2 {
				fmt.Fprintln(out, "Uso: rem <ip>")
				continue
			}
			if err := r.RemoveNeighbor(fields[1]); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		case "data":
			handleDataCLI(r, line, out)

		case "testlsa":
			if len(fields) != 4 {
				fmt.Fprintln(out, "Uso: testlsa <vizinho_ip> <origem_ip> <dup|old>")
				continue
			}
			if err := r.SendTestLSA(fields[1], fields[2], strings.ToLower(fields[3])); err != nil {
				fmt.Fprintf(out, "Erro: %v\n", err)
			}

		default:
			fmt.Fprintf(out, "Comando desconhecido: %s (digite 'ajuda')\n", fields[0])
		}
	}
}

func handleDataCLI(r *Router, rawLine string, out io.Writer) {
	// Syntax: data <dest_ip> [ttl=N] <mensagem...>
	rest := strings.TrimSpace(rawLine[len("data"):])
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		fmt.Fprintln(out, "Uso: data <dest_ip> [ttl=N] <mensagem>")
		return
	}
	destIP := strings.TrimSpace(parts[0])
	msgPart := strings.TrimSpace(parts[1])
	ttl := 16

	if strings.HasPrefix(strings.ToLower(msgPart), "ttl=") {
		sub := strings.SplitN(msgPart, " ", 2)
		if len(sub) < 2 {
			fmt.Fprintln(out, "Uso: data <dest_ip> [ttl=N] <mensagem>")
			return
		}
		valStr := sub[0][4:]
		parsedTTL, err := strconv.Atoi(valStr)
		if err != nil {
			fmt.Fprintln(out, "Erro: TTL inválido (deve ser 0..255)")
			return
		}
		ttl = parsedTTL
		msgPart = strings.TrimSpace(sub[1])
	}

	if msgPart == "" {
		fmt.Fprintln(out, "Erro: mensagem vazia")
		return
	}
	if err := r.SendData(destIP, msgPart, ttl); err != nil {
		fmt.Fprintf(out, "Erro ao enviar DATA: %v\n", err)
	}
}

func printLSDB(r *Router, out io.Writer) {
	recs := r.SnapshotLSDB()
	fmt.Fprintf(out, "\n--- LSDB (%d registros) ---\n", len(recs))
	for _, rec := range recs {
		nbrKeys := make([]string, 0, len(rec.Links))
		for k := range rec.Links {
			nbrKeys = append(nbrKeys, k)
		}
		sort.Strings(nbrKeys)
		linksStr := make([]string, 0, len(nbrKeys))
		for _, k := range nbrKeys {
			linksStr = append(linksStr, fmt.Sprintf("%s:%d", k, rec.Links[k]))
		}
		fmt.Fprintf(out, "  Origem=%-15s | Seq=%-4d | Recebido=%s | Enlaces=[%s]\n",
			rec.OriginIP, rec.Sequence, rec.ReceivedAt.Format("15:04:05"), strings.Join(linksStr, ", "))
	}
}

func printRoutes(r *Router, out io.Writer) {
	routes := r.SnapshotRoutes()
	fmt.Fprintf(out, "\n--- Tabela de Roteamento (%d rotas) ---\n", len(routes))
	fmt.Fprintf(out, "  %-15s | %-11s | %-15s\n", "Destino", "Custo total", "Próximo salto")
	fmt.Fprintln(out, "  ----------------+-------------+----------------")
	for _, rt := range routes {
		fmt.Fprintf(out, "  %-15s | %-11d | %-15s\n", rt.Destination, rt.TotalCost, rt.NextHop)
	}
}

func printNeighbors(r *Router, out io.Writer) {
	nbrs := r.SnapshotNeighbors()
	fmt.Fprintf(out, "\n--- Vizinhos Configurados (%d) ---\n", len(nbrs))
	for _, nb := range nbrs {
		status := "INATIVO"
		if nb.Disabled {
			status = "DESATIVADO"
		} else if nb.Active {
			status = "ATIVO"
		}
		last := "-"
		if !nb.LastHello.IsZero() {
			last = nb.LastHello.Format("15:04:05")
		}
		fmt.Fprintf(out, "  %-15s | Custo=%-4d | Estado=%-10s | UltimoHELLO=%s\n",
			nb.IP, nb.Cost, status, last)
	}
}
