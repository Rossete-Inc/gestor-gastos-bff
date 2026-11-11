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

// GastosService é o que as rotas usam do expenses-service; *gastos.Client o
// satisfaz.
type GastosService interface {
	Criar(ctx context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error)
	Obter(ctx context.Context, userID, id string) (expcontract.Gasto, error)
	Atualizar(ctx context.Context, userID, id string, in expcontract.GastoIn) (expcontract.Gasto, error)
	Excluir(ctx context.Context, userID, id string) error
	Listar(ctx context.Context, userID string, in expcontract.ListarGastosIn) (expcontract.PaginaGastos, error)
	ExisteNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error)
	ResumoMensal(ctx context.Context, userID, mes string) (expcontract.ResumoMensalOut, error)
}

// rotasGastos são servidas quando Deps.Gastos e Deps.Categorias estão
// presentes. Sem eles (Lambda antes da tarefa 8.5), continuam em 501.
var rotasGastos = []string{
	"GET /telas/dashboard",
	"GET /telas/gastos",
	"POST /gastos",
	"PUT /gastos/{id}",
	"DELETE /gastos/{id}",
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
}

// gastoIn é o corpo do POST e do PUT (GastoIn no openapi). O grupo não vem
// do cliente: é o da categoria.
type gastoIn struct {
	ValorCentavos int64  `json:"valorCentavos"`
	Data          string `json:"data"`
	CategoriaID   string `json:"categoriaId"`
	Descricao     string `json:"descricao"`
	Recorrente    bool   `json:"recorrente"`
}

// categoriaResumo é o CategoriaResumo do openapi.
type categoriaResumo struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
	Cor  string `json:"cor"`
}

// gastoOut é o Gasto do openapi: o gasto com a categoria resolvida.
type gastoOut struct {
	ID            string          `json:"id"`
	ValorCentavos int64           `json:"valorCentavos"`
	Data          string          `json:"data"`
	Grupo         string          `json:"grupo"`
	Categoria     categoriaResumo `json:"categoria"`
	Descricao     string          `json:"descricao,omitempty"`
	Recorrente    bool            `json:"recorrente"`
}

func resumo(c catcontract.Categoria) categoriaResumo {
	return categoriaResumo{ID: c.ID, Nome: c.Nome, Cor: c.Cor}
}

// resolver junta a categoria ao gasto. Categoria que não está mais entre as
// visíveis aparece como "Removida".
func resolver(g expcontract.Gasto, cats map[string]catcontract.Categoria) gastoOut {
	c, ok := cats[g.CategoriaID]
	r := categoriaResumo{ID: g.CategoriaID, Nome: nomeCategoriaRemovida, Cor: corCategoriaRemovida}
	if ok {
		r = resumo(c)
	}
	return comCategoria(g, r)
}

func comCategoria(g expcontract.Gasto, c categoriaResumo) gastoOut {
	return gastoOut{
		ID: g.ID, ValorCentavos: g.ValorCentavos, Data: g.Data, Grupo: g.Grupo,
		Categoria: c, Descricao: g.Descricao, Recorrente: g.Recorrente,
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
// expenses-service. Devolve o gasto com a categoria resolvida.
func (a *api) salvarGasto(r *http.Request, w http.ResponseWriter, gravar func(ctx context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error)) (gastoOut, error) {
	c, _ := auth.FromContext(r.Context())
	var in gastoIn
	if err := lerJSON(w, r, &in); err != nil {
		return gastoOut{}, err
	}
	cat, err := a.categoriaDoGasto(r.Context(), c.UserID, in.CategoriaID)
	if err != nil {
		return gastoOut{}, err
	}
	g, err := gravar(r.Context(), c.UserID, expcontract.GastoIn{
		ValorCentavos: in.ValorCentavos, Data: in.Data, CategoriaID: cat.ID, Grupo: cat.Grupo,
		Descricao: in.Descricao, Recorrente: in.Recorrente,
	})
	if err != nil {
		return gastoOut{}, err
	}
	return comCategoria(g, resumo(cat)), nil
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
}

// parametroInvalido é o 400 de query string malformada.
func parametroInvalido(nome string) error {
	return errs.BadRequest("parametro_invalido", "Parâmetro inválido: "+nome+".")
}

// listagemDaQuery lê e confere os parâmetros de /telas/gastos.
func listagemDaQuery(r *http.Request) (expcontract.ListarGastosIn, error) {
	q := r.URL.Query()
	in := expcontract.ListarGastosIn{
		Mes: q.Get("mes"), Grupo: q.Get("grupo"), CategoriaID: q.Get("categoriaId"), Cursor: q.Get("cursor"),
	}
	switch {
	case !padraoMes.MatchString(in.Mes):
		return in, parametroInvalido("mes (AAAA-MM)")
	case q.Has("grupo") && in.Grupo != catcontract.GrupoCartao && in.Grupo != catcontract.GrupoFixo:
		return in, parametroInvalido("grupo (CARTAO ou FIXO)")
	case q.Has("categoriaId") && (in.CategoriaID == "" || len(in.CategoriaID) > maxID):
		return in, parametroInvalido("categoriaId")
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

// emParalelo roda f e g ao mesmo tempo e devolve o primeiro erro.
func emParalelo(f, g func() error) error {
	var wg sync.WaitGroup
	var errF, errG error
	wg.Add(2)
	go func() { defer wg.Done(); errF = f() }()
	go func() { defer wg.Done(); errG = g() }()
	wg.Wait()
	if errF != nil {
		return errF
	}
	return errG
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
	err = emParalelo(
		func() (err error) { pagina, err = a.deps.Gastos.Listar(r.Context(), c.UserID, in); return err },
		func() (err error) { cats, err = a.deps.Categorias.Listar(r.Context(), c.UserID); return err },
	)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	m := porID(cats)
	itens := make([]gastoOut, 0, len(pagina.Items))
	for _, g := range pagina.Items {
		itens = append(itens, resolver(g, m))
	}
	escreverJSON(w, http.StatusOK, telaGastos{Mes: in.Mes, Itens: itens, NextCursor: pagina.NextCursor, Categorias: cats})
}
