package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/jsii-runtime-go"
)

// Testes de assertion da stack do BFF. Rodam sem AWS e sem compilar a
// Lambda: o bootstrap é um arquivo falso e o SSM, quando lido no synth, é falso.

const stackTeste = "gestor-gastos-dev-bff"

// dirLambdaFake cria uma pasta com um bootstrap falso.
func dirLambdaFake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// valorFalso é o valor que o SSM falso devolve para cada caminho.
func valorFalso(nome string) string {
	if strings.HasSuffix(nome, ParamUsersFunctionArn) {
		return "arn:aws:lambda:us-east-1:123456789012:function:gestor-gastos-dev-users:live"
	}
	return "valor:" + nome
}

// ssmFalso devolve todos os nomes pedidos, menos os ausentes.
func ssmFalso(ausentes ...string) BuscarParametros {
	return func(_ context.Context, nomes []string) (map[string]string, error) {
		m := map[string]string{}
		for _, n := range nomes {
			if !slices.Contains(ausentes, n) {
				m[n] = valorFalso(n)
			}
		}
		return m, nil
	}
}

func appTeste(t *testing.T, contexto map[string]interface{}, o Opcoes) awscdk.Stack {
	t.Helper()
	if o.DirLambda == "" {
		o.DirLambda = dirLambdaFake(t)
	}
	if o.Buscar == nil {
		o.Buscar = func(context.Context, []string) (map[string]string, error) {
			t.Error("o SSM não deveria ser consultado fora da esteira")
			return nil, nil
		}
	}
	app := awscdk.NewApp(&awscdk.AppProps{Context: &contexto})
	return NovoApp(app, o)
}

func templateTeste(t *testing.T) assertions.Template {
	t.Helper()
	return assertions.Template_FromStack(appTeste(t, map[string]interface{}{}, Opcoes{}), nil)
}

// paramSSM devolve o id lógico do parâmetro do CloudFormation que lê o
// caminho do SSM no deploy (valueForStringParameter).
func paramSSM(t *testing.T, tpl assertions.Template, param string) string {
	t.Helper()
	caminho := CaminhoSSM("dev", param)
	for id, p := range *tpl.FindParameters(jsii.String("*"), nil) {
		m := *p
		if m["Default"] == caminho && m["Type"] == "AWS::SSM::Parameter::Value<String>" {
			return id
		}
	}
	t.Fatalf("parâmetro do SSM %s não encontrado no template", caminho)
	return ""
}

func ref(id string) map[string]interface{} { return map[string]interface{}{"Ref": id} }

// unico devolve o id lógico e as propriedades do único recurso do tipo.
func unico(t *testing.T, tpl assertions.Template, tipo string) (string, map[string]interface{}) {
	t.Helper()
	recursos := *tpl.FindResources(jsii.String(tipo), nil)
	if len(recursos) != 1 {
		t.Fatalf("esperava 1 recurso %s, achei %d", tipo, len(recursos))
	}
	for id, r := range recursos {
		props, _ := (*r)["Properties"].(map[string]interface{})
		return id, props
	}
	return "", nil
}

func jsonDe(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLambdaArm64Com256MB(t *testing.T) {
	tpl := templateTeste(t)
	tpl.ResourceCountIs(jsii.String("AWS::Lambda::Function"), jsii.Number(1))
	tpl.HasResourceProperties(jsii.String("AWS::Lambda::Function"), map[string]interface{}{
		"FunctionName":  "gestor-gastos-dev-bff",
		"Runtime":       "provided.al2023",
		"Architectures": []interface{}{"arm64"},
		"Handler":       "bootstrap",
		"MemorySize":    256,
		"Timeout":       10,
		"Environment": map[string]interface{}{"Variables": map[string]interface{}{
			"RUNTIME":            "aws",
			"ENV":                "dev",
			"LOG_LEVEL":          "info",
			"COGNITO_ISSUER":     ref(paramSSM(t, tpl, ParamIssuerURL)),
			"COGNITO_CLIENT_ID":  ref(paramSSM(t, tpl, ParamUserPoolClientID)),
			"COGNITO_JWKS_URL":   ref(paramSSM(t, tpl, ParamJwksURL)),
			"USERS_FUNCTION_ARN": ref(paramSSM(t, tpl, ParamUsersFunctionArn)),
		}},
	})
	// Sem VPC: custo zero parado e sem NAT.
	_, props := unico(t, tpl, "AWS::Lambda::Function")
	if _, ok := props["VpcConfig"]; ok {
		t.Error("a Lambda não deveria estar em VPC")
	}
}

func TestLogsPorSeteDias(t *testing.T) {
	tpl := templateTeste(t)
	tpl.HasResource(jsii.String("AWS::Logs::LogGroup"), map[string]interface{}{
		"Properties": map[string]interface{}{
			"LogGroupName":    "/aws/lambda/gestor-gastos-dev-bff",
			"RetentionInDays": 7,
		},
		"DeletionPolicy": "Delete",
	})
}

func TestAliasLiveComCanary(t *testing.T) {
	tpl := templateTeste(t)
	aliasID, _ := unico(t, tpl, "AWS::Lambda::Alias")
	tpl.HasResource(jsii.String("AWS::Lambda::Alias"), map[string]interface{}{
		"Properties": map[string]interface{}{"Name": "live"},
		"UpdatePolicy": map[string]interface{}{"CodeDeployLambdaAliasUpdate": map[string]interface{}{
			"ApplicationName":     assertions.Match_AnyValue(),
			"DeploymentGroupName": assertions.Match_AnyValue(),
		}},
	})
	tpl.ResourceCountIs(jsii.String("AWS::Lambda::Version"), jsii.Number(1))

	tpl.HasResourceProperties(jsii.String("AWS::CodeDeploy::Application"), map[string]interface{}{
		"ApplicationName": "gestor-gastos-dev-bff",
		"ComputePlatform": "Lambda",
	})
	alarmeID, _ := unico(t, tpl, "AWS::CloudWatch::Alarm")
	tpl.HasResourceProperties(jsii.String("AWS::CodeDeploy::DeploymentGroup"), map[string]interface{}{
		"DeploymentGroupName":  "gestor-gastos-dev-bff",
		"DeploymentConfigName": "CodeDeployDefault.LambdaCanary10Percent5Minutes",
		"AutoRollbackConfiguration": map[string]interface{}{
			"Enabled": true,
			"Events": assertions.Match_ArrayWith(&[]interface{}{
				"DEPLOYMENT_FAILURE", "DEPLOYMENT_STOP_ON_REQUEST", "DEPLOYMENT_STOP_ON_ALARM",
			}),
		},
		"AlarmConfiguration": map[string]interface{}{
			"Enabled": true,
			"Alarms":  []interface{}{map[string]interface{}{"Name": ref(alarmeID)}},
		},
	})

	// O alarme olha os erros do alias.
	tpl.HasResourceProperties(jsii.String("AWS::CloudWatch::Alarm"), map[string]interface{}{
		"AlarmName":          "gestor-gastos-dev-bff-erros",
		"MetricName":         "Errors",
		"Namespace":          "AWS/Lambda",
		"Statistic":          "Sum",
		"Period":             60,
		"Threshold":          1,
		"EvaluationPeriods":  1,
		"ComparisonOperator": "GreaterThanOrEqualToThreshold",
		"TreatMissingData":   "notBreaching",
	})
	_, alarme := unico(t, tpl, "AWS::CloudWatch::Alarm")
	if !strings.Contains(jsonDe(t, alarme["Dimensions"]), aliasID) && !strings.Contains(jsonDe(t, alarme["Dimensions"]), ":live") {
		t.Errorf("dimensões do alarme não citam o alias: %s", jsonDe(t, alarme["Dimensions"]))
	}
}

func TestRotasNaHttpApiDoInfraBase(t *testing.T) {
	tpl := templateTeste(t)
	apiID := paramSSM(t, tpl, ParamAPIID)
	authorizerID := paramSSM(t, tpl, ParamAuthorizerID)
	aliasID, _ := unico(t, tpl, "AWS::Lambda::Alias")

	// Só recursos novos na API existente: nada de API, stage ou authorizer.
	for _, tipo := range []string{"AWS::ApiGatewayV2::Api", "AWS::ApiGatewayV2::Stage", "AWS::ApiGatewayV2::Authorizer"} {
		tpl.ResourceCountIs(jsii.String(tipo), jsii.Number(0))
	}

	integracaoID, _ := unico(t, tpl, "AWS::ApiGatewayV2::Integration")
	tpl.HasResourceProperties(jsii.String("AWS::ApiGatewayV2::Integration"), map[string]interface{}{
		"ApiId":                ref(apiID),
		"IntegrationType":      "AWS_PROXY",
		"IntegrationUri":       ref(aliasID),
		"PayloadFormatVersion": "2.0",
	})

	rotas := *tpl.FindResources(jsii.String("AWS::ApiGatewayV2::Route"), nil)
	if len(rotas) != len(Rotas) {
		t.Fatalf("esperava %d rotas, achei %d", len(Rotas), len(rotas))
	}
	var chaves []string
	for id, r := range rotas {
		p := (*r)["Properties"].(map[string]interface{})
		chaves = append(chaves, p["RouteKey"].(string))
		if p["AuthorizationType"] != "JWT" || jsonDe(t, p["AuthorizerId"]) != jsonDe(t, ref(authorizerID)) {
			t.Errorf("%s: rota sem o authorizer JWT do SSM: %v", id, p)
		}
		if jsonDe(t, p["ApiId"]) != jsonDe(t, ref(apiID)) || !strings.Contains(jsonDe(t, p["Target"]), integracaoID) {
			t.Errorf("%s: rota fora da API ou da integração: %v", id, p)
		}
	}
	slices.Sort(chaves)
	esperadas := slices.Sorted(slices.Values(Rotas))
	if !slices.Equal(chaves, esperadas) {
		t.Errorf("rotas = %v, queria %v", chaves, esperadas)
	}
	if slices.ContainsFunc(chaves, func(c string) bool { return strings.Contains(c, "/infra/") }) {
		t.Error("a rota /infra/auth-check é do infra-base")
	}

	// A HTTP API pode invocar só o alias, a partir desta API.
	_, permissao := unico(t, tpl, "AWS::Lambda::Permission")
	if permissao["Principal"] != "apigateway.amazonaws.com" || permissao["Action"] != "lambda:InvokeFunction" ||
		jsonDe(t, permissao["FunctionName"]) != jsonDe(t, ref(aliasID)) {
		t.Errorf("permissão = %v", permissao)
	}
	if fonte := jsonDe(t, permissao["SourceArn"]); !strings.Contains(fonte, `{"Ref":"`+apiID+`"}`) || !strings.Contains(fonte, "/*/*") {
		t.Errorf("SourceArn = %s", fonte)
	}
}

func TestInvocaSoOUsersService(t *testing.T) {
	tpl := templateTeste(t)
	usersID := paramSSM(t, tpl, ParamUsersFunctionArn)
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), map[string]interface{}{
		"PolicyDocument": map[string]interface{}{
			"Statement": []interface{}{map[string]interface{}{
				"Sid":      "InvocarUsersService",
				"Action":   "lambda:InvokeFunction",
				"Effect":   "Allow",
				"Resource": ref(usersID),
			}},
		},
	})
}

func TestPublicaOArnDoAliasNoSSM(t *testing.T) {
	tpl := templateTeste(t)
	aliasID, _ := unico(t, tpl, "AWS::Lambda::Alias")
	tpl.ResourceCountIs(jsii.String("AWS::SSM::Parameter"), jsii.Number(1))
	tpl.HasResourceProperties(jsii.String("AWS::SSM::Parameter"), map[string]interface{}{
		"Name":  "/gestor-gastos/dev/bff/function-arn",
		"Type":  "String",
		"Tier":  "Standard",
		"Value": ref(aliasID),
	})
}

func TestLeTodosOsPrerequisitosDoSSM(t *testing.T) {
	tpl := templateTeste(t)
	for _, p := range Prerequisitos {
		paramSSM(t, tpl, p.Param)
	}
	// Nada de exports nem lookups.
	if s := jsonDe(t, tpl.ToJSON()); strings.Contains(s, "Fn::ImportValue") || strings.Contains(s, `"Export"`) {
		t.Error("o template não deveria usar exports")
	}
}

func TestSemBinarioFalhaComMensagemClara(t *testing.T) {
	stack := appTeste(t, map[string]interface{}{}, Opcoes{DirLambda: filepath.Join(t.TempDir(), "nao-existe")})
	assertions.Annotations_FromStack(stack).HasError(jsii.String("/"+stackTeste), jsii.String(MsgFaltaBuild))
}

func TestPrerequisitoAusenteReprovaODeploy(t *testing.T) {
	var consultados []string
	falso := ssmFalso("/gestor-gastos/dev/users/function-arn")
	buscar := func(ctx context.Context, nomes []string) (map[string]string, error) {
		consultados = nomes
		return falso(ctx, nomes)
	}
	stack := appTeste(t, map[string]interface{}{"verificarSsm": "erro"}, Opcoes{Buscar: buscar})

	if len(consultados) != len(Prerequisitos) || !slices.Contains(consultados, "/gestor-gastos/dev/api/authorizer-id") {
		t.Errorf("consultados = %v", consultados)
	}
	anot := assertions.Annotations_FromStack(stack)
	anot.HasError(jsii.String("/"+stackTeste), assertions.Match_StringLikeRegexp(jsii.String(
		"/gestor-gastos/dev/users/function-arn não existe no SSM.*gestor-gastos-users-service")))
	anot.HasNoWarning(jsii.String("*"), assertions.Match_AnyValue())
}

func TestEsteiraSemContextoValeErro(t *testing.T) {
	lookup := func(k string) (string, bool) { return "dev", k == VariavelAmbienteEsteira }
	stack := appTeste(t, map[string]interface{}{}, Opcoes{Buscar: ssmFalso("/gestor-gastos/dev/api/id"), Lookup: lookup})
	assertions.Annotations_FromStack(stack).HasError(jsii.String("/"+stackTeste),
		assertions.Match_StringLikeRegexp(jsii.String("/gestor-gastos/dev/api/id não existe no SSM.*gestor-gastos-infra-base")))
}

func TestPrerequisitoAusenteNoPrViraAviso(t *testing.T) {
	stack := appTeste(t, map[string]interface{}{"verificarSsm": "aviso"},
		Opcoes{Buscar: ssmFalso("/gestor-gastos/dev/users/function-arn")})
	anot := assertions.Annotations_FromStack(stack)
	anot.HasWarning(jsii.String("/"+stackTeste), assertions.Match_StringLikeRegexp(jsii.String("users/function-arn")))
	anot.HasNoError(jsii.String("*"), assertions.Match_AnyValue())
	// Sem todos os valores, a stack volta aos parâmetros do CloudFormation.
	paramSSM(t, assertions.Template_FromStack(stack, nil), ParamUsersFunctionArn)
}

// Na esteira, os valores lidos no synth vão direto para o template: a role
// de deploy do bootstrap não consegue resolver parâmetros do SSM.
func TestNaEsteiraGravaOsValoresNoTemplate(t *testing.T) {
	stack := appTeste(t, map[string]interface{}{"verificarSsm": "erro"}, Opcoes{Buscar: ssmFalso()})
	assertions.Annotations_FromStack(stack).HasNoError(jsii.String("*"), assertions.Match_AnyValue())
	tpl := assertions.Template_FromStack(stack, nil)
	v := func(param string) string { return valorFalso(CaminhoSSM("dev", param)) }

	// Só sobra o parâmetro de versão do bootstrap, que a role de deploy lê.
	for id, p := range *tpl.FindParameters(jsii.String("*"), nil) {
		if d, _ := (*p)["Default"].(string); strings.HasPrefix(d, PrefixoSSM("dev")) {
			t.Errorf("parâmetro do CloudFormation %s ainda lê %s no deploy", id, d)
		}
	}
	tpl.HasResourceProperties(jsii.String("AWS::Lambda::Function"), map[string]interface{}{
		"Environment": map[string]interface{}{"Variables": map[string]interface{}{
			"COGNITO_ISSUER":     v(ParamIssuerURL),
			"COGNITO_CLIENT_ID":  v(ParamUserPoolClientID),
			"COGNITO_JWKS_URL":   v(ParamJwksURL),
			"USERS_FUNCTION_ARN": v(ParamUsersFunctionArn),
		}},
	})
	tpl.HasResourceProperties(jsii.String("AWS::ApiGatewayV2::Route"), map[string]interface{}{
		"RouteKey":          "GET /me",
		"ApiId":             v(ParamAPIID),
		"AuthorizationType": "JWT",
		"AuthorizerId":      v(ParamAuthorizerID),
	})
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), map[string]interface{}{
		"PolicyDocument": map[string]interface{}{
			"Statement": []interface{}{map[string]interface{}{
				"Sid": "InvocarUsersService", "Action": "lambda:InvokeFunction", "Effect": "Allow",
				"Resource": v(ParamUsersFunctionArn),
			}},
		},
	})
}

func TestFalhaAoLerOSSM(t *testing.T) {
	buscar := func(context.Context, []string) (map[string]string, error) { return nil, errors.New("AccessDenied") }
	stack := appTeste(t, map[string]interface{}{"verificarSsm": "erro"}, Opcoes{Buscar: buscar})
	assertions.Annotations_FromStack(stack).HasError(jsii.String("/"+stackTeste),
		assertions.Match_StringLikeRegexp(jsii.String("não foi possível ler os pré-requisitos.*AccessDenied")))
}

func TestContextoInvalido(t *testing.T) {
	casos := map[string]map[string]interface{}{
		"ambiente":     {"env": "prod"},
		"verificarSsm": {"verificarSsm": "talvez"},
	}
	for nome, ctx := range casos {
		t.Run(nome, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("esperava panic")
				}
			}()
			appTeste(t, ctx, Opcoes{})
		})
	}
}

type fakeSSM struct {
	existentes map[string]bool
	chamadas   int
	err        error
}

func (f *fakeSSM) GetParameters(_ context.Context, in *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	f.chamadas++
	if len(in.Names) > 10 {
		return nil, fmt.Errorf("lote de %d nomes", len(in.Names))
	}
	if f.err != nil {
		return nil, f.err
	}
	out := &ssm.GetParametersOutput{}
	for _, n := range in.Names {
		if f.existentes[n] {
			out.Parameters = append(out.Parameters, ssmtypes.Parameter{Name: jsii.String(n), Value: jsii.String("v" + n)})
		} else {
			out.InvalidParameters = append(out.InvalidParameters, n)
		}
	}
	return out, nil
}

func TestParametrosNoSSM(t *testing.T) {
	var nomes []string
	existentes := map[string]bool{}
	for i := range 12 {
		n := fmt.Sprintf("/p/%02d", i)
		nomes = append(nomes, n)
		existentes[n] = i%5 != 0
	}
	f := &fakeSSM{existentes: existentes}
	valores, err := ParametrosNoSSM(f)(context.Background(), nomes)
	if err != nil {
		t.Fatal(err)
	}
	if f.chamadas != 2 || len(valores) != 9 || valores["/p/01"] != "v/p/01" {
		t.Errorf("chamadas = %d, valores = %v", f.chamadas, valores)
	}
	for _, n := range []string{"/p/00", "/p/05", "/p/10"} {
		if _, ok := valores[n]; ok {
			t.Errorf("%s não existe e não deveria voltar", n)
		}
	}

	if _, err := ParametrosNoSSM(&fakeSSM{err: errors.New("throttling")})(context.Background(), nomes); err == nil {
		t.Error("esperava erro")
	}
}

// TestRotasIguaisAoContrato garante que toda operação do openapi.yaml vira
// uma rota na HTTP API, e nenhuma além delas.
func TestRotasIguaisAoContrato(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	caminho := regexp.MustCompile(`^  (/\S*):\s*$`)
	metodo := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	var contrato []string
	emPaths, atual := false, ""
	s := bufio.NewScanner(f)
	for s.Scan() {
		linha := s.Text()
		if !strings.HasPrefix(linha, " ") && strings.TrimSpace(linha) != "" {
			emPaths = strings.HasPrefix(linha, "paths:")
			continue
		}
		if !emPaths {
			continue
		}
		if m := caminho.FindStringSubmatch(linha); m != nil {
			atual = m[1]
		} else if m := metodo.FindStringSubmatch(linha); m != nil {
			contrato = append(contrato, strings.ToUpper(m[1])+" "+atual)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(contrato)
	if rotas := slices.Sorted(slices.Values(Rotas)); !slices.Equal(contrato, rotas) {
		t.Errorf("rotas do CDK = %v\nopenapi.yaml = %v", rotas, contrato)
	}
}

func TestIDRota(t *testing.T) {
	if got := IDRota("PUT /gastos/{id}"); got != "RotaPUTgastosid" {
		t.Errorf("IDRota = %q", got)
	}
}

func TestZipComBitDeExecucao(t *testing.T) {
	dir := dirLambdaFake(t)
	destino, err := zipBootstrap(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(destino)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 1 || r.File[0].Name != "bootstrap" || r.File[0].Mode().Perm() != 0o755 {
		t.Errorf("zip = %+v", r.File)
	}
}
