package main

// Convenções do app CDK do BFF. Os nomes seguem o infra/config.go do
// gestor-gastos-infra-base: mudar um caminho do SSM aqui quebra quem consome.

const (
	// Projeto é o prefixo de stacks, recursos e parâmetros.
	Projeto = "gestor-gastos"

	// Repositorio vai na tag `repositorio` de todos os recursos.
	Repositorio = "gestor-gastos-bff"

	// Regiao é a única região usada pelo sistema.
	Regiao = "us-east-1"

	// ChaveContextoAmbiente é a chave de contexto com o ambiente
	// (`cdk deploy --context env=dev`), a mesma que o go-service.yml passa.
	ChaveContextoAmbiente = "env"

	// AmbientePadrao é usado quando o contexto `env` não é informado.
	AmbientePadrao = "dev"

	// AliasLive é o alias que recebe o tráfego da HTTP API e que o CodeDeploy
	// desloca no canary.
	AliasLive = "live"

	// MemoriaMB e TimeoutSegundos da Lambda: arm64 com 256 MB (design.md,
	// cold start). Os logs ficam 7 dias (regra de dev, ADR 0007).
	MemoriaMB       = 256
	TimeoutSegundos = 10
)

// Ambientes aceitos. Por custo, há um único ambiente na AWS (ADR 0007).
var Ambientes = []string{"dev"}

// Parâmetros do SSM, relativos a PrefixoSSM(env).
const (
	// Lidos do infra-base (stacks auth e api).
	ParamIssuerURL        = "auth/issuer-url"
	ParamUserPoolClientID = "auth/user-pool-client-id"
	ParamJwksURL          = "auth/jwks-url"
	ParamAPIID            = "api/id"
	ParamAuthorizerID     = "api/authorizer-id"

	// Lido do users-service: ARN do alias live da Lambda dele.
	ParamUsersFunctionArn = "users/function-arn"

	// Publicado por este repositório: ARN do alias live do BFF.
	ParamBffFunctionArn = "bff/function-arn"
)

// Prerequisito é um parâmetro do SSM que outro repositório precisa ter
// publicado antes do deploy do BFF.
type Prerequisito struct {
	Param string
	// Origem diz quem publica o parâmetro, para a mensagem de erro.
	Origem string
}

// Prerequisitos são todos os parâmetros que a stack lê. A ordem do primeiro
// deploy é infra-base -> users-service -> BFF (design.md, Migration Plan).
var Prerequisitos = []Prerequisito{
	{ParamIssuerURL, "gestor-gastos-infra-base (stack auth)"},
	{ParamUserPoolClientID, "gestor-gastos-infra-base (stack auth)"},
	{ParamJwksURL, "gestor-gastos-infra-base (stack auth)"},
	{ParamAPIID, "gestor-gastos-infra-base (stack api)"},
	{ParamAuthorizerID, "gestor-gastos-infra-base (stack api)"},
	{ParamUsersFunctionArn, "gestor-gastos-users-service"},
}

// PrefixoSSM devolve o caminho base dos parâmetros de um ambiente, sem barra
// final. Ex.: /gestor-gastos/dev.
func PrefixoSSM(env string) string {
	return "/" + Projeto + "/" + env
}

// CaminhoSSM devolve o caminho completo de um parâmetro do ambiente.
// Ex.: CaminhoSSM("dev", ParamAPIID) = /gestor-gastos/dev/api/id.
func CaminhoSSM(env, param string) string {
	return PrefixoSSM(env) + "/" + param
}

// NomeStack devolve o nome de uma stack ou recurso por ambiente.
// Ex.: gestor-gastos-dev-bff.
func NomeStack(env, nome string) string {
	return Projeto + "-" + env + "-" + nome
}

// Rotas são as rotas do openapi.yaml publicadas na HTTP API do infra-base,
// todas com o authorizer JWT. Rotas explícitas, e não `ANY /{proxy+}`: um
// caminho fora do contrato recebe 404 da própria API, sem invocar a Lambda.
// O teste TestRotasIguaisAoContrato compara esta lista com o openapi.yaml.
var Rotas = []string{
	"GET /me",
	"GET /telas/dashboard",
	"GET /telas/gastos",
	"GET /telas/gasto-form",
	"POST /gastos",
	"PUT /gastos/{id}",
	"DELETE /gastos/{id}",
	"GET /categorias",
	"POST /categorias",
	"DELETE /categorias/{id}",
	"GET /admin/telas/usuarios",
	"PATCH /admin/usuarios/{id}",
	"DELETE /admin/usuarios/{id}",
}
