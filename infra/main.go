// Command infra é o app AWS CDK (Go) do BFF.
//
// Uma stack por ambiente (contexto `env`, padrão dev): gestor-gastos-<env>-bff,
// com a Lambda do BFF (arm64, 256 MB, alias live, canary do CodeDeploy, logs
// por 7 dias), as rotas do contrato na HTTP API do infra-base e o ARN do alias
// publicado no SSM. Tudo o que vem de outros repositórios é lido do SSM.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// Opcoes são as dependências do app, separadas de main para os testes.
type Opcoes struct {
	// DirLambda é a pasta do bootstrap (DirLambda).
	DirLambda string
	// Buscar consulta o SSM; só é chamado com o contexto verificarSsm ligado.
	Buscar BuscarAusentes
}

// NovoApp monta o app e devolve a stack do BFF.
func NovoApp(app awscdk.App, o Opcoes) awscdk.Stack {
	env := Ambiente(app)
	modo := ModoVerificacao(app)

	awscdk.Tags_Of(app).Add(jsii.String("projeto"), jsii.String(Projeto), nil)
	awscdk.Tags_Of(app).Add(jsii.String("repositorio"), jsii.String(Repositorio), nil)
	awscdk.Tags_Of(app).Add(jsii.String("ambiente"), jsii.String(env), nil)

	stack := NovaStackBff(app, NomeStack(env, "bff"), &BffProps{
		StackProps: awscdk.StackProps{Env: ambienteAws()},
		Ambiente:   env,
		Codigo:     CodigoLambda(o.DirLambda),
	})

	if modo != "" {
		anotarPrerequisitos(stack, modo, MensagensPrerequisitos(context.Background(), env, o.Buscar))
	}
	return stack
}

// Ambiente lê o contexto `env` (padrão dev) e aceita só os de Ambientes.
func Ambiente(scope constructs.Construct) string {
	env := AmbientePadrao
	if v, ok := scope.Node().TryGetContext(jsii.String(ChaveContextoAmbiente)).(string); ok && v != "" {
		env = v
	}
	if !slices.Contains(Ambientes, env) {
		panic(fmt.Sprintf("ambiente %q inválido: use um de %v (contexto %s)", env, Ambientes, ChaveContextoAmbiente))
	}
	return env
}

// ambienteAws fixa a região em us-east-1. A conta vem das credenciais do CLI
// (CDK_DEFAULT_ACCOUNT) quando houver; sem credenciais a stack fica agnóstica de
// conta e as ARNs usam ${AWS::AccountId}, o que permite synth e testes offline.
func ambienteAws() *awscdk.Environment {
	env := &awscdk.Environment{Region: jsii.String(Regiao)}
	if conta := os.Getenv("CDK_DEFAULT_ACCOUNT"); conta != "" {
		env.Account = jsii.String(conta)
	}
	return env
}

// buscarNoSSM cria o client SSM só quando a verificação roda, com as
// credenciais padrão (as da esteira).
func buscarNoSSM(ctx context.Context, nomes []string) ([]string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(Regiao))
	if err != nil {
		return nil, err
	}
	return AusentesNoSSM(ssm.NewFromConfig(cfg))(ctx, nomes)
}

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)
	NovoApp(app, Opcoes{DirLambda: DirLambda, Buscar: buscarNoSSM})
	app.Synth(nil)
}
