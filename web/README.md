# web

Frontend Next.js para o painel do assistente pessoal.

## Stack

- Next.js 14 (App Router) + TypeScript
- Tailwind CSS + shadcn/ui (componentes locais)
- recharts (timeline psicologica)
- pt-BR fixo (sem i18n)

## Rodar local

Em outro terminal, suba o bot Go (que expoe a API REST em `:8080`):

```bash
cd ../bot
go run .
```

Em seguida:

```bash
cd web
npm install
cp .env.example .env.local
# ajuste NEXT_PUBLIC_API_BASE_URL se necessario
npm run dev
```

App sobe em `http://localhost:3000`.

Para autenticar, abra `/login`, peca um link com seu numero (o backend
envia pelo WhatsApp) e clique no link recebido.

## Build

```bash
npm run build
```

## Estrutura

```
src/
  app/                       # rotas (App Router)
  components/
    ui/                      # primitivas (shadcn/ui local)
    forms/                   # PhoneInput, CepInput, formularios
    family/                  # cards e timeline do dependente
  lib/
    api.ts                   # fetch wrapper
    api/                     # modulos por dominio (auth, family, users)
    masks.ts                 # maskPhone, maskCep, normalizePhoneE164BR
    viacep.ts                # cliente ViaCEP
    timezones.ts             # lista de fusos brasileiros
  types/api.ts               # tipos compartilhados com o backend
```

## Convencoes

- Inputs brasileiros (telefone, CEP) tem componente reusavel em
  `components/forms`. Mascara so na apresentacao; persistencia usa digitos
  puros.
- Server components fazem fetch da API encaminhando o cookie de sessao via
  `getSessionCookieHeader()`.
- Erros do backend chegam como `ApiError` com `status` e `code`.
