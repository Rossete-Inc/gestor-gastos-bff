package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// contrato carrega o openapi.yaml uma vez para validar as respostas.
var contrato routers.Router

func init() {
	openapi3filter.RegisterBodyDecoder("application/problem+json", openapi3filter.JSONBodyDecoder)
}

func carregarContrato(t *testing.T) routers.Router {
	t.Helper()
	if contrato != nil {
		return contrato
	}
	doc, err := openapi3.NewLoader().LoadFromFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.yaml inválido: %v", err)
	}
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	contrato = r
	return r
}

// conforme valida status, headers e corpo da resposta contra o contrato.
func conforme(t *testing.T, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	r := carregarContrato(t)
	vreq := req.Clone(context.Background())
	vreq.URL.Scheme, vreq.URL.Host, vreq.Host = "http", "localhost:8080", "localhost:8080"
	route, params, err := r.FindRoute(vreq)
	if err != nil {
		t.Fatalf("rota fora do contrato: %v", err)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: vreq, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 rec.Code,
		Header:                 rec.Header(),
		Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	}
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		t.Errorf("resposta %d fora do contrato: %v\ncorpo: %s", rec.Code, err, rec.Body.String())
	}
}

type fakeMe struct {
	perfil contract.Perfil
	err    error
	claims auth.Claims
	token  string
	panico bool
}

func (f *fakeMe) Obter(_ context.Context, c auth.Claims, token string) (contract.Perfil, error) {
	if f.panico {
		panic("estourou")
	}
	f.claims, f.token = c, token
	return f.perfil, f.err
}

func perfilAna() contract.Perfil {
	return contract.Perfil{
		ID: "u-1", Email: "ana@x.com", Nome: "Ana", Papel: contract.PapelCliente,
		Status: contract.StatusAtivo, CriadoEm: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
	}
}

func montar(t *testing.T, me *fakeMe) (http.Handler, *authtest.Emissor, *bytes.Buffer) {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := New(Deps{
		Tokens: v, Me: me, Log: slog.New(slog.NewJSONHandler(&logs, nil)),
		CORSOrigins: []string{"http://localhost:8081"},
	})
	return h, emissor, &logs
}

func fazer(h http.Handler, method, path, token string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return req, rec
}

func problema(t *testing.T, rec *httptest.ResponseRecorder) errs.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != errs.ContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	var p errs.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("corpo não é problem: %s", rec.Body.String())
	}
	return p
}

func TestMe(t *testing.T) {
	me := &fakeMe{perfil: perfilAna()}
	h, emissor, logs := montar(t, me)
	token := emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})

	req, rec := fazer(h, http.MethodGet, "/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if me.claims.UserID != "u-1" || me.token != token {
		t.Errorf("claims = %+v, token repassado = %v", me.claims, me.token == token)
	}
	if strings.Contains(logs.String(), token) || !strings.Contains(logs.String(), `"userId":"u-1"`) {
		t.Errorf("log da requisição = %s", logs.String())
	}
}

func TestSemAutenticacao(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{perfil: perfilAna()})
	outro := authtest.New(t)
	casos := map[string]string{
		"sem token":                "",
		"token malformado":         "abc.def.ghi",
		"token vencido":            emissor.Token(t, "u-1", authtest.Opcoes{Validade: -time.Hour}),
		"outro app client":         emissor.Token(t, "u-1", authtest.Opcoes{ClientID: "outro"}),
		"assinado por outra chave": outro.Token(t, "u-1", authtest.Opcoes{}),
	}
	for nome, token := range casos {
		t.Run(nome, func(t *testing.T) {
			for _, rota := range []string{"/me", "/telas/dashboard?mes=2026-11", "/admin/telas/usuarios"} {
				req, rec := fazer(h, http.MethodGet, rota, token)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("%s: status = %d", rota, rec.Code)
				}
				if p := problema(t, rec); p.Code != "token_invalido" {
					t.Errorf("%s: code = %q", rota, p.Code)
				}
				conforme(t, req, rec)
			}
		})
	}
}

func TestAdmin(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{})

	t.Run("cliente recebe 403", func(t *testing.T) {
		// Cenário "Cliente tenta acessar área admin".
		token := emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/admin/telas/usuarios"},
			{http.MethodPatch, "/admin/usuarios/u-2"},
			{http.MethodDelete, "/admin/usuarios/u-2"},
		} {
			req, rec := fazer(h, r.method, r.path, token)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: status = %d", r.method, r.path, rec.Code)
			}
			if p := problema(t, rec); p.Code != "somente_admin" {
				t.Errorf("code = %q", p.Code)
			}
			if r.method == http.MethodGet {
				conforme(t, req, rec)
			}
		}
	})

	t.Run("usuário sem grupo é cliente", func(t *testing.T) {
		_, rec := fazer(h, http.MethodGet, "/admin/telas/usuarios", emissor.Token(t, "u-1", authtest.Opcoes{}))
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d", rec.Code)
		}
	})

	t.Run("admin passa da checagem", func(t *testing.T) {
		_, rec := fazer(h, http.MethodGet, "/admin/telas/usuarios", emissor.Token(t, "adm", authtest.Opcoes{Grupos: []string{"admin"}}))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("status = %d, quer 501 (rota da fatia 5)", rec.Code)
		}
		if p := problema(t, rec); p.Code != "nao_implementado" {
			t.Errorf("code = %q", p.Code)
		}
	})
}

func TestErrosDoMe(t *testing.T) {
	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"token revogado no Cognito": {errs.Unauthorized("token_invalido", "Sessão inválida."), 401, "token_invalido"},
		"falha interna":             {errs.Internal(errors.New("users-service: segredo")), 500, "erro_interno"},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			h, emissor, logs := montar(t, &fakeMe{err: c.err})
			req, rec := fazer(h, http.MethodGet, "/me", emissor.Token(t, "u-1", authtest.Opcoes{}))
			if rec.Code != c.status {
				t.Fatalf("status = %d", rec.Code)
			}
			p := problema(t, rec)
			if p.Code != c.code || strings.Contains(rec.Body.String(), "segredo") {
				t.Errorf("problem = %+v", p)
			}
			conforme(t, req, rec)
			if c.status == 500 && !strings.Contains(logs.String(), "segredo") {
				t.Error("a causa da falha interna deveria ir para o log")
			}
		})
	}
}

func TestPanicViraErroInterno(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{panico: true})
	_, rec := fazer(h, http.MethodGet, "/me", emissor.Token(t, "u-1", authtest.Opcoes{}))
	if rec.Code != http.StatusInternalServerError || problema(t, rec).Code != "erro_interno" {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRotaInexistente(t *testing.T) {
	h, _, _ := montar(t, &fakeMe{})
	_, rec := fazer(h, http.MethodGet, "/nada", "")
	if rec.Code != http.StatusNotFound || problema(t, rec).Code != "rota_nao_encontrada" {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRotasFuturasExigemToken(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{})
	token := emissor.Token(t, "u-1", authtest.Opcoes{})
	for _, rota := range rotasFuturas {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.ReplaceAll(path, "{id}", "x")
		if _, rec := fazer(h, method, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem token: %d", rota, rec.Code)
		}
		if strings.HasPrefix(path, "/admin/") {
			continue
		}
		if _, rec := fazer(h, method, path, token); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s com token: %d", rota, rec.Code)
		}
	}
}

func TestCORS(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{perfil: perfilAna()})

	t.Run("preflight da origem do Expo", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/me", nil)
		req.Header.Set("Origin", "http://localhost:8081")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8081" ||
			!strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Errorf("preflight = %d %v", rec.Code, rec.Header())
		}
	})

	t.Run("requisição da origem do Expo", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Origin", "http://localhost:8081")
		req.Header.Set("Authorization", "Bearer "+emissor.Token(t, "u-1", authtest.Opcoes{}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8081" {
			t.Errorf("status = %d, headers = %v", rec.Code, rec.Header())
		}
	})

	t.Run("outra origem não é liberada", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/me", nil)
		req.Header.Set("Origin", "https://site-malicioso.example")
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origem estranha liberada: %v", rec.Header())
		}
	})
}

func TestBearer(t *testing.T) {
	casos := map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "Bearer": "", "": ""}
	for h, quer := range casos {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", h)
		if got := bearer(req); got != quer {
			t.Errorf("bearer(%q) = %q, quer %q", h, got, quer)
		}
	}
}
