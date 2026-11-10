package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/jsii-runtime-go"
)

// Verificação dos pré-requisitos no SSM (spec entrega-continua, "Pré-requisitos
// de deploy"): o deploy falha com mensagem clara, antes de criar qualquer
// recurso, quando um parâmetro de outro repositório ainda não existe.
//
// Sem a verificação, o CloudFormation também recusaria o change set, mas com
// uma mensagem genérica do tipo "Unable to fetch parameters [...]". A
// verificação diz qual parâmetro falta e qual repositório o publica.
//
// Ela é ligada pelo contexto `verificarSsm` (ver o ci.yml):
//   - erro: parâmetro ausente reprova o synth (deploy na main).
//   - aviso: parâmetro ausente vira aviso (cdk diff no PR, que não implanta).
//   - vazio: não consulta a AWS (testes e synth local, offline).
const (
	ChaveContextoVerificarSsm = "verificarSsm"
	VerificarErro             = "erro"
	VerificarAviso            = "aviso"
)

// BuscarAusentes devolve, dos nomes completos informados, os que não existem
// no SSM.
type BuscarAusentes func(ctx context.Context, nomes []string) ([]string, error)

// LeitorSSM é o subconjunto do client SSM usado aqui.
type LeitorSSM interface {
	GetParameters(ctx context.Context, in *ssm.GetParametersInput, opt ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

// AusentesNoSSM consulta o SSM em lotes de 10 (limite do GetParameters).
// Um nome inexistente volta em InvalidParameters, sem erro.
func AusentesNoSSM(api LeitorSSM) BuscarAusentes {
	return func(ctx context.Context, nomes []string) ([]string, error) {
		var ausentes []string
		for i := 0; i < len(nomes); i += 10 {
			lote := nomes[i:min(i+10, len(nomes))]
			out, err := api.GetParameters(ctx, &ssm.GetParametersInput{Names: lote})
			if err != nil {
				return nil, err
			}
			ausentes = append(ausentes, out.InvalidParameters...)
		}
		sort.Strings(ausentes)
		return ausentes, nil
	}
}

// ModoVerificacao lê o contexto `verificarSsm`. Valor desconhecido entra em
// pânico no synth, como o contexto `env` inválido.
func ModoVerificacao(app awscdk.App) string {
	v, _ := app.Node().TryGetContext(jsii.String(ChaveContextoVerificarSsm)).(string)
	switch v = strings.TrimSpace(v); v {
	case "", VerificarErro, VerificarAviso:
		return v
	}
	panic(fmt.Sprintf("contexto %s=%q inválido: use %q, %q ou não informe", ChaveContextoVerificarSsm, v, VerificarErro, VerificarAviso))
}

// MensagensPrerequisitos consulta os pré-requisitos do ambiente e devolve uma
// mensagem por problema (vazio = tudo certo).
func MensagensPrerequisitos(ctx context.Context, env string, buscar BuscarAusentes) []string {
	origem := map[string]string{}
	nomes := make([]string, 0, len(Prerequisitos))
	for _, p := range Prerequisitos {
		nome := CaminhoSSM(env, p.Param)
		origem[nome] = p.Origem
		nomes = append(nomes, nome)
	}

	ausentes, err := buscar(ctx, nomes)
	if err != nil {
		return []string{fmt.Sprintf("não foi possível ler os pré-requisitos no SSM (%s): %v. "+
			"Confira as credenciais da AWS e a permissão ssm:GetParameters em %s/*.",
			strings.Join(nomes, ", "), err, PrefixoSSM(env))}
	}
	msgs := make([]string, 0, len(ausentes))
	for _, nome := range ausentes {
		msgs = append(msgs, fmt.Sprintf("o parâmetro %s não existe no SSM. Ele é publicado pelo %s: "+
			"faça o deploy dele em %s antes do BFF.", nome, origemOuDesconhecida(origem[nome]), env))
	}
	return msgs
}

func origemOuDesconhecida(o string) string {
	if o == "" {
		return "repositório dono do parâmetro"
	}
	return o
}

// anotarPrerequisitos registra as mensagens na stack como erro (o synth e o
// deploy falham antes de criar recursos) ou como aviso.
func anotarPrerequisitos(stack awscdk.Stack, modo string, msgs []string) {
	for _, m := range msgs {
		m = "pré-requisito de deploy: " + m
		if modo == VerificarErro {
			awscdk.Annotations_Of(stack).AddError(jsii.String(m))
		} else {
			awscdk.Annotations_Of(stack).AddWarning(jsii.String(m))
		}
	}
}
