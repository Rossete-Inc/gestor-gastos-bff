// Command lambda é o BFF na AWS: a Lambda atrás da HTTP API (payload 2.0).
// Usa o mesmo handler do cmd/local (internal/api) e chama o users-service
// por Lambda Invoke (ADR 0004).
//
// O binário segue a convenção do go-service.yml: dist/lambda/bootstrap
// (linux/arm64, provided.al2023). As variáveis de ambiente são definidas
// pelo CDK em infra/, com valores lidos do SSM.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/api"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/cognito"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/lambdahttp"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/me"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/usuarios"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
)

const servico = "bff"

// Variáveis de ambiente definidas pelo CDK (infra/bff.go).
const (
	VarCognitoIssuer   = "COGNITO_ISSUER"
	VarCognitoClientID = "COGNITO_CLIENT_ID"
	VarCognitoJWKSURL  = "COGNITO_JWKS_URL"
	VarUsersFunction   = "USERS_FUNCTION_ARN"
)

// Config é a configuração da Lambda.
type Config struct {
	Base            config.Base
	CognitoIssuer   string
	CognitoClientID string
	CognitoJWKSURL  string
	// UsersFunction é o ARN do alias live do users-service.
	UsersFunction string
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=aws.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if base.IsLocal() {
		return Config{}, errors.New("cmd/lambda exige RUNTIME=aws; localmente use o cmd/local")
	}
	r := config.NewReader(lookup)
	c := Config{
		Base:            base,
		CognitoIssuer:   r.Require(VarCognitoIssuer),
		CognitoClientID: r.Require(VarCognitoClientID),
		CognitoJWKSURL:  r.Get(VarCognitoJWKSURL, ""),
		UsersFunction:   r.Require(VarUsersFunction),
	}
	if err := r.Err(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// NewHandler monta o BFF com os clients da AWS.
func NewHandler(c Config, awsCfg aws.Config) (http.Handler, error) {
	log := logging.New(logging.Options{Service: servico, Env: c.Base.Env, Level: c.Base.LogLevel})

	tokens, err := auth.NewValidator(auth.Config{Issuer: c.CognitoIssuer, ClientID: c.CognitoClientID, JWKSURL: c.CognitoJWKSURL})
	if err != nil {
		return nil, err
	}
	transporte, err := invoke.NewTransport(c.Base.Runtime, c.UsersFunction, awslambda.NewFromConfig(awsCfg), nil)
	if err != nil {
		return nil, err
	}

	// CORS é feito pela HTTP API (infra-base), por isso sem CORSOrigins aqui.
	// Sem Categorias até a tarefa 7.6 (deploy do categories-service): as rotas
	// de categorias respondem 501 em dev.
	return api.New(api.Deps{
		Tokens: tokens,
		Me:     me.New(usuarios.New(transporte), cognito.New(cip.NewFromConfig(awsCfg))),
		Log:    log,
	}), nil
}

func main() {
	if err := run(context.Background(), os.LookupEnv, func(f lambdahttp.Func) { lambda.Start(f) }); err != nil {
		// Falha na inicialização: a Lambda sai com erro, o que conta no alarme
		// de erros do alias e faz o CodeDeploy reverter o canary.
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupFunc, start func(lambdahttp.Func)) error {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return err
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Base.Region))
	if err != nil {
		return fmt.Errorf("configuração da AWS: %w", err)
	}
	h, err := NewHandler(cfg, awsCfg)
	if err != nil {
		return err
	}
	start(lambdahttp.Handler(h))
	return nil
}
