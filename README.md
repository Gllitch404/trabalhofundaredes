# Roteamento Link State (OSPF-like) em Go

Implementação do Trabalho Final de Fundamentos de Redes de Computadores (PUCRS) em Go, utilizando sockets UDP, descoberta de vizinhos via `HELLO`, banco de dados `LSDB`, flooding de `LSA`, algoritmo de Dijkstra próprio e encaminhamento de mensagens `DATA`.

## Estrutura do Projeto

- `main.go` — Ponto de entrada e flags (`-ip`, `-port`, `-local`, `-dir`, `-peers`, `-bind`).
- `internal/protocol/messages.go` — Parsing, formatação e validação (`HELLO`, `LSA`, `DATA`, UTF-8, máximo 1200 bytes, sem quebra de linha ao final).
- `internal/routing/dijkstra.go` — Construção do grafo direcionado (exigindo reconhecimento bidirecional), algoritmo de Dijkstra, desempate aleatório e detecção de rotas incluídas/removidas/modificadas.
- `internal/router/config.go` — Leitura e persistência de `roteadores.txt`, `seq.txt` e `peers.txt`.
- `internal/router/router.go` — Motor principal do roteador (UDP, temporizadores 5s/10s/15s/30s, LSDB, flooding, expiração e encaminhamento `DATA`).
- `internal/router/cli.go` — Interface interativa de linha de comando.
- `scripts/run_spec.ps1` — Execução no **Modo Especificação** (porta UDP 6000, IPs reais).
- `scripts/run_local_demo.ps1` — Execução no **Modo Local Loopback** (`127.0.0.1` nas portas `6000`, `6001`, `6002`, `6003`).

---

## 1. Modo Especificação (4+ computadores / interoperabilidade)

Neste modo, cada roteador escuta na **porta UDP 6000** (`0.0.0.0:6000`) e usa os endereços IPv4 reais configurados em `roteadores.txt`.

1. Crie `roteadores.txt` na pasta do roteador (veja `roteadores.txt.example`):
   ```text
   192.168.1.2;10
   192.168.1.3;5
   192.168.1.4;20
   ```
2. Execute:
   ```powershell
   .\scripts\run_spec.ps1 -IP 192.168.1.1
   ```
   *(Ou diretamente: `go run . -ip 192.168.1.1 -port 6000`)*

---

## 2. Modo Teste Local (Loopback `127.0.0.1` com portas `6000`, `6001`, `6002`, `6003`)

Para testar toda a rede em uma única máquina sem conflito de porta `6000`, use a flag `-local`.  
Cada roteador escuta em `127.0.0.1:<porta>` e mantém seu identificador IPv4 lógico nos pacotes (`HELLO`, `LSA`, `DATA`), mapeado via `peers.txt` ou `-peers`.

Para subir automaticamente 4 roteadores em janelas separadas:
```powershell
.\scripts\run_local_demo.ps1
```
Ou para apenas gerar os arquivos em `sim/r1`..`sim/r4` e rodar manualmente em cada terminal:
```powershell
.\scripts\run_local_demo.ps1 -SetupOnly
```

Topologia criada pelo script local:
- **R1** (`10.0.0.1` em `127.0.0.1:6000`) ↔ **R2** (`10.0.0.2` em `127.0.0.1:6001`) custo `10`
- **R1** (`10.0.0.1` em `127.0.0.1:6000`) ↔ **R3** (`10.0.0.3` em `127.0.0.1:6002`) custo `5`
- **R2** (`10.0.0.2` em `127.0.0.1:6001`) ↔ **R4** (`10.0.0.4` em `127.0.0.1:6003`) custo `10`
- **R3** (`10.0.0.3` em `127.0.0.1:6002`) ↔ **R4** (`10.0.0.4` em `127.0.0.1:6003`) custo `50`

---

## Comandos Interativos no Terminal

- `lsdb` — Exibe todos os LSAs válidos na LSDB.
- `rotas` — Exibe a tabela de roteamento atual (`Destino | Custo total | Próximo salto`).
- `vizinhos` — Lista vizinhos configurados, custos e estado (`ATIVO`, `INATIVO`, `DESATIVADO`).
- `desativar <ip>` — Desativa enlace lógico (para de enviar HELLO e descarta HELLOs/LSAs do vizinho).
- `ativar <ip>` — Reativa enlace lógico (volta a ser ativo após confirmação por HELLO válido).
- `custo <ip> <novo_custo>` — Altera custo do enlace, atualiza `roteadores.txt` e divulga novo LSA.
- `add <ip> <custo> [porta_local]` — Adiciona vizinho em `roteadores.txt` (informe `porta_local` no modo `-local` para novos roteadores).
- `rem <ip>` — Remove vizinho de `roteadores.txt`.
- `data <dest_ip> [ttl=N] <mensagem>` — Envia mensagem `DATA` (TTL padrão `16`, intervalo `0..255`). Ex.: `data 10.0.0.4 ttl=2 Olá rede!`
- `testlsa <vizinho_ip> <origem_ip> <dup|old>` — Reenvia LSA armazenado com sequência original (`dup`) ou menor (`old`) para testar descarte.
- `sair` — Encerra o roteador.

---

## Testes Automatizados e Wireshark

- **Testes unitários e de integração**:
  ```powershell
  go test -v ./...
  ```
  *(Nota: no Windows com Controle Inteligente de Aplicativos ativo, executáveis recém-compilados localmente podem ser bloqueados pelo sistema operacional ao tentar executar sockets; nesse caso, execute em Linux/WSL ou libere o binário/desative o bloqueio na máquina de teste.)*
- **Wireshark**:
  - Modo Especificação: filtro `udp.port == 6000`
  - Modo Local Loopback (interface *Adapter for loopback traffic capture*): filtro `udp.port >= 6000 && udp.port <= 6003`
