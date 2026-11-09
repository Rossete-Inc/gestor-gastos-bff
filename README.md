# gestor-gastos-bff

BFF do Gestor de Gastos (Go, Lambda, OpenAPI). É a única API pública: entrega ao app os dados prontos de cada tela e delega aos serviços por Lambda Invoke (ADR 0004).

## Contrato

O [`openapi.yaml`](openapi.yaml) é a fonte da verdade da API. O frontend gera os tipos com `orval` a partir dele, numa versão fixada.

| Rota | Tela ou uso |
| --- | --- |
| `GET /me` | Perfil; cria o perfil sob demanda se ainda não existir |
| `GET /telas/dashboard?mes=` | Painel: totais, pizza nas 4 visões e últimos 5 gastos |
| `GET /telas/gastos?mes=&grupo=&categoriaId=&cursor=` | Lista de gastos paginada, com as categorias para os filtros |
| `GET /telas/gasto-form?id=` | Formulário de gasto; com `id`, traz o gasto para edição |
| `POST /gastos`, `PUT /gastos/{id}`, `DELETE /gastos/{id}` | Lançar, editar e excluir gasto |
| `GET /categorias`, `POST /categorias`, `DELETE /categorias/{id}` | Categorias padrão e personalizadas |
| `GET /admin/telas/usuarios` | Lista de usuários (admin) |
| `PATCH /admin/usuarios/{id}`, `DELETE /admin/usuarios/{id}` | Bloquear, reativar, alterar papel e excluir (admin) |

Convenções (detalhes no `info.description` do contrato):

- **Valores:** em centavos inteiros (`valorCentavos`).
- **Percentuais:** em décimos inteiros (`percentualDecimos`: 750 = 75,0%), que somam exatamente 1000 em cada visão.
- **Datas:** `AAAA-MM-DD`; mês `AAAA-MM`.
- **Paginação:** por cursor opaco.
- **Erros:** `application/problem+json`, com `code` estável e `errors[]` nos 422.

## Rodar localmente

O BFF local usa o cognito-local do infra-base e o `cmd/local` de cada serviço. Em três terminais:

```sh
# 1. infra-base: emuladores
task local:up

# 2. users-service
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8082

# 3. bff (este repositório)
set -a; source ../gestor-gastos-infra-base/local/.env.cognito; set +a
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8080
```

| Variável | Padrão |
| --- | --- |
| `RUNTIME` | Obrigatória; só aceita `local` |
| `COGNITO_ISSUER`, `COGNITO_CLIENT_ID` | Obrigatórias; vêm do `.env.cognito` |
| `COGNITO_ENDPOINT` | `http://localhost:9229` |
| `USERS_SERVICE_URL` | `http://127.0.0.1:8082` |
| `HOST`, `PORT` | `127.0.0.1`, `8080` |
| `CORS_ORIGINS` | `http://localhost:8081` (Expo web); aceita várias, separadas por vírgula |

O BFF só aceita endpoints locais. Para testar com um token de verdade:

```sh
TOKEN=$(aws cognito-idp initiate-auth --endpoint-url $COGNITO_ENDPOINT --client-id $COGNITO_CLIENT_ID \
  --auth-flow USER_PASSWORD_AUTH --auth-parameters USERNAME=admin@local.test,PASSWORD=Local1234 \
  --query AuthenticationResult.AccessToken --output text)
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/me
```

## Estado das rotas

| Rota | Estado |
| --- | --- |
| `GET /me` | Implementada (5.4) |
| Telas, gastos e categorias | Respondem 501 `nao_implementado` depois da autenticação. Chegam nas fatias 2 a 4 |
| `/admin/*` | Respondem 401 sem token, 403 para quem não é admin e 501 para admin. Chegam na fatia 5 |

## Estrutura

| Pacote | O que faz |
| --- | --- |
| `internal/api` | Rotas HTTP, autenticação (JWT por JWKS), checagem de `admin` em `/admin/*`, CORS local, log de requisições e recuperação de panic |
| `internal/me` | `GET /me`: obtém o perfil e, se não existir, cria com e-mail e nome do Cognito e papel dos grupos |
| `internal/cognito` | Lê e-mail e nome com o próprio access token (`GetUser`) |
| `internal/usuarios` | Client do users-service sobre o `invoke` da go-commons |
| `internal/authtest` | Emite tokens de teste assinados por um JWKS local |
| `cmd/local` | Servidor HTTP local |

Os testes da `internal/api` validam cada resposta contra o `openapi.yaml` com o kin-openapi. Se o código divergir do contrato, o teste falha.

## Lint

```sh
npx -y @stoplight/spectral-cli@6.17.1 lint openapi.yaml --fail-severity=warn
```

O [`.spectral.yaml`](.spectral.yaml) estende as regras padrão de OpenAPI e acrescenta duas regras do projeto:

- toda resposta 4xx e 5xx com corpo usa `application/problem+json`;
- todo campo `*Centavos` é `integer` `int64`.

A esteira roda o lint do contrato, o lint de Go e os testes em todo PR, e reprova o pacote que ficar abaixo de 80% de cobertura.
