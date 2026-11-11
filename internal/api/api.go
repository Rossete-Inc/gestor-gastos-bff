// Package api é a camada HTTP do BFF: rotas do openapi.yaml, autenticação,
// checagem de admin em /admin/*, CORS local e log de requisições.
//
// O mesmo http.Handler serve o cmd/local e, na tarefa 6.5, a Lambda atrás
// da HTTP API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// TokenValidator valida o access token; *auth.Validator o satisfaz.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (auth.Claims, error)
}

// MeService é o caso de uso do /me.
type MeService interface {
	Obter(ctx context.Context, c auth.Claims, accessToken string) (contract.Perfil, error)
}

// Deps são as dependências da API.
type Deps struct {
	Tokens TokenValidator
	Me     MeService
	// Categorias é opcional: sem ele, as rotas de categorias e a tela do
	// formulário de gasto respondem 501 (Lambda antes da tarefa 7.6).
	Categorias CategoriasService
	Log        *slog.Logger
	// CORSOrigins lista as origens aceitas. Só o cmd/local usa; na AWS o CORS
	// é feito pela HTTP API.
	CORSOrigins []string
}

// rotasFuturas são as rotas do contrato implementadas nas próximas fatias.
// Respondem 501 depois da autenticação, para o front já tratar 401 e 403.
var rotasFuturas = []string{
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

// New monta o handler da API.
func New(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	a := &api{deps: d}
	mux := http.NewServeMux()

	mux.Handle("GET /me", a.autenticado(http.HandlerFunc(a.me)))
	futuras := rotasFuturas
	if d.Categorias != nil {
		a.rotasDeCategorias(mux)
		futuras = slices.DeleteFunc(slices.Clone(futuras), func(r string) bool { return slices.Contains(rotasCategorias, r) })
	}
	for _, rota := range futuras {
		h := a.autenticado(http.HandlerFunc(naoImplementado))
		if strings.Contains(rota, " /admin/") {
			h = a.autenticado(somenteAdmin(http.HandlerFunc(naoImplementado)))
		}
		mux.Handle(rota, h)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		errs.Write(w, r.URL.Path, errs.NotFound("rota_nao_encontrada", "Rota não encontrada."))
	})

	return a.recuperar(a.registrar(a.cors(mux)))
}

type api struct{ deps Deps }

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	p, err := a.deps.Me.Obter(r.Context(), c, bearer(r))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, p)
}

// autenticado valida o token e põe as claims no contexto.
func (a *api) autenticado(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := a.deps.Tokens.Validate(r.Context(), bearer(r))
		if err != nil {
			a.erro(w, r, err)
			return
		}
		if rw, ok := w.(*respostaRegistrada); ok {
			rw.userID = c.UserID
		}
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), c)))
	})
}

// somenteAdmin responde 403 a quem não está no grupo admin.
func somenteAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := auth.FromContext(r.Context())
		if err := auth.RequireAdmin(c); err != nil {
			errs.Write(w, r.URL.Path, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func naoImplementado(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", errs.ContentType)
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(errs.Problem{
		Type:     errs.TypePrefix + "nao_implementado",
		Title:    "Não implementado",
		Status:   http.StatusNotImplemented,
		Detail:   "Esta funcionalidade chega numa próxima fatia.",
		Instance: r.URL.Path,
		Code:     "nao_implementado",
	})
}

// erro grava o problem+json. Falhas internas vão para o log com a causa, que
// nunca vai na resposta.
func (a *api) erro(w http.ResponseWriter, r *http.Request, err error) {
	if errs.KindOf(err) == errs.KindInternal {
		a.deps.Log.ErrorContext(r.Context(), "falha interna", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	errs.Write(w, r.URL.Path, err)
}

// cors atende o preflight e libera as origens configuradas.
func (a *api) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origem := r.Header.Get("Origin")
		if origem == "" || !slices.Contains(a.deps.CORSOrigins, origem) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origem)
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// respostaRegistrada guarda status e usuário para o log da requisição.
type respostaRegistrada struct {
	http.ResponseWriter
	status int
	userID string
}

func (rw *respostaRegistrada) WriteHeader(s int) {
	if rw.status == 0 {
		rw.status = s
	}
	rw.ResponseWriter.WriteHeader(s)
}

func (rw *respostaRegistrada) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

// registrar loga método, path, status, duração e userId. Nunca o token, a
// query string ou o corpo.
func (a *api) registrar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inicio := time.Now()
		rw := &respostaRegistrada{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		a.deps.Log.InfoContext(r.Context(), "requisição",
			"method", r.Method, "path", r.URL.Path, "status", rw.status,
			"ms", time.Since(inicio).Milliseconds(), "userId", rw.userID)
	})
}

// recuperar transforma panic em 500 genérico.
func (a *api) recuperar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				a.erro(w, r, errs.Internal(fmt.Errorf("panic: %v", p)))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func escreverJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
