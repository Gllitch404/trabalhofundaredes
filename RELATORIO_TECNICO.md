# Relatório Técnico — Protocolo de Roteamento Link State (OSPF-like)

**Disciplina:** Fundamentos de Redes de Computadores — Escola Politécnica (PUCRS)  
**Linguagem:** Go (`go1.21+`) com sockets UDP (`net.UDPConn`)

---

## 1. Arquitetura da Solução

A aplicação foi estruturada de forma modular em Go, separando camada de protocolo, algoritmo de roteamento, motor do roteador e interface interativa:

- **`main.go`**: ponto de entrada responsável pelo parsing de parâmetros (`-ip`, `-port`, `-local`, `-dir`, `-peers`, `-bind`) e tratamento de sinais de encerramento.
- **`internal/protocol/messages.go`**: codificação, decodificação e validação das mensagens `HELLO` (`@`), `LSA` (`#`) e `DATA` (`!`), garantindo codificação UTF-8, endereços IPv4 válidos, limite máximo de `1200` bytes e ausência de quebra de linha ao final.
- **`internal/routing/dijkstra.go`**: construção do grafo direcionado a partir da LSDB, implementação própria do algoritmo de Dijkstra (sem bibliotecas externas de caminhos mínimos) e detecção de alterações na tabela de roteamento (`DiffRoutes`).
- **`internal/router/config.go`**: leitura e gravação de `roteadores.txt`, persistência de número de sequência em `seq.txt` e mapeamento opcional de portas locais (`peers.txt`) para testes em loopback.
- **`internal/router/router.go`**: gerenciamento do socket UDP, loops concorrentes de recepção e temporizadores, manutenção de vizinhos, LSDB, flooding, expiração de anúncios e encaminhamento de mensagens `DATA`.
- **`internal/router/cli.go`**: interface de linha de comando para consulta (`lsdb`, `rotas`, `vizinhos`) e manipulação dinâmica da topologia sem reiniciar o processo (`desativar`, `ativar`, `custo`, `add`, `rem`, `data`, `testlsa`).

### Modos de Execução
- **Modo Especificação (padrão)**: escuta em `0.0.0.0:6000` e utiliza os endereços IPv4 reais configurados em `roteadores.txt`, garantindo interoperabilidade entre computadores distintos na porta UDP `6000`.
- **Modo Local Loopback (`-local`)**: escuta em `127.0.0.1:<porta>` (`6000`–`6003`) mantendo os identificadores IPv4 lógicos (`10.0.0.1`–`10.0.0.4`) nos datagramas, permitindo testar múltiplos roteadores em uma única máquina.

---

## 2. Estruturas de Dados e Temporizadores

1. **Tabela de Vizinhos (`map[string]*NeighborState`)**:
   - Armazena para cada vizinho configurado em `roteadores.txt`: `IP`, `Cost` (inteiro positivo), `Disabled` (estado lógico do enlace), `Active` (vizinhança mútua confirmada via `HELLO`) e `LastHello` (instante do último `HELLO` válido).
2. **LSDB (`map[string]routing.LSARecord`) e Controle de Sequência (`maxSeq map[string]int`)**:
   - Cada registro da LSDB guarda `OriginIP`, `Sequence`, `Links` (`map[string]int`) e `ReceivedAt`.
   - O mapa `maxSeq` mantém, durante toda a execução, o maior número de sequência já aceito de cada origem, mesmo após expiração do LSA na LSDB, impedindo a aceitação tardia de anúncios antigos ou duplicados.
   - O número de sequência local é persistido em `seq.txt` antes do envio de cada novo LSA.
3. **Tabela de Roteamento (`map[string]routing.Route`)**:
   - Mapeia cada `Destination` (excluindo o próprio roteador) para `TotalCost` e `NextHop` (sempre um vizinho ativo).
4. **Temporizadores (`timerLoop` em `router.go`)**:
   - **`HelloInterval` = 5 s**: envio periódico de `HELLO` a todos os vizinhos configurados e não desativados.
   - **`LSAInterval` = 10 s**: renovação periódica do LSA local.
   - **`NeighborTimeout` = 15 s**: marca vizinho como inativo caso fique mais de 15 s sem enviar `HELLO` válido, atualizando e propagando o LSA local.
   - **`LSAExpiration` = 30 s**: remove LSAs remotos da LSDB após 30 s sem versão nova aceita (duplicados/antigos não renovam o prazo), disparando recálculo de rotas.

---

## 3. Implementação do Algoritmo de Dijkstra

O cálculo de menor caminho em `internal/routing/dijkstra.go` (`ComputeDijkstra`) segue as regras da especificação:

1. **Construção do Grafo (`BuildGraph`)**: uma aresta direcionada $u \rightarrow v$ com custo $c(u, v)$ só é adicionada quando **ambas as extremidades** ($u$ e $v$) possuem LSAs válidos na LSDB reconhecendo o enlace. Para arestas partindo do próprio roteador, exige-se ainda que $v$ seja um vizinho ativo.
2. **Cálculo de Menor Custo**: inicia com `dist[selfIP] = 0` e infinito para os demais vértices; a cada iteração seleciona o vértice não visitado de menor distância e relaxa suas arestas de saída.
3. **Empates de Custo**: para cada destino, armazena-se a lista `nextHops[v]` com todos os primeiros saltos candidatos que atingem o custo mínimo; ao final, um próximo salto é escolhido **aleatoriamente** (`rng.Intn`).
4. **Otimização e Destaque de Mudanças**: renovações periódicas de LSA sem alteração de enlaces ou custos não disparam o Dijkstra. Quando executado, `DiffRoutes` compara a tabela anterior com a nova e destaca rotas `INCLUIDA`, `REMOVIDA` e `MODIFICADA`.

---

## 4. Topologia Empregada nos Testes

Os testes foram realizados com 4 roteadores conectados conforme a topologia assimétrica/com caminhos alternativos abaixo:

- **R1 (`10.0.0.1`, porta `6000`)** $\leftrightarrow$ **R2 (`10.0.0.2`, porta `6001`)**: custo `10`
- **R1 (`10.0.0.1`, porta `6000`)** $\leftrightarrow$ **R3 (`10.0.0.3`, porta `6002`)**: custo `5`
- **R2 (`10.0.0.2`, porta `6001`)** $\leftrightarrow$ **R4 (`10.0.0.4`, porta `6003`)**: custo `10`
- **R3 (`10.0.0.3`, porta `6002`)** $\leftrightarrow$ **R4 (`10.0.0.4`, porta `6003`)**: custo `50`

Caminho ótimo inicial de **R1 $\rightarrow$ R4**: `R1 -> R2 -> R4` com custo total **`20`** (alternativa via `R3` tem custo `5 + 50 = 55`).

---

## 5. Cenários de Teste Documentados (A a H)

### Cenário A — Convergência inicial da rede
- **Comportamento esperado:** Os roteadores trocam `HELLO`, estabelecem vizinhanças mútuas, sincronizam suas LSDBs, propagam os LSAs e convergem via Dijkstra para as rotas de menor custo.
- **Resultado observado:** R1 estabeleceu vizinhança com R3 (custo `5`) e R2 (custo `10`), recebeu o LSA de R4 (`SEQ=4`) via R2 e convergiu para todas as 3 rotas remotas.
- **LSAs relevantes / Alterações:**
  - LSA de R4 (`#10.0.0.4;4*10.0.0.2;10*10.0.0.3;50`) aceito em R1; tabela de R1 passou a conter `10.0.0.3` (custo `5`, salto `10.0.0.3`), `10.0.0.2` (custo `10`, salto `10.0.0.2`) e `10.0.0.4` (custo `20`, salto `10.0.0.2`).
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [11:34:32.148] VIZINHO_ATIVO | 10.0.0.3 | CUSTO=5
  [11:34:32.232] VIZINHO_ATIVO | 10.0.0.2 | CUSTO=10
  [11:34:37.004] LSA_ACEITO | 10.0.0.3 | SEQ=2 | VIA=10.0.0.3
  [11:34:37.004] ROTA_INCLUIDA | 10.0.0.3 | CUSTO=5 | PROX_SALTO=10.0.0.3
  [11:34:37.005] LSA_ACEITO | 10.0.0.2 | SEQ=2 | VIA=10.0.0.2
  [11:34:37.005] ROTA_INCLUIDA | 10.0.0.2 | CUSTO=10 | PROX_SALTO=10.0.0.2
  [11:34:42.012] LSA_ACEITO | 10.0.0.4 | SEQ=4 | VIA=10.0.0.2
  [11:34:42.012] DIJKSTRA_EXECUTADO | ROTAS=3
  [11:34:42.012] ROTA_INCLUIDA | 10.0.0.4 | CUSTO=20 | PROX_SALTO=10.0.0.2
  ```

### Cenário B — Entrada de um novo roteador
- **Comportamento esperado:** Ao ingressar um novo roteador na topologia, seus vizinhos sincronizam os LSAs válidos da LSDB e propagam o novo LSA, fazendo os demais roteadores incluírem a nova rota sem reiniciar os processos existentes.
- **Resultado observado:** Quando `10.0.0.4` entrou na rede e estabeleceu enlace com R2 e R3, o LSA `SEQ=4` de `10.0.0.4` foi propagado até R1, que o inseriu na LSDB e incluiu a rota na tabela.
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [11:34:42.012] LSA_ACEITO | 10.0.0.4 | SEQ=4 | VIA=10.0.0.2
  [11:34:42.012] DIJKSTRA_EXECUTADO | ROTAS=3
  [11:34:42.012] ROTA_INCLUIDA | 10.0.0.4 | CUSTO=20 | PROX_SALTO=10.0.0.2
  ```

### Cenário C — Falha de um enlace
- **Comportamento esperado:** Ao desativar logicamente o enlace `R1 <-> R2` (`desativar 10.0.0.2`), R1 para de enviar `HELLO` a R2, descarta `HELLO`s vindos dele, atualiza seu LSA (`SEQ=196`) removendo R2 e recalcula o menor caminho via R3.
- **Resultado observado:** R1 desviou imediatamente as rotas de `10.0.0.2` (custo `10 -> 65`) e `10.0.0.4` (custo `20 -> 55`) para o próximo salto `10.0.0.3`.
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [00:03:58.433] ENLACE_DESATIVADO | 10.0.0.2
  [00:03:58.433] LSA_ENVIADO | 10.0.0.1 | SEQ=196
  [00:03:58.433] DIJKSTRA_EXECUTADO | ROTAS=3
  [00:03:58.433] ROTA_MODIFICADA | 10.0.0.2 | CUSTO 10->65 | PROX_SALTO 10.0.0.2->10.0.0.3
  [00:03:58.433] ROTA_MODIFICADA | 10.0.0.4 | CUSTO 20->55 | PROX_SALTO 10.0.0.2->10.0.0.3
  [00:03:59.017] HELLO_DESCARTADO | 10.0.0.2 | ENLACE_DESATIVADO
  ```

### Cenário D — Falha de um roteador
- **Comportamento esperado:** Ao encerrar um roteador, seus vizinhos detectam ausência de `HELLO` após 15 s (`VIZINHO_INATIVO`), atualizam seus LSAs e recalculam as rotas; após 30 s sem renovação, o LSA do roteador caído expira da LSDB (`LSA_EXPIRADO`).
- **Resultado observado:** Com o encerramento do roteador R3 (`10.0.0.3`), R1 e R4 detectaram timeout após 15 s, removeram a rota para `10.0.0.3` e, aos 30 s, expiraram o LSA `SEQ=177` de `10.0.0.3`. Em outro teste de queda de R1, R2 desviou sua rota para `10.0.0.3` (`15 -> 60`) via `10.0.0.4`.
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [23:47:20.200] VIZINHO_INATIVO | 10.0.0.3 | TIMEOUT_HELLO (>15s)
  [23:47:20.200] LSA_ENVIADO | 10.0.0.1 | SEQ=182
  [23:47:20.201] DIJKSTRA_EXECUTADO | ROTAS=2
  [23:47:20.201] ROTA_REMOVIDA | 10.0.0.3 | ANTIGO_CUSTO=5 | ANTIGO_SALTO=10.0.0.3
  [23:47:30.200] LSA_EXPIRADO | 10.0.0.3 | SEQ=177
  [23:47:30.200] DIJKSTRA_EXECUTADO | ROTAS=2
  ```

### Cenário E — Recuperação após falha
- **Comportamento esperado:** Ao reativar o enlace (`ativar 10.0.0.2`), o envio de `HELLO` é retomado; após confirmação mútua, a LSDB é sincronizada e o Dijkstra restaura a rota ótima via R2.
- **Resultado observado:** R1 confirmou o `HELLO` de R2, sincronizou os 4 LSAs da LSDB e restaurou as rotas `10.0.0.2` (`65 -> 10`) e `10.0.0.4` (`55 -> 20`).
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [00:04:12.914] ENLACE_ATIVADO | 10.0.0.2 | AGUARDANDO_HELLO
  [00:04:14.017] VIZINHO_ATIVO | 10.0.0.2 | CUSTO=10
  [00:04:14.018] LSDB_SINCRONIZADA | 10.0.0.2 | LSAS=4
  [00:04:14.018] LSA_ENVIADO | 10.0.0.1 | SEQ=199
  [00:04:14.018] DIJKSTRA_EXECUTADO | ROTAS=3
  [00:04:14.018] ROTA_MODIFICADA | 10.0.0.2 | CUSTO 65->10 | PROX_SALTO 10.0.0.3->10.0.0.2
  [00:04:14.018] ROTA_MODIFICADA | 10.0.0.4 | CUSTO 55->20 | PROX_SALTO 10.0.0.3->10.0.0.2
  ```

### Cenário F — Recebimento de LSAs antigos e duplicados
- **Comportamento esperado:** LSAs recebidos com número de sequência menor ou igual ao maior já aceito (`maxSeq`) devem ser descartados sem flooding, sem renovação de validade e sem execução do Dijkstra.
- **Resultado observado:** R1 enviou a R2 o LSA armazenado de `10.0.0.4` com sequência antiga (`SEQ=202`, `MODO=old`) e depois duplicada (`SEQ=204`, `MODO=dup`); R2 descartou ambos imediatamente.
- **Trechos de log (`sim/r1/router.log` e `sim/r2/router.log`):**
  ```text
  [00:05:01.665] (R1) LSA_TESTE_ENVIADO | 10.0.0.2 | ORIGEM=10.0.0.4 | SEQ=202 | MODO=old
  [00:05:01.665] (R2) LSA_DESCARTADO | 10.0.0.4 | SEQ=202 | ANTIGO_OU_DUPLICADO (MAX=203)
  [00:05:12.892] (R1) LSA_TESTE_ENVIADO | 10.0.0.2 | ORIGEM=10.0.0.4 | SEQ=204 | MODO=dup
  [00:05:12.892] (R2) LSA_DESCARTADO | 10.0.0.4 | SEQ=204 | ANTIGO_OU_DUPLICADO (MAX=204)
  ```

### Cenário G — Alteração do custo de um enlace
- **Comportamento esperado:** Alterar o custo do enlace `R1 -> R2` de `10` para `100` (`custo 10.0.0.2 100`) atualiza `roteadores.txt`, gera novo LSA (`SEQ=201`) e faz o Dijkstra escolher o caminho alternativo por R3.
- **Resultado observado:** R1 alterou a rota para `10.0.0.4` de custo `20` (via `10.0.0.2`) para custo `55` (via `10.0.0.3`). Ao restaurar o custo para `10`, a rota voltou para `10.0.0.2`.
- **Trecho de log (`sim/r1/router.log`):**
  ```text
  [00:04:25.644] CUSTO_ALTERADO | 10.0.0.2 | 10->100
  [00:04:25.645] LSA_ENVIADO | 10.0.0.1 | SEQ=201
  [00:04:25.645] DIJKSTRA_EXECUTADO | ROTAS=3
  [00:04:25.645] ROTA_MODIFICADA | 10.0.0.2 | CUSTO 10->65 | PROX_SALTO 10.0.0.2->10.0.0.3
  [00:04:25.645] ROTA_MODIFICADA | 10.0.0.4 | CUSTO 20->55 | PROX_SALTO 10.0.0.2->10.0.0.3
  ```

### Cenário H — Encaminhamento de DATA com TTL insuficiente
- **Comportamento esperado:** Um datagrama `DATA` enviado com `TTL=0` para um destino a mais de 1 salto deve ser encaminhado pela origem sem decremento e descartado pelo roteador intermediário com registro do motivo (`TTL_EXPIRADO`).
- **Resultado observado:** R3 enviou a mensagem `ID=1002` destinada a `10.0.0.4` com `TTL=0` via `10.0.0.1` (R1); ao recebê-la e constatar que não era o destino e `TTL == 0`, R1 descartou a mensagem registrando `MOTIVO=TTL_EXPIRADO`.
- **Trechos de log (`sim/r3/router.log` e `sim/r1/router.log`):**
  ```text
  [23:46:55.424] (R3) DATA_ENCAMINHADA | 10.0.0.3 | ID=1002 | DESTINO 10.0.0.4 | PROX_SALTO=10.0.0.1 | TTL=0 | Teste TTL insuficiente
  [23:46:55.424] (R1) DATA_DESCARTADA | 10.0.0.3 | ID=1002 | DESTINO 10.0.0.4 | MOTIVO=TTL_EXPIRADO
  ```

---

## 6. Capturas no Wireshark

As capturas de tráfego acompanham a entrega nos arquivos `wireshark.pcapng` e `req.pcapng`, filtradas pelas portas UDP da aplicação:

1. **Mensagem `HELLO` (Frame 34)** — IPv4 `127.0.0.1 -> 127.0.0.1`, UDP `Src Port: 6003 -> Dst Port: 6002`, `27 bytes`: `@10.0.0.4*10.0.0.2*10.0.0.3`  
   *(Inserir aqui o Print 1 do Wireshark)*
2. **Mensagem `LSA` (Frame 35)** — IPv4 `127.0.0.1 -> 127.0.0.1`, UDP `Src Port: 6002 -> Dst Port: 6003`, `25 bytes`: `#10.0.0.3;174*10.0.0.4;50`  
   *(Inserir aqui o Print 2 do Wireshark)*
3. **Mensagem `DATA` (Frame 192)** — IPv4 `127.0.0.1 -> 127.0.0.1`, UDP `Src Port: 6002 -> Dst Port: 6000`, `48 bytes`: `!10.0.0.3;10.0.0.4;1002;0;Teste TTL insuficiente`  
   *(Inserir aqui o Print 3 do Wireshark)*
