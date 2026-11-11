package main

import (
	"regexp"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatch"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscodedeploy"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/aws-cdk-go/awscdk/v2/interfaces/interfacesawscloudwatch"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// BffProps configura a stack do BFF.
type BffProps struct {
	awscdk.StackProps
	Ambiente string
	// Codigo é o pacote da Lambda. nil = binário ausente: a stack registra
	// MsgFaltaBuild e não é sintetizada.
	Codigo awslambda.Code
	// Valores são os Prerequisitos lidos do SSM no synth (esteira). nil = cada
	// valor vira um parâmetro do CloudFormation resolvido no deploy (offline).
	Valores Valores
}

// NovaStackBff cria a Lambda do BFF (alias live com canary do CodeDeploy) e
// publica as rotas do contrato na HTTP API do infra-base, protegidas pelo
// authorizer JWT de lá.
//
// Tudo o que vem de outros repositórios vem do SSM, sem exports: na esteira,
// com os valores lidos no synth (props.Valores); offline, como parâmetros do
// CloudFormation (ver prerequisitos.go).
func NovaStackBff(scope constructs.Construct, id string, props *BffProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), &props.StackProps)
	env := props.Ambiente

	if props.Codigo == nil {
		awscdk.Annotations_Of(stack).AddError(jsii.String(MsgFaltaBuild))
		return stack
	}

	ler := func(param string) *string {
		if props.Valores != nil {
			return jsii.String(props.Valores[param])
		}
		return awsssm.StringParameter_ValueForStringParameter(stack, jsii.String(CaminhoSSM(env, param)), nil)
	}
	apiID := ler(ParamAPIID)
	usersArn := ler(ParamUsersFunctionArn)
	categoriesArn := ler(ParamCategoriesFunctionArn)

	nome := NomeStack(env, "bff")
	logs := awslogs.NewLogGroup(stack, jsii.String("Logs"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/" + nome),
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	fn := awslambda.NewFunction(stack, jsii.String("Funcao"), &awslambda.FunctionProps{
		FunctionName: jsii.String(nome),
		Description:  jsii.String("BFF do Gestor de Gastos (" + env + "): API pública atrás da HTTP API"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         props.Codigo,
		MemorySize:   jsii.Number(MemoriaMB),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(TimeoutSegundos)),
		LogGroup:     logs,
		// Variáveis lidas por cmd/lambda (LoadConfig).
		Environment: &map[string]*string{
			"RUNTIME":                 jsii.String("aws"),
			"ENV":                     jsii.String(env),
			"LOG_LEVEL":               jsii.String("info"),
			"COGNITO_ISSUER":          ler(ParamIssuerURL),
			"COGNITO_CLIENT_ID":       ler(ParamUserPoolClientID),
			"COGNITO_JWKS_URL":        ler(ParamJwksURL),
			"USERS_FUNCTION_ARN":      usersArn,
			"CATEGORIES_FUNCTION_ARN": categoriesArn,
		},
	})

	// O BFF só invoca o alias live de cada serviço (ADR 0004).
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Sid:       jsii.String("InvocarUsersService"),
		Actions:   jsii.Strings("lambda:InvokeFunction"),
		Resources: &[]*string{usersArn},
	}))
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Sid:       jsii.String("InvocarCategoriesService"),
		Actions:   jsii.Strings("lambda:InvokeFunction"),
		Resources: &[]*string{categoriesArn},
	}))

	alias := awslambda.NewAlias(stack, jsii.String("Live"), &awslambda.AliasProps{
		AliasName: jsii.String(AliasLive),
		Version:   fn.CurrentVersion(),
	})

	// Rollback automático (spec entrega-continua): qualquer erro da função no
	// alias durante o canary dispara o alarme, e o CodeDeploy devolve todo o
	// tráfego à versão anterior. Erro de inicialização (config ausente) conta.
	alarme := awscloudwatch.NewAlarm(stack, jsii.String("ErrosNoAlias"), &awscloudwatch.AlarmProps{
		AlarmName:        jsii.String(nome + "-erros"),
		AlarmDescription: jsii.String("Erros na Lambda do BFF (alias live); reverte o canary do CodeDeploy"),
		Metric: alias.MetricErrors(&awscloudwatch.MetricOptions{
			Period:    awscdk.Duration_Minutes(jsii.Number(1)),
			Statistic: jsii.String("Sum"),
		}),
		Threshold:          jsii.Number(1),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})

	app := awscodedeploy.NewLambdaApplication(stack, jsii.String("CodeDeployApp"), &awscodedeploy.LambdaApplicationProps{
		ApplicationName: jsii.String(nome),
	})
	awscodedeploy.NewLambdaDeploymentGroup(stack, jsii.String("Canary"), &awscodedeploy.LambdaDeploymentGroupProps{
		Application:         app,
		DeploymentGroupName: jsii.String(nome),
		Alias:               alias,
		DeploymentConfig:    awscodedeploy.LambdaDeploymentConfig_CANARY_10PERCENT_5MINUTES(),
		Alarms:              &[]interfacesawscloudwatch.IAlarmRef{alarme},
		AutoRollback: &awscodedeploy.AutoRollbackConfig{
			FailedDeployment:  jsii.Bool(true),
			StoppedDeployment: jsii.Bool(true),
			DeploymentInAlarm: jsii.Bool(true),
		},
	})

	publicarRotas(stack, apiID, ler(ParamAuthorizerID), alias)

	awsssm.NewStringParameter(stack, jsii.String("Param-"+ParamBffFunctionArn), &awsssm.StringParameterProps{
		ParameterName: jsii.String(CaminhoSSM(env, ParamBffFunctionArn)),
		StringValue:   alias.FunctionArn(),
		Description:   jsii.String("ARN do alias live da Lambda do BFF"),
		Tier:          awsssm.ParameterTier_STANDARD,
	})

	return stack
}

// publicarRotas adiciona à HTTP API do infra-base a integração com o alias
// live e uma rota por operação do contrato, todas com o authorizer JWT. A
// API, o stage $default (auto deploy), o CORS e a rota GET /infra/auth-check
// continuam sendo do infra-base; aqui só entram recursos novos.
func publicarRotas(stack awscdk.Stack, apiID, authorizerID *string, alias awslambda.Alias) {
	integracao := awsapigatewayv2.NewCfnIntegration(stack, jsii.String("Integracao"), &awsapigatewayv2.CfnIntegrationProps{
		ApiId:                apiID,
		Description:          jsii.String("Lambda do BFF (alias " + AliasLive + ")"),
		IntegrationType:      jsii.String("AWS_PROXY"),
		IntegrationUri:       alias.FunctionArn(),
		PayloadFormatVersion: jsii.String("2.0"),
	})

	for _, rota := range Rotas {
		awsapigatewayv2.NewCfnRoute(stack, jsii.String(IDRota(rota)), &awsapigatewayv2.CfnRouteProps{
			ApiId:             apiID,
			RouteKey:          jsii.String(rota),
			Target:            jsii.String("integrations/" + *integracao.Ref()),
			AuthorizationType: jsii.String("JWT"),
			AuthorizerId:      authorizerID,
		})
	}

	// A HTTP API invoca só o alias live, a partir de qualquer rota e stage da API.
	alias.AddPermission(jsii.String("InvocadoPelaHttpApi"), &awslambda.Permission{
		Principal: awsiam.NewServicePrincipal(jsii.String("apigateway.amazonaws.com"), nil),
		Action:    jsii.String("lambda:InvokeFunction"),
		SourceArn: jsii.String("arn:" + *awscdk.Aws_PARTITION() + ":execute-api:" + *awscdk.Aws_REGION() + ":" +
			*awscdk.Aws_ACCOUNT_ID() + ":" + *apiID + "/*/*"),
	})
}

var naoAlfanumerico = regexp.MustCompile(`[^A-Za-z0-9]+`)

// IDRota devolve o id do construct de uma rota. Ex.: "PUT /gastos/{id}" -> RotaPUTgastosid.
func IDRota(rota string) string {
	return "Rota" + naoAlfanumerico.ReplaceAllString(rota, "")
}
