package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	catcontract "github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// CategoriasService é o que as rotas de categorias usam do categories-service;
// *categorias.Client o satisfaz.
type CategoriasService interface {
	Listar(ctx context.Context, userID string) (catcontract.CategoriasPorGrupo, error)
	Obter(ctx context.Context, userID, id string) (catcontract.Categoria, error)
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
	mux.Handle("GET /categorias", a.naConta(http.HandlerFunc(a.listarCategorias)))
	mux.Handle("POST /categorias", a.naConta(http.HandlerFunc(a.criarCategoria)))
	mux.Handle("DELETE /categorias/{id}", a.naConta(http.HandlerFunc(a.excluirCategoria)))
	mux.Handle("GET /telas/gasto-form", a.naConta(http.HandlerFunc(a.telaGastoForm)))
}

func (a *api) listarCategorias(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	out, err := a.deps.Categorias.Listar(r.Context(), c.DonoID)
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
	c := contaDe(r.Context())
	var in categoriaIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	out, err := a.deps.Categorias.Criar(r.Context(), c.DonoID, catcontract.CriarCategoriaIn(in))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusCreated, out)
}

func (a *api) excluirCategoria(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	// Regra que atravessa serviços, por isso no BFF (design.md): categoria
	// com gastos não é excluída. A corrida entre a checagem e a exclusão é
	// aceita no MVP; o gasto órfão aparece com a categoria "Removida".
	if a.deps.Gastos != nil {
		emUso, err := a.deps.Gastos.ExisteNaCategoria(r.Context(), c.DonoID, id)
		if err != nil {
			a.erro(w, r, err)
			return
		}
		if emUso {
			a.erro(w, r, errs.Conflict("categoria_em_uso", "Há gastos nesta categoria. Mova ou exclua os gastos antes de excluí-la."))
			return
		}
	}
	if err := a.deps.Categorias.Excluir(r.Context(), c.DonoID, id); err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// telaGastoForm devolve as categorias e os cartões ativos para o formulário
// de gasto e, com ?id=, também o gasto para a tela de edição; se o cartão
// dele estiver arquivado, ele entra na lista para continuar selecionado.
// Sem o expenses-service (Lambda antes da tarefa 8.5), a lista de cartões
// vem vazia e o modo de edição responde 501.
func (a *api) telaGastoForm(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	q := r.URL.Query()
	var id string
	if q.Has("id") {
		if a.deps.Gastos == nil {
			naoImplementado(w, r)
			return
		}
		var err error
		if id, err = idValido(q.Get("id")); err != nil {
			a.erro(w, r, err)
			return
		}
	}
	var cats catcontract.CategoriasPorGrupo
	var cartoes []expcontract.Cartao
	var g expcontract.Gasto
	tarefas := []func() error{
		func() (err error) { cats, err = a.deps.Categorias.Listar(r.Context(), c.DonoID); return err },
	}
	if a.deps.Gastos != nil {
		tarefas = append(tarefas, func() (err error) { cartoes, err = a.deps.Gastos.ListarCartoes(r.Context(), c.DonoID); return err })
	}
	if id != "" {
		tarefas = append(tarefas, func() (err error) { g, err = a.deps.Gastos.Obter(r.Context(), c.DonoID, id); return err })
	}
	if err := emParalelo(tarefas...); err != nil {
		a.erro(w, r, err)
		return
	}
	tela := telaGastoForm{Categorias: cats, Cartoes: []expcontract.Cartao{}}
	for _, k := range cartoes {
		if !k.Arquivado || (id != "" && k.ID == g.CartaoID) {
			tela.Cartoes = append(tela.Cartoes, k)
		}
	}
	if id != "" {
		out := novasReferencias(cats, cartoes).resolver(g)
		tela.Gasto = &out
	}
	escreverJSON(w, http.StatusOK, tela)
}

// telaGastoForm é o TelaGastoForm do openapi.
type telaGastoForm struct {
	Categorias catcontract.CategoriasPorGrupo `json:"categorias"`
	Cartoes    []expcontract.Cartao           `json:"cartoes"`
	Gasto      *gastoOut                      `json:"gasto,omitempty"`
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
