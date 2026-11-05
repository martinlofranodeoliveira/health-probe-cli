# Health Probe CLI

Verificador HTTP de execução única da **Lofrano Tecnologia**: [site oficial](https://lofranotecnologia.com.br). Repositório previsto: [martinlofranodeoliveira/health-probe-cli](https://github.com/martinlofranodeoliveira/health-probe-cli).

Envia um GET para uma URL explícita, verifica o status esperado e o limite do corpo e emite um relatório JSON sanitizado. Um prazo total inclui conexão, cabeçalhos e leitura do corpo. **Não é** um serviço de monitoramento contínuo, teste de carga ou avaliação de prontidão para produção.

Código Go original, apenas biblioteca padrão. Sem dependência de modelos ou serviços de API, telemetria, credenciais, estado persistente ou chamadas de rede alheias à sondagem. As fixtures são sintéticas.

## Instalação, testes e demonstração

Requer Go 1.27 ou posterior. No diretório do código-fonte:

```sh
go test ./...
go build -o health-probe ./cmd/health-probe
go install ./cmd/health-probe
go run ./cmd/health-probe demo
```

`go install` instala em `GOBIN` ou no diretório `bin` do workspace Go quando `GOBIN` não está definido. O build local cria `./health-probe`. Não há módulos de terceiros para baixar; para uso totalmente offline, o toolchain correspondente precisa estar disponível localmente.

Verificações adicionais:

```sh
go vet ./...
go test -race ./...
```

Os testes iniciam apenas servidores de loopback. Exercitam a biblioteca pública e compilam e executam a CLI real, incluindo códigos de saída, rejeição TLS, argumentos inválidos e sanitização.

## CLI

Para um endpoint que você já mantém localmente:

```sh
./health-probe --expect 200,204 --timeout-ms 1500 --max-bytes 4096 http://127.0.0.1:8080/health
./health-probe --help
```

Uma sondagem normal não descobre nem inicia endpoints. `--help` contém um exemplo local. As opções podem vir antes ou depois da URL, nas formas `--nome VALOR` ou `--nome=VALOR`, uma vez cada. Não há opções curtas, atalhos posicionais, arquivos de configuração, opções de ambiente ou delimitador `--`. `--help` e `demo` devem ser usados isoladamente.

| Opção | Padrão | Intervalo aceito |
| --- | --- | --- |
| `--expect` | `200` | 1–32 códigos distintos de três dígitos, separados por vírgula, em 100–599 |
| `--timeout-ms` | `2000` | 1–60000 milissegundos de prazo total |
| `--max-bytes` | `65536` | 0–1048576 bytes de corpo |

Inteiros usam dígitos decimais canônicos: sem sinais, espaços, zeros à esquerda exceto `0`, ou casas decimais. O limite zero permite apenas corpo vazio. A URL tem limite de 8192 bytes UTF-8 e exige `http://` ou `https://` em minúsculas, hostname e porta opcional em 1–65535. Credenciais, fragmentos (inclusive `#` vazio), espaços, caracteres de controle, escapes inválidos e identificadores de zona IPv6 são rejeitados. Consulte o [contrato completo](docs/contract.md), em inglês.

### Relatório e saídas

Exemplo ilustrativo de uma resposta sintética 200 com corpo de dois bytes:

```json
{"healthy":true,"status_code":200,"duration_ms":0,"bytes_read":2,"reason":"ok"}
```

`duration_ms` é o tempo medido em milissegundos inteiros truncados, não um benchmark; varia e pode ser zero. Sem cabeçalhos de resposta disponíveis, `status_code` é `null`. Corpo, cabeçalhos, URL, valores de query e erros brutos de transporte nunca aparecem no relatório ou nos diagnósticos. Falhas normais da sondagem não escrevem diagnóstico em stderr: consulte `reason` no JSON.

Motivos estáveis: `ok`, `status_mismatch`, `body_limit`, `timeout`, `canceled`, `request_error`, `read_error`, `unsupported_upgrade`. DNS, conexão, verificação TLS e cabeçalhos rejeitados ou excessivos usam `request_error`. HTTP `101 Switching Protocols` retorna `unsupported_upgrade`, com status `101` e zero bytes de corpo, mesmo com `--expect 101`; a conexão de protocolo atualizado é fechada imediatamente. A leitura observa no máximo `max-bytes + 1`; `bytes_read` não é Content-Length nem o tamanho integral da resposta. Um status esperado não torna saudável uma resposta excessiva, truncada ou que excedeu o prazo.

| Saída | Significado |
| --- | --- |
| 0 | Sondagem saudável, ajuda ou demonstração bem-sucedida |
| 1 | Sondagem não saudável, inclusive rede/TLS/prazo/cancelamento; também falha da demonstração ou da escrita da saída |
| 2 | Argumentos ou configuração inválidos; nenhuma requisição |

Uma sondagem normal escreve um objeto JSON seguido de quebra de linha em stdout. Entrada inválida escreve diagnóstico fixo, sem valores recebidos, em stderr e não emite relatório. Falha de escrita pode deixar stdout parcial e gera diagnóstico fixo quando possível. Ctrl+C solicita cancelamento; terminação forçada não garante relatório.

## Demonstração offline determinística

```sh
go run ./cmd/health-probe demo
```

A demonstração inicia e encerra dois servidores `httptest` de loopback, executa GETs reais e confere ambos os resultados antes de imprimir:

```text
Offline demo: two loopback GETs; durations omitted from summaries.
{"case":"healthy","healthy":true,"status_code":200,"bytes_read":2,"reason":"ok"}
{"case":"synthetic-failure","healthy":false,"status_code":503,"bytes_read":4,"reason":"status_mismatch"}
Checks passed: 2/2. No external host or persistent state.
```

As linhas JSON são **resumos da demonstração**, não relatórios completos. Durações medidas e portas efêmeras são omitidas, não substituídas por valores inventados, mantendo a saída de sucesso determinística. Cada sondagem da demonstração tem prazo total de 10000 ms. O ambiente precisa permitir sockets de loopback.

## Biblioteca reutilizável

```go
package main

import (
    "context"
    "encoding/json"
    "os"

    "github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

func main() {
    cfg := probe.DefaultConfig("http://127.0.0.1:8080/health")
    cfg.ExpectedStatuses = []int{200, 204}
    report, err := probe.Run(context.Background(), cfg)
    if err != nil {
        // Apenas erros de entrada/configuração são retornados como error.
        panic(err)
    }
    if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
        panic("could not write report")
    }
}
```

`Config.Validate()` valida sem acessar a rede. O valor zero de `Config` é inválido; `DefaultConfig` fornece os padrões e uma lista de status independente. `Run` retorna relatório e erro nil mesmo para falhas de rede. Contexto nil é inválido. Cancelamento ou prazo anterior do chamador é respeitado.

Para TLS local de teste com confiança explícita, injete `probe.Prober{Transport: server.Client().Transport}.Run(ctx, cfg)`. O chamador gerencia esse transporte e deve garantir respeito ao contexto e ao contrato de `http.RoundTripper`. Campo nil seleciona o transporte padrão. Não altere a configuração durante a execução. Veja [API e JSON](docs/contract.md).

## Decisões e limitações

- Transporte HTTP/1.1 novo por sondagem padrão; sem redirecionamentos, tentativas HTTP adicionais, pool compartilhado, cookies, proxies do ambiente ou descompressão automática. A verificação normal de certificado e hostname TLS permanece ativa. É possível esperar 302 explicitamente, mas o destino nunca é visitado.
- Cabeçalhos de resposta limitados a 64 KiB e descarte do corpo em streaming. O byte extra detecta excesso. Buffers e enquadramento TLS/HTTP acrescentam custo; não é um limite de todos os bytes de rede ou da memória total do processo.
- Sem limiar de latência separado do prazo total, validação do conteúdo, lista de DNS permitidos, autenticação, certificados de cliente, cabeçalhos personalizados, modo HEAD ou histórico persistente.
- Disponibilidade é uma observação pontual, não garantia de uptime. DNS, conexões e escalonamento afetam a duração. O prazo não é uma garantia de término do processo em tempo real.
- O transporte confiável injetado na biblioteca pode mudar protocolo, confiança, limites e tentativas. Código arbitrário injetado não pode ser obrigado a respeitar contexto.
- URLs explícitas podem apontar para redes privadas ou loopback. Não use como filtro de SSRF nem exponha a chamadores não confiáveis com acesso privilegiado à rede. Queries são enviadas ao destino e podem estar visíveis nos argumentos do processo, apesar de omitidas dos relatórios.
- O projeto implementa um subconjunto documentado usando a pilha HTTP do Go; não afirma conformidade ampla com padrões HTTP.

Consulte o [projeto técnico](docs/design.md), [segurança](SECURITY.md) e [contribuição](CONTRIBUTING.md), em inglês. Licença [MIT](LICENSE), copyright 2026 Lofrano Tecnologia.
