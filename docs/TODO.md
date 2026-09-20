# Feader RSS: pendências

A migração Python → Go e SQLite está concluída. O histórico de mudanças fica
em [CHANGELOG.md](CHANGELOG.md) e no Git. Esta lista reúne o trabalho aberto.

## P0

- [ ] Integrar a instalação do backend ao fluxo público do Omarchy.
  `omarchy plugin add` não executa `scripts/install.sh`, que faz build ou
  download verificado do binário. O README agora orienta usar esse script;
  a integração automática continua pendente.
- [ ] Dividir `Panel.qml`, que concentra estado, configuração, processos e
  interface. Separar as telas de lista, artigo e configuração conforme as
  responsabilidades existentes.
- [ ] Atualizar FTS5 por artigo. `rebuildFTS()` recria o índice inteiro em
  várias operações de escrita; reservar o rebuild global para reparo e
  migração. Validar com retenção de até 100.000 artigos.

## P1

- [ ] Versionar migrações SQLite com `PRAGMA user_version` ou uma tabela de
  migrações, incluindo migração de identidade e importação do JSON legado.
- [ ] Centralizar no Go as regras de configuração ainda duplicadas no QML,
  mantendo a apresentação e coordenação da interface no QML.
- [ ] Documentar os comandos e respostas JSON usados pelo QML. Avaliar
  versão de protocolo e códigos de erro quando houver mudança incompatível.
- [ ] Testar o painel no Quickshell: abrir, trocar tema, configurar feeds,
  atualizar, filtrar, navegar pelo teclado, pesquisar, abrir artigo,
  favoritar e fechar/reabrir. Substituir testes por busca de strings por
  testes de comportamento onde houver suporte.
  A validação registrada em 2026-09-03 cobriu instalação, refresh de feeds
  reais, SQLite e ícone de não lidos; os cliques no painel ainda faltam.
- [ ] Acrescentar diagnóstico opcional de tempos de fetch, parse, banco,
  FTS e prefetch, com falhas identificadas por feed.

## P2

- [ ] Avaliar a ordem de prefetch: priorizar artigos não lidos e recentes,
  respeitando os limites de concorrência e o backoff persistente.
- [ ] Melhorar a escolha do conteúdo principal em páginas sem marcação
  semântica adequada, com exemplos de páginas que falham no extrator atual.
- [ ] Sincronizar a versão do User-Agent com a versão publicada. O workflow
  passa `-X main.version`, mas o backend ainda não declara essa variável.
  Corrigir também as dependências diretas marcadas como indiretas no `go.mod`.
- [ ] Cobrir datas de publicação inválidas ou malformadas e ampliar os
  testes da CLI. Redirects públicos/privados, charset e concorrência SQLite
  já têm testes; a concorrência usa conexões independentes no mesmo arquivo.
- [ ] Identificar se resta alguma lacuna concreta na distribuição antes de
  acrescentar verificações. Preservar checksum, attestation, workflow e
  commit de origem verificados pelo instalador.
- [ ] Atualizar a versão do manifest quando a próxima release estiver pronta.
