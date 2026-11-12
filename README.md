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

O BFF local usa o cognito-local do infra-base e o `cmd/local` de cada serviço. Em cinco terminais:

```sh
# 1. infra-base: emuladores
task local:up

# 2. users-service
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8082

# 3. categories-service
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8083

# 4. expenses-service
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8084

# 5. bff (este repositório)
set -a; source ../gestor-gastos-infra-base/local/.env.cognito; set +a
RUNTIME=local go run ./cmd/local          # http://127.0.0.1:8080
```

| Variável | Padrão |
| --- | --- |
| `RUNTIME` | Obrigatória; só aceita `local` |
| `COGNITO_ISSUER`, `COGNITO_CLIENT_ID` | Obrigatórias; vêm do `.env.cognito` |
| `COGNITO_ENDPOINT` | `http://localhost:9229` |
| `USERS_SERVICE_URL` | `http://127.0.0.1:8082` |
| `CATEGORIES_SERVICE_URL` | `http://127.0.0.1:8083` |
| `EXPENSES_SERVICE_URL` | `http://127.0.0.1:8084` |
| `HOST`, `PORT` | `127.0.0.1`, `8080` |
| `CORS_ORIGINS` | `http://localhost:8081` (Expo web); aceita várias, separadas por vírgula |

O BFF só aceita endpoints locais. Para testar com um token de verdade:

```sh
TOKEN=$(aws cognito-idp initiate-auth --endpoint-url $COGNITO_ENDPOINT --client-id $COGNITO_CLIENT_ID \
  --auth-flow USER_PASSWORD_AUTH --auth-parameters USERNAME=admin@local.test,PASSWORD=Local1234 \
  --query AuthenticationResult.AccessToken --output text)
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/me
```

Categorias (tarefa 7.3):

```sh
B=http://127.0.0.1:8080; H="Authorization: Bearer $TOKEN"
J='Content-Type: application/json'
curl -s -H "$H" $B/categorias                                    # 12 padrão + as suas, por grupo
curl -s -H "$H" -H "$J" -X POST $B/categorias -d '{"nome":"Pet","grupo":"CARTAO","cor":"#17BEBB"}'   # 201 com o id
curl -s -H "$H" -H "$J" -X POST $B/categorias -d '{"nome":"pet","grupo":"CARTAO","cor":"#17BEBB"}'   # 409 categoria_duplicada
curl -s -H "$H" -X DELETE $B/categorias/alimentacao              # 403 categoria_padrao
curl -s -o /dev/null -w '%{http_code}\n' -H "$H" -X DELETE $B/categorias/<id>   # 204
curl -s -H "$H" $B/telas/gasto-form                              # {"categorias":{...}}
```

Gastos (tarefa 8.3). O grupo do gasto vem da categoria; o cliente não o envia:

```sh
curl -s -H "$H" -H "$J" -X POST $B/gastos -d '{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","descricao":"Almoço"}'   # 201
curl -s -H "$H" -H "$J" -X POST $B/gastos -d '{"valorCentavos":0,"data":"2026-11-03","categoriaId":"alimentacao"}'   # 422 valorCentavos
curl -s -H "$H" -H "$J" -X POST $B/gastos -d '{"valorCentavos":100,"data":"2026-11-03","categoriaId":"nao-existe"}'   # 422 categoriaId
curl -s -H "$H" "$B/telas/gastos?mes=2026-11&grupo=CARTAO&limit=20"   # itens com nome e cor da categoria + nextCursor
curl -s -H "$H" "$B/telas/gasto-form?id=<id>"                       # categorias + o gasto para edição
curl -s -H "$H" "$B/telas/dashboard?mes=2026-11"                     # totais, pizza nas 4 visões e últimos 5
curl -s -H "$H" -H "$J" -X PUT $B/gastos/<id> -d '{"valorCentavos":9990,"data":"2026-11-04","categoriaId":"internet"}'   # 200; data nova = id novo
curl -s -H "$H" -X DELETE $B/categorias/internet                     # 403 (padrão); numa personalizada com gastos, 409 categoria_em_uso
curl -s -o /dev/null -w '%{http_code}
' -H "$H" -X DELETE $B/gastos/<id>   # 204
```

Regras do BFF nas rotas de gastos:

- **Categoria do gasto:** o BFF busca a categoria no categories-service (`ObterCategoria`). Inexistente ou de outro usuário vira 422 no campo `categoriaId`, e o expenses-service nem é chamado.
- **Categoria em uso:** antes de excluir uma categoria, o BFF pergunta ao expenses-service (`ExisteGastoNaCategoria`) e responde 409 `categoria_em_uso`. A corrida entre a checagem e a exclusão é aceita no MVP.
- **Categoria removida:** um gasto cuja categoria não está mais entre as visíveis aparece com a categoria "Removida" (cor `#9E9E9E`).
- **Telas:** `/telas/gastos`, `/telas/gasto-form?id=` e `/telas/dashboard` buscam gastos e categorias em paralelo.
- **Painel:** o expenses-service (`ResumoMensal`) devolve as somas exatas em centavos; o BFF junta nome e cor das categorias e calcula `percentualDecimos` pelo método do maior resto ([`internal/painel`](internal/painel)), para que cada visão não vazia some 1000 (100,0%). As visões `CARTAO` e `FIXO` têm percentuais sobre o total do grupo. Na visão `todas`, uma categoria que aparecer nos dois grupos vira uma fatia só. A visão `grupos` só tem os grupos com gasto no mês. Fatias em ordem decrescente de valor.
- **Query string:** `mes` fora de `AAAA-MM`, `grupo` fora de `CARTAO`/`FIXO`, `limit` fora de 1 a 100 ou `categoriaId` vazio respondem 400 `parametro_invalido`.

Regras do BFF nas rotas de admin:

- **Grupo admin:** o BFF confere o grupo `admin` no token antes de chamar o users-service. Quem não está no grupo recebe 403 `somente_admin`, e o users-service nem é chamado. O `sub` do admin vai no envelope do invoke, para as regras "a si mesmo".
- **Regras da conta:** último admin, bloquear ou excluir a si mesmo e transições de status são do users-service ([`ListarUsuarios`, `AtualizarUsuario`, `ExcluirUsuario`](https://github.com/Rossete-Inc/gestor-gastos-users-service#operações-de-admin)). Os 404, 409 e 422 dele passam como estão.
- **Corpo do `PATCH`:** o BFF só confere o formato do JSON (campos fora de `status` e `papel` respondem 400 `corpo_invalido`). Valor fora do enum ou corpo sem campo nenhum voltam 422 do users-service.
- **Lista:** o users-service devolve `items`; a tela devolve `itens`, como as outras telas. `limit` fora de 1 a 100 responde 400 `parametro_invalido`, e o conteúdo do cursor é conferido pelo users-service (400 `cursor_invalido`).

## Estado das rotas

| Rota | Estado |
| --- | --- |
| `GET /me` | Implementada (5.4) |
| `GET/POST /categorias`, `DELETE /categorias/{id}`, `GET /telas/gasto-form` | Implementadas (7.3), em `dev` desde a 7.5. O 409 `categoria_em_uso` e o `?id=` do formulário entraram na 8.3 |
| `POST /gastos`, `PUT/DELETE /gastos/{id}`, `GET /telas/gastos` | Implementadas (8.3), em `dev` desde a 8.5, junto com o `?id=` do formulário e a checagem de gastos na exclusão de categoria |
| `GET /telas/dashboard` | Implementada (9.2). O smoke test em `dev` confere o painel com o gasto de teste |
| `GET /admin/telas/usuarios`, `PATCH/DELETE /admin/usuarios/{id}` | Implementadas (10.2). Em `dev`, `PATCH` e `DELETE` respondem 400 `operacao_desconhecida` do users-service até a 10.4 dar à Lambda dele o User Pool e o barramento de eventos |

## Estrutura

| Pacote | O que faz |
| --- | --- |
| `internal/api` | Rotas HTTP, autenticação (JWT por JWKS), checagem de `admin` em `/admin/*`, CORS local, log de requisições e recuperação de panic |
| `internal/me` | `GET /me`: obtém o perfil e, se não existir, cria com e-mail e nome do Cognito e papel dos grupos |
| `internal/cognito` | Lê e-mail e nome com o próprio access token (`GetUser`) |
| `internal/usuarios` | Client do users-service sobre o `invoke` da go-commons |
| `internal/categorias` | Client do categories-service sobre o `invoke` da go-commons |
| `internal/gastos` | Client do expenses-service sobre o `invoke` da go-commons |
| `internal/lambdahttp` | Converte o evento da HTTP API (payload 2.0) em `http.Request` e a resposta de volta |
| `internal/authtest` | Emite tokens de teste assinados por um JWKS local |
| `cmd/local` | Servidor HTTP local |
| `cmd/lambda` | Lambda na AWS: o mesmo handler, com o users-service e o categories-service por Lambda Invoke |
| `infra/` | App CDK (módulo Go à parte) da stack `gestor-gastos-dev-bff` |
| `scripts/smoke.sh` | Smoke test de `/me`, `/categorias`, do ciclo de um gasto, do painel e do 403 do admin em dev, rodado pela esteira depois do deploy |

Os testes da `internal/api` validam cada resposta contra o `openapi.yaml` com o kin-openapi. Se o código divergir do contrato, o teste falha.

## Lint

```sh
npx -y @stoplight/spectral-cli@6.17.1 lint openapi.yaml --fail-severity=warn
```

O [`.spectral.yaml`](.spectral.yaml) estende as regras padrão de OpenAPI e acrescenta duas regras do projeto:

- toda resposta 4xx e 5xx com corpo usa `application/problem+json`;
- todo campo `*Centavos` é `integer` `int64`.

A esteira roda o lint do contrato, o lint de Go e os testes em todo PR, e reprova o pacote que ficar abaixo de 80% de cobertura.

## Deploy na AWS (dev)

A esteira ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) chama o `go-service.yml` do infra-base. No PR: qualidade, build arm64, testes do CDK e `cdk diff` comentado. No merge na `main`: deploy em `dev` e smoke test. **Ordem do primeiro deploy:** infra-base (auth e api) → users-service, categories-service e expenses-service → BFF.

### Stack `gestor-gastos-dev-bff`

| Recurso | Configuração |
| --- | --- |
| Lambda `gestor-gastos-dev-bff` | `provided.al2023`, arm64, 256 MB, timeout de 10 s, sem VPC; binário `dist/lambda/bootstrap` de `./cmd/lambda` |
| Alias `live` + CodeDeploy | `CodeDeployDefault.LambdaCanary10Percent5Minutes`, rollback automático em falha, parada ou alarme |
| Alarme `gestor-gastos-dev-bff-erros` | Qualquer erro da função no alias em 1 minuto; reverte o canary |
| Logs `/aws/lambda/gestor-gastos-dev-bff` | 7 dias |
| Rotas na HTTP API do infra-base | Uma rota por operação do `openapi.yaml` (13), todas com o authorizer JWT do infra-base e a integração `AWS_PROXY` (payload 2.0) com o alias `live`. A API, o stage, o CORS e a rota `GET /infra/auth-check` continuam no infra-base |
| Permissões | A HTTP API invoca só o alias `live`; a Lambda invoca só os alias `live` do users-service, do categories-service e do expenses-service |

Parâmetros do SSM:

| Parâmetro | Sentido | Uso |
| --- | --- | --- |
| `/gestor-gastos/dev/auth/issuer-url`, `auth/user-pool-client-id`, `auth/jwks-url` | lido (infra-base) | `COGNITO_ISSUER`, `COGNITO_CLIENT_ID`, `COGNITO_JWKS_URL` |
| `/gestor-gastos/dev/api/id`, `api/authorizer-id` | lido (infra-base) | Rotas e integração |
| `/gestor-gastos/dev/users/function-arn` | lido (users-service) | `USERS_FUNCTION_ARN` e `lambda:InvokeFunction` |
| `/gestor-gastos/dev/categories/function-arn` | lido (categories-service) | `CATEGORIES_FUNCTION_ARN` e `lambda:InvokeFunction` |
| `/gestor-gastos/dev/expenses/function-arn` | lido (expenses-service) | `EXPENSES_FUNCTION_ARN` e `lambda:InvokeFunction` |
| `/gestor-gastos/dev/bff/function-arn` | publicado | ARN do alias `live` do BFF |

Na esteira, o app lê esses parâmetros no synth (`ssm:GetParameters`, com as credenciais do job) e grava os valores no template. Isso é obrigatório: a role de deploy do bootstrap do CDK só lê o parâmetro de versão do bootstrap, então um parâmetro do CloudFormation do tipo `AWS::SSM::Parameter::Value<String>` falharia no `CreateChangeSet` com AccessDenied. É o mesmo tratamento do users-service. O modo vem do contexto `verificarSsm`, que o `ci.yml` passa; sem ele, a variável `AMBIENTE` do `go-service.yml` liga o modo `erro`:

- `erro` (deploy na `main`): parâmetro ausente reprova o synth dizendo qual falta e quem o publica, sem criar nada.
- `aviso` (`cdk diff` no PR): a falta vira aviso, e a stack volta aos parâmetros do CloudFormation.
- Sem contexto e sem `AMBIENTE` (testes e synth local): nada é consultado, e cada valor vira um parâmetro do CloudFormation (`valueForStringParameter`), resolvido só no deploy.

Variáveis da Lambda (`cmd/lambda`): `RUNTIME=aws`, `ENV=dev`, `LOG_LEVEL`, `COGNITO_ISSUER`, `COGNITO_CLIENT_ID`, `COGNITO_JWKS_URL`, `USERS_FUNCTION_ARN`, `CATEGORIES_FUNCTION_ARN` e `EXPENSES_FUNCTION_ARN`. O CORS é feito pela HTTP API.

### Smoke test

[`scripts/smoke.sh`](scripts/smoke.sh) roda depois do deploy, com a role de deploy:

1. `GET /me` sem token: 401 do authorizer.
2. Garante o usuário `smoke-test@example.com` no User Pool (criado sem e-mail de convite), troca a senha por uma aleatória a cada execução e pede um access token com `ADMIN_USER_PASSWORD_AUTH`. Nenhuma senha fica guardada.
3. `GET /me` com o token: 200 com o perfil do usuário de smoke test.
4. `GET /categorias`: 200 com as 12 categorias padrão.
5. Gastos: `GET /telas/gastos` do mês, `POST /gastos` de R$ 0,01 em Alimentação, `GET /telas/gasto-form?id=` e `DELETE /gastos/{id}` (depois, o formulário responde 404). O gasto de teste não fica na tabela.
6. Painel: `GET /telas/dashboard` do mês, antes do `DELETE`, com o gasto de teste nos últimos e cada visão da pizza somando 100%.
7. Admin: `GET /admin/telas/usuarios` com o token do usuário de smoke test, que é cliente: 403 `somente_admin`.

Os passos 2 e 3 dependem de duas mudanças no infra-base (PR #16). O `ci.yml` roda com `SMOKE_EXIGIR_TOKEN=1`, então a falta delas reprova o smoke test. Sem a variável, os passos são pulados com um aviso:

- `infra/auth.go`: `AuthFlows: &awscognito.AuthFlow{UserSrp: jsii.Bool(true), AdminUserPassword: jsii.Bool(true)}` no app client (`ALLOW_ADMIN_USER_PASSWORD_AUTH`). O fluxo só é aceito com credenciais IAM, então não muda nada para o app público.
- `infra/cicd.go`: na role `gh-deploy-gestor-gastos-bff-dev`, `cognito-idp:AdminGetUser`, `AdminCreateUser`, `AdminSetUserPassword` e `AdminInitiateAuth` em `arn:aws:cognito-idp:us-east-1:<conta>:userpool/*`.

### Comandos locais (sem AWS)

```sh
go test ./...                                   # BFF, incluindo o cmd/lambda
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags="-s -w" -o dist/lambda/bootstrap ./cmd/lambda
cd infra
go test ./...                                   # testes de assertion do CDK
npx --yes aws-cdk@2.1148.0 synth --context env=dev   # template em infra/cdk.out
```
