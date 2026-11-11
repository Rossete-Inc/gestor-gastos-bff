package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	catcontract "github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// CategoriasService é o que as rotas de categorias usam do categories-service;
// *categorias.Client o satisfaz.
type CategoriasService interface {
	Listar(ctx context.Context, userID string) (catcontract.CategoriasPorGrupo, error)
	Criar(ctx context.Context, userID string, in catcontract.CriarCategoriaIn) (catcontract.Categoria, error)
	Excluir(ctx context.Context, userID, id string) error
}

// rotasCategorias são servidas pelo categories-service quando Deps.Categorias
// está presente. Sem ele (Lambda antes da tarefa 7.6), continuam em 501.
var rotasCategorias = []string{
	"GET /categorias",
	"POST /categorias",
	"DELETE /categorias/{id}",
	"GET /telas/gasto-form",
}

// maxCorpo limita o corpo das requisições com JSON. Uma CategoriaIn válida
// cabe com folga em 1 KiB.
const maxCorpo = 16 << 10

// maxID é o tamanho máximo de um id no path e na query (IdPath no openapi).
const maxID = 64

func (a *api) rotasDeCategorias(mux *http.ServeMux) {
	mux.Handle("GET /categorias", a.autenticado(http.HandlerFunc(a.listarCategorias)))
	mux.Handle("POST /categorias", a.autenticado(http.HandlerFunc(a.criarCategoria)))
	mux.Handle("DELETE /categorias/{id}", a.autenticado(http.HandlerFunc(a.excluirCategoria)))
	mux.Handle("GET /telas/gasto-form", a.autenticado(http.HandlerFunc(a.telaGastoForm)))
}

func (a *api) listarCategorias(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	out, err := a.deps.Categorias.Listar(r.Context(), c.UserID)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, out)
}

// categoriaIn é o corpo do POST /categorias (CategoriaIn no openapi). O BFF só
// confere o formato do JSON; as regras (nome, grupo, cor, duplicidade) são do
// categories-service, que responde 422 com errors[] ou 409.
type categoriaIn struct {
	Nome  string `json:"nome"`
	Grupo string `json:"grupo"`
	Cor   string `json:"cor"`
}

func (a *api) criarCategoria(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	var in categoriaIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	out, err := a.deps.Categorias.Criar(r.Context(), c.UserID, catcontract.CriarCategoriaIn(in))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusCreated, out)
}

func (a *api) excluirCategoria(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	// A checagem de gastos vinculados (409 categoria_em_uso) entra com o
	// expenses-service, na tarefa 8.3.
	if err := a.deps.Categorias.Excluir(r.Context(), c.UserID, id); err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// telaGastoForm devolve as categorias para o formulário de gasto. O modo de
// edição (?id=) precisa do expenses-service e chega na tarefa 8.3.
func (a *api) telaGastoForm(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("id") {
		naoImplementado(w, r)
		return
	}
	c, _ := auth.FromContext(r.Context())
	cats, err := a.deps.Categorias.Listar(r.Context(), c.UserID)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, telaGastoForm{Categorias: cats})
}

// telaGastoForm é o TelaGastoForm do openapi; Gasto entra na tarefa 8.3.
type telaGastoForm struct {
	Categorias catcontract.CategoriasPorGrupo `json:"categorias"`
}

func idValido(id string) (string, error) {
	if id == "" || utf8.RuneCountInString(id) > maxID {
		return "", errs.BadRequest("id_invalido", "Identificador inválido.")
	}
	return id, nil
}

// lerJSON decodifica um único objeto JSON do corpo, recusando campos fora do
// contrato (additionalProperties: false), corpo vazio, lixo depois do objeto e
// corpos acima de maxCorpo. Todos respondem 400 corpo_invalido.
func lerJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCorpo))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var grande *http.MaxBytesError
		if errors.As(err, &grande) {
			return errs.BadRequest("corpo_invalido", "Corpo da requisição grande demais.")
		}
		return errs.BadRequest("corpo_invalido", "Corpo da requisição inválido.")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errs.BadRequest("corpo_invalido", "Corpo da requisição inválido.")
	}
	return nil
}
