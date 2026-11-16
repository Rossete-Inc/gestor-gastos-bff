package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"sync"

	catcontract "github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// GastosService é o que as rotas usam do expenses-service, gastos e cartões
// (ADR 0009); *gastos.Client o satisfaz.
type GastosService interface {
	Criar(ctx context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error)
	Obter(ctx context.Context, userID, id string) (expcontract.Gasto, error)
	Atualizar(ctx context.Context, userID, id string, in expcontract.GastoIn) (expcontract.Gasto, error)
	Excluir(ctx context.Context, userID, id string) error
	Listar(ctx context.Context, userID string, in expcontract.ListarGastosIn) (expcontract.PaginaGastos, error)
	ExisteNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error)
	ResumoMensal(ctx context.Context, userID, mes string) (expcontract.ResumoMensalOut, error)

	ListarCartoes(ctx context.Context, userID string) ([]expcontract.Cartao, error)
	CriarCartao(ctx context.Context, userID string, in expcontract.CartaoIn) (expcontract.Cartao, error)
	AtualizarCartao(ctx context.Context, userID, id string, in expcontract.CartaoIn) (expcontract.Cartao, error)
	ArquivarCartao(ctx context.Context, userID, id string, arquivado bool) (expcontract.Cartao, error)
	ExcluirCartao(ctx context.Context, userID, id string) error
	ResumoCartoes(ctx context.Context, userID, mes, hoje string) (expcontract.ResumoCartoesOut, error)
}

// rotasGastos são servidas quando Deps.Gastos e Deps.Categorias estão
// presentes. Sem eles (Lambda antes da tarefa 8.5), continuam em 501.
var rotasGastos = []string{
	"GET /telas/dashboard",
	"GET /telas/gastos",
	"POST /gastos",
	"PUT /gastos/{id}",
	"DELETE /gastos/{id}",
	"GET /telas/cartoes",
	"GET /cartoes",
	"POST /cartoes",
	"PUT /cartoes/{id}",
	"PATCH /cartoes/{id}",
	"DELETE /cartoes/{id}",
}

// Categoria mostrada num gasto cuja categoria já foi excluída. A exclusão de
// categoria em uso é bloqueada, mas a corrida entre a checagem e a exclusão é
// aceita no MVP (design.md).
const (
	nomeCategoriaRemovida = "Removida"
	corCategoriaRemovida  = "#9E9E9E"
)

var padraoMes = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func (a *api) rotasDeGastos(mux *http.ServeMux) {
	mux.Handle("GET /telas/dashboard", a.autenticado(http.HandlerFunc(a.telaDashboard)))
	mux.Handle("GET /telas/gastos", a.autenticado(http.HandlerFunc(a.telaGastos)))
	mux.Handle("POST /gastos", a.autenticado(http.HandlerFunc(a.criarGasto)))
	mux.Handle("PUT /gastos/{id}", a.autenticado(http.HandlerFunc(a.atualizarGasto)))
	mux.Handle("DELETE /gastos/{id}", a.autenticado(http.HandlerFunc(a.excluirGasto)))
	a.rotasDeCartoes(mux)
}

// gastoIn é o corpo do POST e do PUT (GastoIn no openapi). O grupo não vem
// do cliente: é o da categoria.
type gastoIn struct {
	ValorCentavos int64  `json:"valorCentavos"`
	Data          string `json:"data"`
	CategoriaID   string `json:"categoriaId"`
	Descricao     string `json:"descricao"`
	Recorrente    bool   `json:"recorrente"`
	CartaoID      string `json:"cartaoId"`
}

// categoriaResumo é o CategoriaResumo do openapi.
type categoriaResumo struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
	Cor  string `json:"cor"`
}

// cartaoResumo é o CartaoResumo do openapi.
type cartaoResumo struct {
	ID      string `json:"id"`
	Apelido string `json:"apelido"`
	Cor     string `json:"cor"`
}

// gastoOut é o Gasto do openapi: o gasto com a categoria e o cartão
// resolvidos.
type gastoOut struct {
	ID            string          `json:"id"`
	ValorCentavos int64           `json:"valorCentavos"`
	Data          string          `json:"data"`
	MesReferencia string          `json:"mesReferencia"`
	Grupo         string          `json:"grupo"`
	Categoria     categoriaResumo `json:"categoria"`
	Cartao        *cartaoResumo   `json:"cartao,omitempty"`
	Descricao     string          `json:"descricao,omitempty"`
	Recorrente    bool            `json:"recorrente"`
}

// Cartão mostrado num gasto cujo cartão não existe mais. A exclusão de cartão
// com gastos é bloqueada; a corrida é aceita como na categoria.
const (
	apelidoCartaoRemovido = "Cartão removido"
	corCartaoRemovido     = "#9E9E9E"
)

func resumo(c catcontract.Categoria) categoriaResumo {
	return categoriaResumo{ID: c.ID, Nome: c.Nome, Cor: c.Cor}
}

// referencias são as categorias e os cartões visíveis, por ID, para resolver
// os gastos.
type referencias struct {
	categorias map[string]catcontract.Categoria
	cartoes    map[string]expcontract.Cartao
}

func novasReferencias(cats catcontract.CategoriasPorGrupo, cartoes []expcontract.Cartao) referencias {
	m := make(map[string]expcontract.Cartao, len(cartoes))
	for _, c := range cartoes {
		m[c.ID] = c
	}
	return referencias{categorias: porID(cats), cartoes: m}
}

func (ref referencias) cartao(id string) *cartaoResumo {
	if id == "" {
		return nil
	}
	if c, ok := ref.cartoes[id]; ok {
		return &cartaoResumo{ID: c.ID, Apelido: c.Apelido, Cor: c.Cor}
	}
	return &cartaoResumo{ID: id, Apelido: apelidoCartaoRemovido, Cor: corCartaoRemovido}
}

// resolver junta a categoria e o cartão ao gasto. Categoria que não está mais
// entre as visíveis aparece como "Removida".
func (ref referencias) resolver(g expcontract.Gasto) gastoOut {
	c, ok := ref.categorias[g.CategoriaID]
	r := categoriaResumo{ID: g.CategoriaID, Nome: nomeCategoriaRemovida, Cor: corCategoriaRemovida}
	if ok {
		r = resumo(c)
	}
	return comCategoria(g, r, ref.cartao(g.CartaoID))
}

func comCategoria(g expcontract.Gasto, c categoriaResumo, cartao *cartaoResumo) gastoOut {
	mes := g.MesReferencia
	if mes == "" && len(g.Data) >= 7 {
		mes = g.Data[:7]
	}
	return gastoOut{
		ID: g.ID, ValorCentavos: g.ValorCentavos, Data: g.Data, MesReferencia: mes, Grupo: g.Grupo,
		Categoria: c, Cartao: cartao, Descricao: g.Descricao, Recorrente: g.Recorrente,
	}
}

func porID(pg catcontract.CategoriasPorGrupo) map[string]catcontract.Categoria {
	m := make(map[string]catcontract.Categoria, len(pg.Cartao)+len(pg.Fixo))
	for _, c := range pg.Cartao {
		m[c.ID] = c
	}
	for _, c := range pg.Fixo {
		m[c.ID] = c
	}
	return m
}

// categoriaDoGasto valida a categoria escolhida no categories-service e
// devolve-a, com o grupo que o gasto herda. Inexistente ou de outro usuário
// vira 422 no campo categoriaId (cenário "Categoria de outro usuário").
func (a *api) categoriaDoGasto(ctx context.Context, userID, id string) (catcontract.Categoria, error) {
	invalida := func(msg string) error {
		return errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: "categoriaId", Message: msg})
	}
	if id == "" {
		return catcontract.Categoria{}, invalida("obrigatório")
	}
	if len(id) > maxID {
		return catcontract.Categoria{}, invalida("deve ter no máximo 64 caracteres")
	}
	c, err := a.deps.Categorias.Obter(ctx, userID, id)
	if errs.KindOf(err) == errs.KindNotFound {
		return catcontract.Categoria{}, invalida("categoria inexistente")
	}
	return c, err
}

// salvarGasto valida o corpo e a categoria e chama gravar com o GastoIn do
// expenses-service. Categoria do grupo CARTAO exige cartão (spec
// lancamento-gastos, "Gasto de cartão sem cartão"); o serviço confere se o
// cartão existe e está ativo. Devolve o gasto com a categoria e o cartão
// resolvidos.
func (a *api) salvarGasto(r *http.Request, w http.ResponseWriter, gravar func(ctx context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error)) (gastoOut, error) {
	c, _ := auth.FromContext(r.Context())
	var in gastoIn
	if err := lerJSON(w, r, &in); err != nil {
		return gastoOut{}, err
	}
	var cat catcontract.Categoria
	var cartoes []expcontract.Cartao
	err := emParalelo(
		func() (err error) { cat, err = a.categoriaDoGasto(r.Context(), c.UserID, in.CategoriaID); return err },
		func() (err error) { cartoes, err = a.deps.Gastos.ListarCartoes(r.Context(), c.UserID); return err },
	)
	if err != nil {
		return gastoOut{}, err
	}
	if cat.Grupo == catcontract.GrupoCartao && in.CartaoID == "" {
		return gastoOut{}, errs.Validation("Dados do gasto inválidos.",
			errs.FieldError{Field: "cartaoId", Message: "obrigatório para categorias de cartão"})
	}
	g, err := gravar(r.Context(), c.UserID, expcontract.GastoIn{
		ValorCentavos: in.ValorCentavos, Data: in.Data, CategoriaID: cat.ID, Grupo: cat.Grupo,
		Descricao: in.Descricao, Recorrente: in.Recorrente, CartaoID: in.CartaoID,
	})
	if err != nil {
		return gastoOut{}, err
	}
	ref := novasReferencias(catcontract.CategoriasPorGrupo{}, cartoes)
	return comCategoria(g, resumo(cat), ref.cartao(g.CartaoID)), nil
}

func (a *api) criarGasto(w http.ResponseWriter, r *http.Request) {
	out, err := a.salvarGasto(r, w, a.deps.Gastos.Criar)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusCreated, out)
}

func (a *api) atualizarGasto(w http.ResponseWriter, r *http.Request) {
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	out, err := a.salvarGasto(r, w, func(ctx context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error) {
		return a.deps.Gastos.Atualizar(ctx, userID, id, in)
	})
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, out)
}

func (a *api) excluirGasto(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	if err := a.deps.Gastos.Excluir(r.Context(), c.UserID, id); err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// telaGastos é o TelaGastos do openapi.
type telaGastos struct {
	Mes        string                         `json:"mes"`
	Itens      []gastoOut                     `json:"itens"`
	NextCursor string                         `json:"nextCursor,omitempty"`
	Categorias catcontract.CategoriasPorGrupo `json:"categorias"`
	Cartoes    []expcontract.Cartao           `json:"cartoes"`
}

// parametroInvalido é o 400 de query string malformada.
func parametroInvalido(nome string) error {
	return errs.BadRequest("parametro_invalido", "Parâmetro inválido: "+nome+".")
}

// listagemDaQuery lê e confere os parâmetros de /telas/gastos.
func listagemDaQuery(r *http.Request) (expcontract.ListarGastosIn, error) {
	q := r.URL.Query()
	in := expcontract.ListarGastosIn{
		Mes: q.Get("mes"), Grupo: q.Get("grupo"), CategoriaID: q.Get("categoriaId"),
		CartaoID: q.Get("cartaoId"), Cursor: q.Get("cursor"),
	}
	switch {
	case !padraoMes.MatchString(in.Mes):
		return in, parametroInvalido("mes (AAAA-MM)")
	case q.Has("grupo") && in.Grupo != catcontract.GrupoCartao && in.Grupo != catcontract.GrupoFixo:
		return in, parametroInvalido("grupo (CARTAO ou FIXO)")
	case q.Has("categoriaId") && (in.CategoriaID == "" || len(in.CategoriaID) > maxID):
		return in, parametroInvalido("categoriaId")
	case q.Has("cartaoId") && (in.CartaoID == "" || len(in.CartaoID) > maxID):
		return in, parametroInvalido("cartaoId")
	case q.Has("cursor") && (in.Cursor == "" || len(in.Cursor) > 2048):
		return in, errs.BadRequest("cursor_invalido", "Cursor de paginação inválido. Volte para a primeira página.")
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 100 {
			return in, parametroInvalido("limit (1 a 100)")
		}
		in.Limit = n
	}
	return in, nil
}

// emParalelo roda as funções ao mesmo tempo e devolve o primeiro erro, na
// ordem em que foram passadas.
func emParalelo(fs ...func() error) error {
	var wg sync.WaitGroup
	erros := make([]error, len(fs))
	wg.Add(len(fs))
	for i, f := range fs {
		go func() { defer wg.Done(); erros[i] = f() }()
	}
	wg.Wait()
	for _, err := range erros {
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *api) telaGastos(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	in, err := listagemDaQuery(r)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	var pagina expcontract.PaginaGastos
	var cats catcontract.CategoriasPorGrupo
	var cartoes []expcontract.Cartao
	err = emParalelo(
		func() (err error) { pagina, err = a.deps.Gastos.Listar(r.Context(), c.UserID, in); return err },
		func() (err error) { cats, err = a.deps.Categorias.Listar(r.Context(), c.UserID); return err },
		func() (err error) { cartoes, err = a.deps.Gastos.ListarCartoes(r.Context(), c.UserID); return err },
	)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	ref := novasReferencias(cats, cartoes)
	itens := make([]gastoOut, 0, len(pagina.Items))
	for _, g := range pagina.Items {
		itens = append(itens, ref.resolver(g))
	}
	escreverJSON(w, http.StatusOK, telaGastos{
		Mes: in.Mes, Itens: itens, NextCursor: pagina.NextCursor, Categorias: cats, Cartoes: naoNulo(cartoes),
	})
}

// naoNulo devolve a lista, ou uma vazia se ela for nula (o JSON traz []).
func naoNulo[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}
