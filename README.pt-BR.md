# CasaMOD Store

[English](README.md) | **Português (Brasil)**

Uma loja para o [CasaMOD](https://github.com/Cp0204/CasaMOD): instale,
atualize e remova **mods** e **wallpapers** do CasaMOD direto do GitHub, por
uma interface web que abre pelo ícone do app no CasaOS.

- **Loja**: itens de repositórios-fonte (por padrão
  [CasaMODs](https://github.com/UsuarioDeJava/CasaMODs) e
  [WE-Wallpapers](https://github.com/UsuarioDeJava/WE-Wallpapers)), os mods
  que vêm com o CasaMOD, ou qualquer repositório/pasta do GitHub colado como
  URL.
- **Atualizações**: verificadas a cada 6 horas (configurável) ou na hora. Para
  cada item você escolhe *Só avisar* (padrão) ou *Atualizar sozinho*.
- **Aviso no desktop**: um mini-mod (`casamod-store-notifier`) mostra um aviso
  discreto na área de trabalho do CasaOS quando há atualizações, com um link
  para a loja. Textos nos 31 idiomas do CasaOS.
- **Atualizações da própria loja**: aparecem na loja e no aviso do desktop, e
  são aplicadas pelo *Verificar e atualizar* do CasaOS.
- Leve: um binário Go estático (sem dependências), imagem multi-arquitetura
  (amd64, arm/v7, arm64), poucos MB de RAM.

## Requisitos

- CasaOS 0.4.x.
- [CasaMOD](https://github.com/Cp0204/CasaMOD) instalado e rodando (o
  container se chama `casamod`; outro nome pode ser definido em
  **Configurações**).
- Para wallpapers: o mod **Live Wallpapers** (do
  [CasaMODs](https://github.com/UsuarioDeJava/CasaMODs), também na loja), que
  reproduz as pastas de `mod/wallpapers/`.

## Instalar no CasaOS

Este repositório também é uma fonte de apps do CasaOS (`Apps/casamod-store/`).

1. Instale o CasaMOD primeiro.
2. No CasaOS: **Loja de Apps → Mais aplicativos**, adicione
   `https://github.com/UsuarioDeJava/CasaMOD-Store/archive/refs/heads/main.zip`.
3. Instale o **CasaMOD Store** pela App Store e abra pelo ícone (porta
   `8418`).

Na primeira inicialização a loja instala o mini-mod do aviso e reinicia o
CasaMOD uma vez; depois recarregue a página do CasaOS.

O CasaOS só oferece o *Verificar e atualizar* para apps que ele encontra numa
fonte, então instalar pela fonte é o que faz as atualizações da loja
funcionarem. Uma instalação personalizada de
[`Apps/casamod-store/docker-compose.yml`](Apps/casamod-store/docker-compose.yml)
(**Loja de Apps → Instalação personalizada → Importar**) usa o mesmo
`store_app_id` e também é reconhecida depois que a fonte é adicionada.

### Atualizar a loja

Quando uma imagem nova é publicada, a loja mostra o **CasaMOD Store** na aba
**Atualizações** e no aviso do desktop. Atualize pelo CasaOS: menu do ícone do
app (⋮) → **Verificar e atualizar**. Configurações e itens instalados são
mantidos: ficam em `/DATA/AppData/casamod-store`.

O CasaOS guarda a resposta da verificação por até 1 hora. Se ele disser que já
está na última versão logo depois de uma publicação, espere ou rode
`sudo systemctl restart casaos-app-management` no servidor e verifique de
novo.

### Desinstalar

1. Em **Configurações**, desligue **Aviso no CasaOS** e salve: a loja remove o
   mini-mod e reinicia o CasaMOD. (Ou apague
   `/DATA/AppData/casamod/mod/casamod-store-notifier` e reinicie o CasaMOD.)
2. Desinstale o app no CasaOS. Os mods e wallpapers instalados por ela
   continuam nas pastas do CasaMOD; remova-os pela loja antes, se não quiser
   mantê-los.
3. Se quiser, apague `/DATA/AppData/casamod-store` (configurações, token e
   lista de itens instalados).

### Sobre o socket do Docker

O compose monta `/var/run/docker.sock` para a loja poder reiniciar o container
`casamod`. Acesso ao socket dá a um container controle total do Docker na
máquina. A loja só chama o endpoint de *restart* do container definido nas
Configurações, mas se preferir, remova esse volume: a loja continua
funcionando e mostra um aviso pedindo para você reiniciar o CasaMOD.

A interface web não tem login (como a maioria dos apps do CasaOS): mantenha a
porta só na rede local / VPN. As chamadas que alteram algo exigem um
cabeçalho próprio, então outros sites não conseguem controlar a loja pelo seu
navegador.

### Token do GitHub (opcional)

Sem token, a API do GitHub permite 60 requisições por hora por IP. A loja usa
uma requisição por fonte ao carregar a loja (guardada por 30 minutos) e cerca
de uma por item instalado a cada verificação, o que cobre algumas dezenas de
itens. Adicione um token em **Configurações** para instalar de repositórios
privados ou subir o limite para 5000/hora. Um token *fine-grained* com acesso
somente leitura a *Contents* dos repositórios necessários basta. Ele fica
guardado só em `/DATA/AppData/casamod-store/config.json` (modo 0600) e nunca é
enviado de volta ao navegador.

## Usando a loja

**Loja**

- Busque, filtre por tipo (**Mods** / **Wallpapers**) ou por origem.
- **Instalar** instala um item. Marque vários cards e use **Instalar
  selecionados** para instalar todos de uma vez: o CasaMOD é reiniciado uma
  vez só, no fim.
- **Adicionar por URL do GitHub**: cole a URL de um repositório, pasta ou
  arquivo, clique em **Ler**, confira repositório, branch, pasta, ID (nome da
  pasta), tipo e política de atualização e clique em **Instalar**.
- Instalar um wallpaper sem o mod **Live Wallpapers** instalado pergunta
  antes: **Instalar agora** inclui o Live Wallpapers na mesma instalação (um
  único reinício do CasaMOD), **OK** instala só o wallpaper.
- **Recarregar** busca as fontes na hora, sem esperar os 30 minutos do cache.
- Avisos sobre fontes fora das regras de pastas aparecem acima dos cards (veja
  [Fontes](#fontes)).

**Instalados**

- **Gerenciados**: itens instalados pela loja, com origem e commit. Mude a
  política de atualização (*Só avisar* / *Atualizar sozinho*), reinstale um
  item cuja pasta sumiu, ou **Remova**.
- **Manuais**: pastas em `mod/` e `mod/wallpapers/` que a loja não instalou.
  A loja nunca altera nem remove essas pastas; instalar um item com o mesmo
  nome pede confirmação antes de substituir.

**Atualizações**

- Itens com commit mais novo (e a própria loja, quando há imagem nova).
- **Verificar agora** verifica na hora; **Atualizar tudo** atualiza todos os
  itens listados.
- **Atividade** mostra o que a loja fez (instalações, atualizações,
  reinícios, erros).

**Configurações**

| Configuração | |
|---|---|
| Token | Token do GitHub, veja acima. **Remover token** apaga. |
| Fontes | Uma por linha, veja [Fontes](#fontes). |
| Mods do CasaMOD | Lista os mods que vêm com o CasaMOD (de `Cp0204/CasaMOD`). |
| Verificar a cada (horas) | Intervalo da verificação automática; `0` desliga. |
| Aviso no CasaOS | Instala o mini-mod do aviso no desktop. |
| Endereço da loja | Link usado pelo aviso, ex.: `http://casaos.local:8418`. Vazio usa o endereço do CasaOS com a porta da loja. |
| Porta da loja | Porta usada no link do aviso quando o endereço está vazio. |
| Container | Nome do container do CasaMOD a reiniciar. |
| Reiniciar CasaMOD | Reinicia na hora. |

### Trocar a porta

Mude a porta publicada nas configurações do app no CasaOS e coloque o mesmo
valor em **Configurações → Porta da loja**, para o aviso do desktop apontar
para o lugar certo.

## Como funciona

| Item       | Instalado em                                  | Depois de instalar/remover  |
|------------|-----------------------------------------------|-----------------------------|
| mod        | `/DATA/AppData/casamod/mod/<id>`              | o CasaMOD é reiniciado      |
| wallpaper  | `/DATA/AppData/casamod/mod/wallpapers/<id>`   | nada (usado pelo Live Wallpapers) |

- O CasaMOD injeta o `mod.js`/`mod.css` de cada pasta de `mod/` na página do
  CasaOS quando inicia, então **adicionar ou remover um mod** exige reiniciar
  o container `casamod`. A loja faz isso pelo socket do Docker, uma vez por
  lote. **Atualizar** um mod instalado só troca os arquivos (servidos ao
  vivo): recarregue a página do CasaOS.
- Os downloads acontecem no servidor: o último commit que mexeu na pasta é
  lido da API do GitHub, o tarball desse commit é baixado e só a pasta é
  extraída. As instalações são **atômicas**: os arquivos são extraídos ao lado
  do destino e trocados por renomeação; um download com falha nunca deixa um
  mod pela metade, e uma troca interrompida é reparada na próxima
  inicialização.
- Um item tem atualização quando um commit mais novo mexeu **na pasta dele**;
  commits em outras partes do repositório não contam.
- A imagem da loja registra o commit de onde foi gerada; cada verificação
  compara com a última execução bem-sucedida do `docker-publish.yml` na
  `main`.
- O estado fica em `/DATA/AppData/casamod-store`: `config.json`
  (configurações e o token opcional) e `installed.json` (origem, commit e
  política de atualização de cada item).

## Fontes

Uma fonte é um repositório do GitHub (ou uma pasta dele) adicionado em
**Configurações → Fontes**, uma por linha, escrita como `dono/repo`, uma URL
do GitHub para uma pasta, uma URL do GitHub para um arquivo `.json`, ou
qualquer URL https para um arquivo de catálogo.

Sem um `store.json`, a loja lê a árvore do repositório (uma chamada à API) e
decide o que cada pasta é pelos arquivos **diretamente dentro dela**:

| A pasta tem, na raiz | Ela é um | Instalado em |
|---|---|---|
| `mod.js` e/ou `mod.css` | **mod** | `mod/<nome da pasta>/` |
| `project.json` ou `index.html` (e nenhum `mod.js`/`mod.css`) | **wallpaper** | `mod/wallpapers/<nome da pasta>/` |

Regras:

1. **O nome da pasta é o ID** (e o nome da pasta instalada). Dê à pasta o
   nome que ela deve ter em `mod/` ou `mod/wallpapers/`.
2. **Só a pasta mais externa conta.** Quando uma pasta é mod ou wallpaper,
   tudo dentro dela faz parte desse item: uma pasta de mod que contém uma
   pasta de wallpaper esconde esse wallpaper.
3. **Mod tem prioridade.** Uma pasta com `mod.js`/`mod.css` *e* `project.json`
   ou `index.html` é um mod.
4. Uma página na raiz da fonte (`index.html` ao lado do README) não é item.
5. Pastas ocultas (`.github`, …) e `test`, `tests`, `testdata`, `fixtures`,
   `mock(s)`, `docs`, `doc`, `example(s)` e `node_modules` são ignoradas.
6. Nome, descrição, versão e prévia vêm do `mod.json` (mods) ou do
   `project.json` (wallpapers) quando existem; senão, vale o nome da pasta.

Exemplo de estrutura:

```
meus-mods/                    (fonte: dono/meus-mods)
├── README.md
├── hide-footer/              → mod "hide-footer"
│   └── mod.css
├── music-player-widget/      → mod "music-player-widget"
│   ├── mod.js
│   ├── mod.css
│   └── mod.json              (opcional: name, description, version)
└── tests/                    → ignorada

meus-wallpapers/              (fonte: dono/meus-wallpapers)
├── starry-orbit/             → wallpaper "starry-orbit"
│   ├── index.html
│   ├── project.json          (opcional: title, description, preview)
│   └── js/ …
└── neural-plexus/            → wallpaper "neural-plexus"
    └── index.html
```

Exemplo de `mod.json`:

```json
{ "name": "Music Player", "description": "A music widget", "version": "1.2.0", "author": "me" }
```

O `project.json` é o arquivo do Wallpaper Engine; a loja lê `title`,
`description` e `preview` (caminho de uma imagem relativo à pasta).

A aba **Loja** mostra um aviso sobre a fonte quando:

- uma pasta tem arquivos de mod e de wallpaper (é lida como mod);
- uma pasta de item contém outras pastas de item (elas são ignoradas);
- duas pastas têm o mesmo nome (seriam instaladas na mesma pasta, ex.: duas
  pastas `wallpaper-engine/`);
- a fonte não tem nenhum mod nem wallpaper.

Pastas novas aparecem na loja sozinhas, e os itens instalados acompanham cada
commit na pasta deles.

### `store.json` opcional

Um `store.json` na raiz da fonte substitui a leitura automática e lista os
itens explicitamente:

```json
{
  "name": "My mods",
  "items": [
    {
      "id": "my-mod",
      "name": "My Mod",
      "type": "mod",
      "repo": "owner/repo",
      "branch": "main",
      "path": "mods/my-mod",
      "description": "What it does",
      "preview": "preview.png",
      "author": "me",
      "version": "1.0.0"
    }
  ]
}
```

- `type`: `mod` (padrão) ou `wallpaper`. Uma pasta de mod precisa ter
  `mod.js` ou `mod.css` na raiz, senão a instalação é recusada.
- `id`: nome da pasta; o padrão é o último trecho de `path`.
- `repo`: o padrão é o repositório da fonte; `branch`: o padrão é a branch
  padrão do repositório; `path`: o padrão é a raiz do repositório.
- `preview`: uma URL, ou um caminho relativo à pasta do item (`/` = raiz do
  repositório).

## Problemas comuns

| Problema | O que fazer |
|---|---|
| Um mod novo não aparece no CasaOS | O CasaMOD carrega os mods quando inicia: espere o reinício (ou **Configurações → Reiniciar CasaMOD**) e recarregue a página do CasaOS. |
| "Socket do Docker não montado" | O volume do socket foi removido: reinicie o CasaMOD você mesmo depois de adicionar ou remover mods. |
| "Falha ao reiniciar o CasaMOD" | Confira **Configurações → Container** e use **Reiniciar agora**. |
| "Not found on GitHub" | Repositório, branch ou pasta errados, ou repositório privado sem token. |
| "GitHub API rate limit reached" | Espere uma hora ou adicione um token em **Configurações**. |
| O CasaOS diz que a loja está atualizada, mas a loja mostra versão nova | A loja não foi instalada pela fonte de apps: adicione a fonte (veja [Instalar](#instalar-no-casaos)). O CasaOS também guarda a resposta por até 1 hora. |
| O aviso do desktop abre o endereço errado | Ajuste **Configurações → Endereço da loja** (ou **Porta da loja**). |
| Uma fonte mostra um aviso | Corrija a estrutura de pastas nesse repositório (veja [Fontes](#fontes)). |

## Configuração

Variáveis de ambiente (padrão entre colchetes):

| Variável | |
|---|---|
| `CASAMOD_STORE_ADDR` [`:8418`] | endereço de escuta |
| `CASAMOD_STORE_DATA` [`/DATA/AppData/casamod-store`] | configurações e estado |
| `CASAMOD_DIR` [`/DATA/AppData/casamod`] | pasta do CasaMOD |
| `DOCKER_SOCKET` [`/var/run/docker.sock`] | socket do Docker |
| `CASAMOD_STORE_RESTART_DELAY` [`3`] | segundos de espera (e agrupamento) antes de reiniciar o CasaMOD |

O resto fica na aba Configurações.

## Desenvolvimento

```sh
go test -race ./...           # testes unitários e de integração (GitHub e Docker falsos)
node tests/run.mjs            # testes de navegador (precisa de Go e Chrome/Chromium; use CHROME_BIN se necessário)
SCREENSHOTS=out node tests/run.mjs
go run ./cmd/casamod-store    # com CASAMOD_STORE_DATA / CASAMOD_DIR apontando para pastas temporárias
```

Ganchos de teste: `GITHUB_API_URL`, `GITHUB_CODELOAD_URL` e `GITHUB_RAW_URL`
apontam a loja para um GitHub falso; `CASAMOD_STORE_COMMIT` define o commit de
build usado pela verificação de versão da loja.

- `cmd/casamod-store`: ponto de entrada.
- `internal/github`: cliente da API/codeload do GitHub e leitura de URLs.
- `internal/installer`: extração segura de tarballs e trocas atômicas.
- `internal/store`: configurações, `installed.json`, fontes (`catalog.go`,
  leitura de pastas em `discover.go`).
- `internal/app`: instalar/atualizar/remover, verificações, versão da loja,
  API HTTP.
- `internal/docker`: reinicia o CasaMOD pelo socket do Docker.
- `internal/notifier`: o mini-mod `casamod-store-notifier` embutido.
- `internal/fakegithub`: GitHub em memória usado nos testes.
- `web/static`: a interface web (HTML/CSS/JS puros, embutidos no binário).
- `tests/`: testes de navegador: um harness do Chrome DevTools sem
  dependências, um GitHub falso e uma página que imita o CasaOS e carrega o
  aviso.
- `Apps/casamod-store/`: o app do CasaOS (formato de fonte de apps).

A imagem é publicada em `ghcr.io/usuariodejava/casamod-store` pelo
`.github/workflows/docker-publish.yml` a cada push na `main` que muda o
programa e em tags `v*`.
