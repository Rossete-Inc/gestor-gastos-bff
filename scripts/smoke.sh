#!/usr/bin/env bash
# Smoke test do BFF em dev: /me, /categorias e o ciclo de um gasto pela HTTP API pública,
# com um token real do Cognito.
#
# Roda no job de deploy do go-service.yml (input smoke-test), logo depois do
# cdk deploy, com as credenciais da role gh-deploy-gestor-gastos-bff-dev e
# AMBIENTE=dev. O cdk deploy só termina depois que o canary do CodeDeploy
# passou todo o tráfego para a versão nova, então o teste já vê a versão nova.
#
# Passos:
#   1. GET /me sem token: 401 do authorizer JWT (rota publicada e protegida).
#   2. Garante o usuário de smoke test no User Pool (criado sem e-mail de
#      convite), troca a senha por uma aleatória a cada execução e pede um
#      access token com ADMIN_USER_PASSWORD_AUTH. Nenhuma senha fica guardada.
#   3. GET /me com o token: 200 com o perfil do usuário de smoke test (criado
#      sob demanda no primeiro acesso, design.md), o que exercita BFF ->
#      Lambda Invoke -> users-service -> DynamoDB.
#   4. GET /categorias com o token: 200 com as 12 categorias padrão, o que
#      exercita o categories-service e o seed feito no deploy dele.
#   5. Gastos (tarefa 8.5): GET /telas/gastos do mês, POST /gastos, GET
#      /telas/gasto-form?id= e DELETE /gastos/{id}, o que exercita BFF ->
#      expenses-service (com o segredo dos cursores) -> DynamoDB. O gasto de
#      teste é excluído no fim, e um gasto que sobrar de uma execução que
#      falhou some no próximo DELETE do mesmo dia.
#
# O passo 2 depende do infra-base (PR #16): fluxo
# ALLOW_ADMIN_USER_PASSWORD_AUTH no app client e permissões cognito-idp na
# role de deploy do BFF (ver o README). Com SMOKE_EXIGIR_TOKEN=1 (o que o
# ci.yml usa), a falta dessa configuração reprova o smoke test. Sem a
# variável, os passos 2 e 3 são pulados com um aviso e só o passo 1 vale.
#
# Localmente, com credenciais de admin em dev:
#   AMBIENTE=dev bash scripts/smoke.sh
set -euo pipefail

AMBIENTE="${AMBIENTE:-dev}"
REGIAO="${AWS_REGION:-us-east-1}"
PREFIXO="/gestor-gastos/$AMBIENTE"
EMAIL="${SMOKE_EMAIL:-smoke-test@example.com}"
EXIGIR_TOKEN="${SMOKE_EXIGIR_TOKEN:-0}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
chmod 700 "$tmp"

erro() { echo "::error title=Smoke test do BFF::$*"; exit 1; }

# pular encerra com sucesso e aviso quando falta a configuração do passo 2.
pular() {
  if [ "$EXIGIR_TOKEN" = "1" ]; then
    erro "$* (SMOKE_EXIGIR_TOKEN=1)"
  fi
  echo "::warning title=Smoke test do BFF sem token real::$* Só a checagem sem token (401) foi feita."
  exit 0
}

# ler VAR caminho: lê o parâmetro do SSM na variável VAR. Sem subshell, para
# o erro encerrar o script.
ler() {
  if ! aws ssm get-parameter --region "$REGIAO" --name "$PREFIXO/$2" --query Parameter.Value --output text     > "$tmp/param" 2> "$tmp/erro"; then
    erro "não consegui ler $PREFIXO/$2 no SSM: $(cat "$tmp/erro")"
  fi
  printf -v "$1" '%s' "$(cat "$tmp/param")"
}

# enviar METODO URL TOKEN [JSON]: como o get, para POST/PUT/DELETE.
enviar() {
  local args=(-sS -o "$tmp/corpo" -w '%{http_code}' --max-time 30 -X "$1" -H "Authorization: Bearer $3")
  if [ -n "${4:-}" ]; then
    args+=(-H 'Content-Type: application/json' --data "$4")
  fi
  curl "${args[@]}" "$2" || true
}

# get URL [TOKEN]: grava o corpo em $tmp/corpo e imprime o status HTTP.
get() {
  local args=(-sS -o "$tmp/corpo" -w '%{http_code}' --max-time 30)
  if [ -n "${2:-}" ]; then
    args+=(-H "Authorization: Bearer $2")
  fi
  # Em falha de rede o curl já imprime 000.
  curl "${args[@]}" "$1" || true
}

ler API_URL api/url
ler POOL_ID auth/user-pool-id
ler CLIENT_ID auth/user-pool-client-id
echo "API: $API_URL"

# 1. Sem token: 401. Tenta por até ~1 min, porque o stage $default publica
#    as rotas novas com auto deploy, que pode levar alguns segundos.
status=000
for tentativa in $(seq 12); do
  status=$(get "$API_URL/me")
  [ "$status" = "401" ] && break
  echo "GET /me sem token: $status (tentativa $tentativa); aguardando..."
  sleep 5
done
[ "$status" = "401" ] || erro "GET /me sem token devolveu $status, esperava 401: $(head -c 500 "$tmp/corpo")"
echo "GET /me sem token: 401 (ok)"

# 2. Usuário e token do smoke test.
# cognito: roda o aws cognito-idp, com a saída em $tmp/saida, e decide o que
# fazer com o erro. Sem subshell, para pular e erro encerrarem o script.
cognito() {
  if aws cognito-idp "$@" --region "$REGIAO" > "$tmp/saida" 2> "$tmp/erro"; then
    return 0
  fi
  local saida
  saida=$(cat "$tmp/erro")
  case "$saida" in
    *UserNotFoundException*) return 2 ;;
    *AccessDenied*|*"not authorized to perform"*)
      pular "a role de deploy não tem permissão para $1 no User Pool (mudança pendente no infra-base)." ;;
    *"Auth flow not enabled for this client"*|*"ADMIN_USER_PASSWORD_AUTH"*)
      pular "o app client não aceita ADMIN_USER_PASSWORD_AUTH (mudança pendente no infra-base)." ;;
  esac
  erro "aws cognito-idp $1 falhou: $saida"
}

rc=0
cognito admin-get-user --user-pool-id "$POOL_ID" --username "$EMAIL" || rc=$?
if [ "$rc" = "2" ]; then
  echo "Criando o usuário de smoke test $EMAIL (sem e-mail de convite)"
  cognito admin-create-user --user-pool-id "$POOL_ID" --username "$EMAIL" --message-action SUPPRESS \
    --user-attributes Name=email,Value="$EMAIL" Name=email_verified,Value=true Name=name,Value="Smoke Test"
fi

# Senha nova a cada execução (maiúscula, minúscula e número, como exige o pool).
senha="Sm0ke-$(openssl rand -hex 16)"
echo "::add-mask::$senha"
umask 077
printf '{"UserPoolId":"%s","Username":"%s","Password":"%s","Permanent":true}' "$POOL_ID" "$EMAIL" "$senha" > "$tmp/senha.json"
cognito admin-set-user-password --cli-input-json "file://$tmp/senha.json"

printf '{"UserPoolId":"%s","ClientId":"%s","AuthFlow":"ADMIN_USER_PASSWORD_AUTH","AuthParameters":{"USERNAME":"%s","PASSWORD":"%s"}}' \
  "$POOL_ID" "$CLIENT_ID" "$EMAIL" "$senha" > "$tmp/auth.json"
cognito admin-initiate-auth --cli-input-json "file://$tmp/auth.json" --query AuthenticationResult.AccessToken --output text
token=$(cat "$tmp/saida")
[ -n "$token" ] && [ "$token" != "None" ] || erro "admin-initiate-auth não devolveu access token"
echo "::add-mask::$token"

# 3. Com token: 200 com o perfil do usuário de smoke test.
status=$(get "$API_URL/me" "$token")
[ "$status" = "200" ] || erro "GET /me com token devolveu $status, esperava 200: $(head -c 500 "$tmp/corpo")"
jq -e --arg email "$EMAIL" '.email == $email and (.id | length > 0) and .papel == "cliente"' "$tmp/corpo" > /dev/null \
  || erro "GET /me devolveu um perfil inesperado: $(head -c 500 "$tmp/corpo")"
echo "GET /me com token real: 200, perfil de $EMAIL (ok)"

# 4. Categorias (tarefa 7.5): 200 com as 12 padrão, 6 em cada grupo. Exercita
#    BFF -> Lambda Invoke -> categories-service -> DynamoDB e o seed do deploy.
status=$(get "$API_URL/categorias" "$token")
[ "$status" = "200" ] || erro "GET /categorias com token devolveu $status, esperava 200: $(head -c 500 "$tmp/corpo")"
jq -e '([.CARTAO[] | select(.padrao)] | length) == 6 and ([.FIXO[] | select(.padrao)] | length) == 6' "$tmp/corpo" > /dev/null \
  || erro "GET /categorias não trouxe as 12 categorias padrão (6 + 6). O seed do categories-service rodou? $(head -c 500 "$tmp/corpo")"
echo "GET /categorias com token real: 200, 12 categorias padrão (ok)"

# 5. Gastos (tarefa 8.5): lista do mês, lança, abre e exclui um gasto de teste.
hoje=$(date -u +%Y-%m-%d)
mes=${hoje:0:7}
status=$(get "$API_URL/telas/gastos?mes=$mes" "$token")
[ "$status" = "200" ] || erro "GET /telas/gastos devolveu $status, esperava 200: $(head -c 500 "$tmp/corpo")"
jq -e --arg mes "$mes" '.mes == $mes and (.itens | type == "array") and (.categorias.CARTAO | length >= 6)' "$tmp/corpo" > /dev/null \
  || erro "GET /telas/gastos devolveu uma tela inesperada: $(head -c 500 "$tmp/corpo")"
echo "GET /telas/gastos?mes=$mes: 200 (ok)"

status=$(enviar POST "$API_URL/gastos" "$token" \
  "{\"valorCentavos\":1,\"data\":\"$hoje\",\"categoriaId\":\"alimentacao\",\"descricao\":\"smoke test\"}")
[ "$status" = "201" ] || erro "POST /gastos devolveu $status, esperava 201: $(head -c 500 "$tmp/corpo")"
jq -e '.grupo == "CARTAO" and .categoria.id == "alimentacao" and .valorCentavos == 1' "$tmp/corpo" > /dev/null \
  || erro "POST /gastos devolveu um gasto inesperado: $(head -c 500 "$tmp/corpo")"
gasto=$(jq -r .id "$tmp/corpo")
echo "POST /gastos: 201, gasto $gasto no grupo CARTAO (ok)"

status=$(get "$API_URL/telas/gasto-form?id=$gasto" "$token")
[ "$status" = "200" ] || erro "GET /telas/gasto-form?id= devolveu $status, esperava 200: $(head -c 500 "$tmp/corpo")"
jq -e --arg id "$gasto" '.gasto.id == $id' "$tmp/corpo" > /dev/null \
  || erro "GET /telas/gasto-form não trouxe o gasto $gasto: $(head -c 500 "$tmp/corpo")"
echo "GET /telas/gasto-form?id=: 200 (ok)"

status=$(enviar DELETE "$API_URL/gastos/$gasto" "$token")
[ "$status" = "204" ] || erro "DELETE /gastos/$gasto devolveu $status, esperava 204: $(head -c 500 "$tmp/corpo")"
status=$(get "$API_URL/telas/gasto-form?id=$gasto" "$token")
[ "$status" = "404" ] || erro "o gasto $gasto ainda existe depois do DELETE (status $status)"
echo "DELETE /gastos/{id}: 204, e o gasto não existe mais (ok)"
