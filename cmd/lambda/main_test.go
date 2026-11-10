package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/lambdahttp"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

const usersArn = "arn:aws:lambda:us-east-1:123456789012:function:gestor-gastos-dev-users:live"

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func valido() map[string]string {
	return map[string]string{
		"RUNTIME":          "aws",
		"ENV":              "dev",
		VarCognitoIssuer:   "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_abc",
		VarCognitoClientID: "client",
		VarUsersFunction:   usersArn,
	}
}

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(valido()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Base.Env != "dev" || c.Base.Region != "us-east-1" || c.UsersFunction != usersArn || c.CognitoJWKSURL != "" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]struct {
		mudar func(m map[string]string)
		msg   string
	}{
		"sem RUNTIME":   {func(m map[string]string) { delete(m, "RUNTIME") }, "RUNTIME"},
		"RUNTIME=local": {func(m map[string]string) { m["RUNTIME"], m["ENV"] = "local", "local" }, "RUNTIME=aws"},
		"sem issuer":    {func(m map[string]string) { delete(m, VarCognitoIssuer) }, VarCognitoIssuer},
		"sem client":    {func(m map[string]string) { delete(m, VarCognitoClientID) }, VarCognitoClientID},
		"sem users":     {func(m map[string]string) { delete(m, VarUsersFunction) }, VarUsersFunction},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			m := valido()
			c.mudar(m)
			_, err := LoadConfig(env(m))
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("erro = %v, queria %q", err, c.msg)
			}
		})
	}
}

// fakeAWS imita o endpoint de Invoke da Lambda: responde ObterPerfil com um
// perfil existente e guarda a função chamada.
type fakeAWS struct {
	mu      sync.Mutex
	funcoes []string
}

func (f *fakeAWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	nome, ok := strings.CutPrefix(r.URL.EscapedPath(), "/2015-03-31/functions/")
	nome, ok2 := strings.CutSuffix(nome, "/invocations")
	if !ok || !ok2 {
		http.Error(w, "rota inesperada "+r.URL.Path, http.StatusNotFound)
		return
	}
	nome, _ = url.PathUnescape(nome)
	f.mu.Lock()
	f.funcoes = append(f.funcoes, nome)
	f.mu.Unlock()

	var req invoke.Request
	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &req); err != nil || req.Operation != contract.OpObterPerfil {
		http.Error(w, "envelope inesperado: "+string(b), http.StatusBadRequest)
		return
	}
	perfil, _ := json.Marshal(contract.Perfil{ID: req.UserID, Email: "ana@exemplo.com", Nome: "Ana", Papel: contract.PapelCliente, Status: contract.StatusAtivo})
	_ = json.NewEncoder(w).Encode(invoke.Response{Payload: perfil})
}

func awsFake(t *testing.T, h http.Handler) aws.Config {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return aws.Config{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("AKIDTESTE", "segredo", ""),
	}
}

func chamar(t *testing.T, f lambdahttp.Func, metodo, caminho, token string) events.APIGatewayV2HTTPResponse {
	t.Helper()
	ev := events.APIGatewayV2HTTPRequest{Version: "2.0", RawPath: caminho, Headers: map[string]string{}}
	ev.RequestContext.HTTP.Method = metodo
	if token != "" {
		ev.Headers["authorization"] = "Bearer " + token
	}
	out, err := f(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMeInvocaOUsersServicePorLambda(t *testing.T) {
	emissor := authtest.New(t)
	m := valido()
	m[VarCognitoIssuer] = emissor.Issuer
	m[VarCognitoClientID] = authtest.ClientID
	c, err := LoadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeAWS{}
	h, err := NewHandler(c, awsFake(t, fake))
	if err != nil {
		t.Fatal(err)
	}
	f := lambdahttp.Handler(h)

	out := chamar(t, f, http.MethodGet, "/me", emissor.Token(t, "user-1", authtest.Opcoes{}))
	if out.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", out.StatusCode, out.Body)
	}
	var p contract.Perfil
	if err := json.Unmarshal([]byte(out.Body), &p); err != nil || p.ID != "user-1" || p.Email != "ana@exemplo.com" {
		t.Errorf("perfil = %+v, %v", p, err)
	}
	if len(fake.funcoes) != 1 || fake.funcoes[0] != usersArn {
		t.Errorf("funções invocadas = %v", fake.funcoes)
	}

	if out := chamar(t, f, http.MethodGet, "/me", ""); out.StatusCode != http.StatusUnauthorized {
		t.Errorf("sem token: status = %d", out.StatusCode)
	}
	if out := chamar(t, f, http.MethodGet, "/nao-existe", ""); out.StatusCode != http.StatusNotFound {
		t.Errorf("rota desconhecida: status = %d", out.StatusCode)
	}
}

func TestRun(t *testing.T) {
	// Sem arquivos de configuração da máquina: o teste não depende de ~/.aws.
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/nao-existe")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", t.TempDir()+"/nao-existe")

	var iniciada lambdahttp.Func
	if err := run(context.Background(), env(valido()), func(f lambdahttp.Func) { iniciada = f }); err != nil {
		t.Fatal(err)
	}
	if iniciada == nil {
		t.Fatal("lambda.Start não foi chamado")
	}
	if out := chamar(t, iniciada, http.MethodGet, "/me", ""); out.StatusCode != http.StatusUnauthorized {
		t.Errorf("sem token: status = %d", out.StatusCode)
	}
}

func TestRunConfigInvalida(t *testing.T) {
	chamou := false
	if err := run(context.Background(), env(map[string]string{}), func(lambdahttp.Func) { chamou = true }); err == nil || chamou {
		t.Errorf("err = %v, chamou = %v", err, chamou)
	}
}

func TestNewHandlerRecusa(t *testing.T) {
	c, err := LoadConfig(env(valido()))
	if err != nil {
		t.Fatal(err)
	}
	semIssuer, semUsers := c, c
	semIssuer.CognitoIssuer = ""
	semUsers.UsersFunction = ""
	for nome, cfg := range map[string]Config{"sem issuer": semIssuer, "sem users": semUsers} {
		t.Run(nome, func(t *testing.T) {
			if h, err := NewHandler(cfg, aws.Config{Region: "us-east-1"}); err == nil || h != nil {
				t.Errorf("NewHandler = %v, %v", h, err)
			}
		})
	}
}
