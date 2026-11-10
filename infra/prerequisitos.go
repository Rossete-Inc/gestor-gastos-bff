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

// Como os parâmetros de outros repositórios (Prerequisitos) chegam à stack:
//
//   - Na esteira, o app lê o SSM no synth, com as credenciais do job, e grava
//     os valores no template. A leitura no synth é obrigatória na esteira: a
//     role de deploy do bootstrap do CDK só pode ler o parâmetro de versão do
//     bootstrap, então um parâmetro do CloudFormation do tipo
//     AWS::SSM::Parameter::Value<String> falharia no CreateChangeSet com
//     AccessDenied em ssm:GetParameters. É o mesmo tratamento do users-service.
//   - Fora da esteira (synth e testes offline, sem credenciais), cada valor
//     vira um parâmetro do CloudFormation resolvido no deploy
//     (valueForStringParameter).
//
// A mesma leitura verifica os pré-requisitos (spec entrega-continua,
// "Pré-requisitos de deploy"): parâmetro ausente gera uma mensagem dizendo
// qual é e qual repositório o publica, antes de criar qualquer recurso.
//
// O modo vem do contexto `verificarSsm` (ver o ci.yml). Sem o contexto, a
// variável AMBIENTE, que o go-service.yml define no diff e no deploy, liga o
// modo erro.
//   - erro: lê no synth; parâmetro ausente reprova o synth (deploy na main).
//   - aviso: lê no synth; parâmetro ausente vira aviso e a stack volta aos
//     parâmetros do CloudFormation (cdk diff no PR, que não implanta).
//   - vazio: não consulta a AWS (testes e synth local, offline).
const (
	ChaveContextoVerificarSsm = "verificarSsm"
	VerificarErro             = "erro"
	VerificarAviso            = "aviso"

	// VariavelAmbienteEsteira é definida pelo go-service.yml no diff e no deploy.
	VariavelAmbienteEsteira = "AMBIENTE"
)

// Valores guarda o valor de cada parâmetro lido, pela chave relativa
// (ParamAPIID etc.). nil = parâmetros do CloudFormation (synth offline).
type Valores map[string]string

// BuscarParametros devolve os valores dos nomes completos informados que
// existem no SSM. Os ausentes simplesmente não aparecem no mapa.
type BuscarParametros func(ctx context.Context, nomes []string) (map[string]string, error)

// LeitorSSM é o subconjunto do client SSM usado aqui.
type LeitorSSM interface {
	GetParameters(ctx context.Context, in *ssm.GetParametersInput, opt ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

// ParametrosNoSSM consulta o SSM em lotes de 10 (limite do GetParameters).
// Um nome inexistente volta em InvalidParameters, sem erro.
func ParametrosNoSSM(api LeitorSSM) BuscarParametros {
	return func(ctx context.Context, nomes []string) (map[string]string, error) {
		valores := map[string]string{}
		for i := 0; i < len(nomes); i += 10 {
			lote := nomes[i:min(i+10, len(nomes))]
			out, err := api.GetParameters(ctx, &ssm.GetParametersInput{Names: lote})
			if err != nil {
				return nil, err
			}
			for _, p := range out.Parameters {
				if p.Name != nil && p.Value != nil && *p.Value != "" {
					valores[*p.Name] = *p.Value
				}
			}
		}
		return valores, nil
	}
}

// ModoVerificacao lê o contexto `verificarSsm`; sem ele, AMBIENTE definida
// (esteira) vale erro. Valor desconhecido entra em pânico no synth, como o
// contexto `env` inválido.
func ModoVerificacao(app awscdk.App, lookup func(string) (string, bool)) string {
	v, _ := app.Node().TryGetContext(jsii.String(ChaveContextoVerificarSsm)).(string)
	switch v = strings.TrimSpace(v); v {
	case VerificarErro, VerificarAviso:
		return v
	case "":
		if amb, ok := lookup(VariavelAmbienteEsteira); ok && strings.TrimSpace(amb) != "" {
			return VerificarErro
		}
		return ""
	}
	panic(fmt.Sprintf("contexto %s=%q inválido: use %q, %q ou não informe", ChaveContextoVerificarSsm, v, VerificarErro, VerificarAviso))
}

// LerPrerequisitos lê todos os Prerequisitos do ambiente. Devolve os valores
// quando todos existem; caso contrário, valores nil e uma mensagem por
// problema.
func LerPrerequisitos(ctx context.Context, env string, buscar BuscarParametros) (Valores, []string) {
	origem := map[string]string{}
	nomes := make([]string, 0, len(Prerequisitos))
	for _, p := range Prerequisitos {
		nome := CaminhoSSM(env, p.Param)
		origem[nome] = p.Origem
		nomes = append(nomes, nome)
	}

	achados, err := buscar(ctx, nomes)
	if err != nil {
		return nil, []string{fmt.Sprintf("não foi possível ler os pré-requisitos no SSM (%s): %v. "+
			"Confira as credenciais da AWS (na esteira, a role gh-deploy-<repo>-%s ou gh-diff-<repo>) e a permissão "+
			"ssm:GetParameters em %s/*. Para o synth offline, rode sem a variável %s e sem o contexto %s.",
			strings.Join(nomes, ", "), err, env, PrefixoSSM(env), VariavelAmbienteEsteira, ChaveContextoVerificarSsm)}
	}

	var msgs []string
	valores := Valores{}
	for _, p := range Prerequisitos {
		nome := CaminhoSSM(env, p.Param)
		if v, ok := achados[nome]; ok {
			valores[p.Param] = v
			continue
		}
		msgs = append(msgs, fmt.Sprintf("o parâmetro %s não existe no SSM. Ele é publicado pelo %s: "+
			"faça o deploy dele em %s antes do BFF.", nome, origem[nome], env))
	}
	if len(msgs) > 0 {
		sort.Strings(msgs)
		return nil, msgs
	}
	return valores, nil
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
