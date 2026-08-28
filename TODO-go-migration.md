# Migração do backend para Go

Objetivo: substituir `rss-fetch.py` por um binário Go (`feader-rss-fetch` ou similar),
mantendo a mesma interface CLI (args + stdout JSON) para não precisar tocar no QML
além de trocar `python3 rss-fetch.py` pelo caminho do binário compilado.

## 1. Setup do módulo Go
- [x] `go mod init github.com/KitsuneSemCalda/feader-rss`
- [x] Layout: `cmd/feader-rss-fetch/main.go` + `internal/feed`, `internal/safefetch` (`internal/article` pendente, item 4)
- [ ] Adicionar `go.mod`/`go.sum` ao controle de versão, atualizar `.gitignore` (binário compilado, `/bin`)

## 2. Camada de rede segura (equivalente a `require_safe_url`/pinning) — feito
- [x] Reimplementar validação de URL: apenas `http`/`https`, hostname obrigatório
- [x] Resolver DNS manualmente (`net.DefaultResolver.LookupNetIP`) e rejeitar IPs privados/loopback/link-local/multicast/reservados (`net/netip`, `Unmap()` cobre IPv4-mapped IPv6)
- [x] Pinar a conexão no IP validado via `http.Transport.DialContext` customizado, mantendo SNI/verificação TLS pelo hostname original
- [x] Revalidar e repinar cada redirect via `http.Client.CheckRedirect`
- [x] Limitar tamanho da resposta (`ReadCapped`, erro `ErrResponseTooLarge` se exceder)
- [x] Timeouts equivalentes aos `timeout=15`/`timeout=20` do Python (parametrizado por chamada)
- [x] User-Agent customizado (`io.github.kitsunesemcalda.feader-rss/0.2`)
- Testado manualmente: RSS 2.0 e Atom reais funcionam; requisição a `127.0.0.1` é bloqueada.

## 3. Parsing de feeds (RSS/Atom) — feito
- [x] Parsear XML com `encoding/xml` cobrindo `<rss><channel><item>` e Atom `<feed><entry>`
- [x] Extrair título, link (RSS `<link>` texto vs Atom `<link href="">`, preferindo `rel="alternate"`), data (`pubDate`/`published`/`updated`), resumo (`description`/`content:encoded` ou `summary`/`content`)
- [x] Sanitizar resumo: remover tags HTML, unescape de entidades (`html.UnescapeString`), colapsar espaços, truncar em 500 chars
- [x] Gerar `id` estável: `sha256(feedName + "\x00" + link)[:24]` (mesmo formato hex do Python, compatível com estado salvo existente)
- [x] Ordenar itens por `published` desc e aplicar `--limit` (feito no `cmd/feader-rss-fetch/main.go`)
- [ ] Testes automatizados (`go test`) cobrindo os dois formatos — ver item 8

## 4. Extração de artigo (equivalente ao `ArticleParser`) — feito
- [x] `golang.org/x/net/html` para parsear o HTML da página (`internal/article`)
- [x] Lógica de bloco/skip tags replicada (`article, br, div, h1-4, li, p, pre, section` vs `aside, footer, form, header, nav, script, style, svg`)
- [x] Extração de `<title>` e texto legível, normalização de espaços/pontuação/linhas em branco igual ao Python
- [x] Detecção de charset via `golang.org/x/net/html/charset` a partir do `Content-Type`

## 5. CLI e formato de saída — feito (com subcomandos em vez de flags únicas)
- [x] CLI reorganizada em subcomandos: `fetch`, `list`, `article`, `mark-read`, `mark-all` (`cmd/feader-rss-fetch/main.go`)
- [x] Saída JSON: `{"items":[...],"errors":[...],"newItems":[...]}` para `fetch`/`list`; `{"url","title","content"}` ou `{"error","url"}` para `article`
- [x] Erros de feed individual não abortam os demais; log em stderr
- [x] Exit code 1 quando `article` falha
- [x] `encoding/json` emite UTF-8 nativamente (`SetEscapeHTML(false)` para paridade com `ensure_ascii=false`)

## 6. Persistência (SQLite) — feito, além do escopo original do TODO
- [x] `internal/store`: banco SQLite (`modernc.org/sqlite`, puro Go, sem cgo) substitui o antigo JSON de estado gerenciado em JS
- [x] `Upsert` insere itens novos e atualiza metadados sem sobrescrever a flag `read`; retorna os itens realmente novos (para notificações)
- [x] `List`, `MarkRead`, `MarkAllRead`, `SetContent` (cache do conteúdo extraído do artigo)
- [x] Testes cobrindo upsert, preservação de `read`, `mark-all`, limite de `list`

## 7. Build e distribuição
- [x] `go build ./...` funcional; binário único `cmd/feader-rss-fetch`
- [x] Compilação cross-platform via `GOOS`/`GOARCH` no workflow de release (linux/darwin × amd64/arm64), `CGO_ENABLED=0`
- [x] `scripts/install.sh` baixa o binário já compilado da release do GitHub (não builda localmente) — ver seção 9

## 8. Integração com QML — feito
- [x] `Panel.qml`: `fetchScript` (caminho do `.py`) virou `fetchBinary` (caminho do binário compilado), resolvido via `Qt.resolvedUrl`
- [x] `statePath` agora guarda só preferências de UI (`preferences.json`); artigos migraram para `dbPath` (`items.db`)
- [x] `refresh()` chama `fetch --db ... --limit ...`; o merge/dedup/preservação de `read` agora acontece no backend Go, não mais em JS
- [x] Novo `listProcess` chama `list --db ...` na inicialização para popular `articles` a partir do banco
- [x] `markRead`/`markAllRead`/`markAllUnread` atualizam o estado local otimisticamente e persistem via `Quickshell.execDetached([...])` chamando `mark-read`/`mark-all`
- [x] `loadArticle` chama `article --db ... <url>` em vez do script Python
- [x] Testado manualmente via CLI (fetch → list → article → mark-read) fora do Quickshell; teste real dentro do Quickshell ainda pendente (ver Observações)

## 9. Instalação e CI/CD — feito
- [x] `scripts/install.sh`: detecta OS/arch, baixa `feader-rss-fetch_<version>_<os>_<arch>` do GitHub Release (`vX.Y.Z`), valida com `checksums.txt` via `sha256sum`
- [x] `.github/workflows/ci.yml`: job Go separado (`go vet`, `gofmt -l`, `go test -race`, `go build`) além da validação Python existente
- [x] `.github/workflows/release.yml`: matrix build (linux/darwin × amd64/arm64), `checksums.txt`, upload dos binários e do zip do plugin na mesma release

## 10. Limpeza final
- [x] Removidos `rss-fetch.py` e `tests/test_rss_fetch.py` (paridade coberta pelos testes Go)
- [x] `tests/test_ui_accessibility.py` atualizado para as novas asserções de comando (`fetchBinary`, `dbPath`)
- [x] `.gitignore` atualizado: binário compilado, `dist/`, arquivos `*.db*`
- [ ] Atualizar `README.md` (requisitos: Go em vez de Python, instruções de build/uso do binário)
- [ ] Bump de versão no `manifest.json` (necessário antes do próximo `git tag vX.Y.Z` para a release funcionar)

## Observações
- Prioridade alta: preservar exatamente as proteções de SSRF/DNS-rebinding já implementadas
  (commits `7dca620`, `a76329f`, `09cc542`) — feito e testado em `internal/safefetch`.
- Formato do `id` do artigo (`sha256[:24]`) preservado — compatível com qualquer estado antigo,
  embora o formato de armazenamento tenha mudado de JSON para SQLite (arquivo `items.db`).
- Pendente: validar o fluxo dentro do Quickshell de verdade (não só via CLI) — abrir o painel,
  configurar feeds, refresh automático pelo timer, abrir artigo, marcar como lido/não lido.
- Pendente: `scripts/install.sh` depende de uma release publicada (`vX.Y.Z` com os assets); antes
  do primeiro release com binários Go, o passo `fetch_binary` vai falhar — precisa de um primeiro
  `git tag`/push para popular a release.

## Observações
- Prioridade alta: preservar exatamente as proteções de SSRF/DNS-rebinding já implementadas
  (commits `7dca620`, `a76329f`, `09cc542`) — não regredir segurança durante a reescrita.
- Manter o formato do `id` do artigo (`sha256[:24]`) para compatibilidade com o estado
  já salvo em `~/.local/state/omarchy/rss-reader/items.json` dos usuários existentes.
