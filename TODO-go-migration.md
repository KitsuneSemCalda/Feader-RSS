# Migração do backend para Go

Objetivo: substituir `rss-fetch.py` por um binário Go (`feader-rss-fetch` ou similar),
mantendo a mesma interface CLI (args + stdout JSON) para não precisar tocar no QML
além de trocar `python3 rss-fetch.py` pelo caminho do binário compilado.

## 1. Setup do módulo Go
- [x] `go mod init github.com/KitsuneSemCalda/feader-rss`
- [x] Layout: `cmd/feader-rss-fetch/main.go` + `internal/feed`, `internal/safefetch` e `internal/article`
- [x] `go.mod`/`go.sum` no controle de versão e `.gitignore` atualizado (binário compilado, `/bin`)

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
- [x] Sanitizar resumo com parser HTML: remover tags de apresentação/script, decodificar entidades,
  preservar parágrafos, preferir `content:encoded` e truncar em 500 runes
- [x] Manter o formato legado do `id`: `sha256(feedName + "\x00" + link)[:24]` (compatível com o estado salvo existente);
  a identidade independente do nome editável ficou registrada na seção 11
- [x] Ordenar itens por `published_at` normalizado (RSS/Atom) e aplicar `--limit`
  (feito no `internal/store` e no `cmd/feader-rss-fetch`)
- [x] Testes automatizados (`go test`) cobrindo RSS, Atom, Unicode, links relativos e migração SQLite

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
- [x] `refresh()` chama `fetch --db ... --limit ...`; o merge/preservação de `read` agora acontece no backend Go, não mais em JS
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

## 11. Auditoria do leitor e próxima fase — 2026-08-28

### Concluído nesta rodada
- [x] Normalizar datas RSS/Atom em `published_at`, mantendo o texto original para exibição.
- [x] Migrar o schema SQLite existente sem quebrar bancos criados pela versão 0.3.0.
- [x] Extrair autor e categorias de RSS/Atom e persistir esses metadados.
- [x] Limpar resumos HTML com parser, removendo `script`/`style`, preservando parágrafos e
  priorizando `content:encoded` quando o feed oferece a versão mais rica.
- [x] Truncar resumos por rune, sem cortar sequências UTF-8.
- [x] Resolver links relativos usando a URL do feed como base.
- [x] Rejeitar respostas HTTP fora da faixa 2xx antes de parsear ou armazenar conteúdo.
- [x] Unificar a resolução de nomes opcionais de feeds no QML, evitando que artigos válidos sejam filtrados fora.
- [x] Respeitar `XDG_CONFIG_HOME` também no frontend.
- [x] Melhorar os cards e o detalhe do artigo: estado read/unread, autor, data local, categorias,
  resumo RSS, conteúdo completo e estados vazios mais claros.
- [x] Remover o log `DEBUG` que era emitido a cada atualização de notificações.

### Dados, migração e experiência offline
- [ ] Atualizar `scripts/backup.sh` e `scripts/restore.sh` para incluir `items.db` com backup
  consistente de SQLite/WAL; restaurar o banco atual, não apenas o legado `items.json`.
- [ ] Adicionar testes de round-trip para backup/restore SQLite e para restauração de um backup
  em uma instalação que já possui um banco não vazio.
- [ ] Definir retenção: `maxItems` hoje limita apenas a resposta da listagem, enquanto o banco
  continua crescendo sem limite.
- [ ] Criar contagem de não lidos no banco, para o ícone não ignorar artigos antigos fora dos
  primeiros `maxItems`.
- [ ] Registrar falhas de prefetch com retry/backoff para não repetir indefinidamente as mesmas
  requisições a cada refresh.
- [ ] Usar todos os endereços públicos validados como fallback de conexão; hoje apenas o primeiro
  IP retornado pelo DNS é tentado, o que pode falhar em hosts com IPv6 indisponível.
- [ ] Restringir permissões do diretório/arquivo SQLite (`0700`/`0600`) e documentar a política
  de privacidade do cache local.

### Identidade e compatibilidade de artigos
- [ ] Fazer o ID depender da identidade estável do feed (URL canônica ou ID Atom/RSS), não do
  nome editável exibido ao usuário; preservar IDs antigos através de migração/alias.
- [ ] Deduplicar itens repetidos dentro de uma mesma resposta antes de gerar `newItems` e
  notificações.
- [ ] Normalizar URLs de artigos com cuidado, sem alterar o caminho/query de forma incorreta,
  e adicionar índice para consultas por URL no cache.

### Configuração e frontend
- [ ] Validar e normalizar configurações carregadas de arquivo: limite de oito feeds, nomes/URLs
  válidos, `maxItems`, intervalo e preferências com números finitos.
- [ ] Evitar que `mark all` opere silenciosamente sobre artigos de feeds removidos; decidir se a
  ação se aplica ao banco inteiro ou somente aos feeds visíveis.
- [ ] Atualizar a contagem de não lidos e o fluxo de notificações para não depender apenas do
  subconjunto carregado na UI.
- [ ] Executar um teste manual/automatizado real no Quickshell: abrir painel, trocar tema,
  configurar feed, refresh, filtros, teclado, detalhe, cache e ciclo de vida do processo.
- [ ] Substituir os testes QML baseados apenas em busca de strings por testes de comportamento
  onde a infraestrutura do Quickshell permitir.

### Distribuição e supply chain
- [x] Corrigir o workflow de release: cada job publica um checksum com nome único e o job final
  valida e combina os quatro arquivos antes de publicar `checksums.txt`.
- [ ] Amarrar o binário instalado a uma referência imutável e revisada (commit/tag protegido,
  digest esperado ou artefato construído a partir do source revisado). O checksum baixado da
  mesma release mutável não é suficiente por si só.
- [ ] Tornar a verificação de attestation explícita quanto ao workflow e ao commit de origem,
  em vez de verificar somente o repositório com `gh attestation verify --repo`.
- [ ] Fazer o instalador montar uma cópia temporária e trocar o plugin somente depois que o
  binário foi construído/verificado; uma falha hoje pode deixar a instalação parcialmente limpa.
- [ ] Não cair silenciosamente para um binário remoto quando o build local falha; oferecer uma
  escolha explícita ou falhar com diagnóstico.
- [ ] Validar no CI que a versão do `manifest.json` corresponde à tag da release.

### Qualidade e documentação
- [x] Adicionar cobertura para status HTTP, metadados RSS/Atom, links relativos, Unicode e
  migração do schema SQLite.
- [ ] Completar a matriz de testes para redirects seguros/privados, charset, conteúdo RSS/Atom
  completo, datas inválidas, concorrência do SQLite e CLI.
- [ ] Fazer `go test -race ./...` funcionar no ambiente de CI e registrar a versão/toolchain
  suportada; no ambiente local de 2026-08-28 o Go 1.27 falha em `runtime/race` antes de rodar
  os testes (`package testmain cannot find package`).
- [ ] Atualizar README e scripts para refletir o backup SQLite, os diretórios XDG e a sequência
  correta de instalação/restore.
- [ ] Marcar o bump de versão do manifest somente quando a próxima release estiver pronta.
