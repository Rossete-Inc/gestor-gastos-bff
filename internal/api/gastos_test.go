package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// fakeGastos imita o client do expenses-service e guarda as chamadas.
type fakeGastos struct {
	mu        sync.Mutex
	chamadas  []string
	userID    string
	id        string
	in        expcontract.GastoIn
	listarIn  expcontract.ListarGastosIn
	pagina    expcontract.PaginaGastos
	gasto     expcontract.Gasto
	emUso     bool
	err       error
	errListar error
	resumo    expcontract.ResumoMensalOut
	mes       string
	errResumo error
}

func (f *fakeGastos) registrar(op, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chamadas = append(f.chamadas, op)
	f.userID = userID
}

func (f *fakeGastos) chamou() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.chamadas, ",")
}

// gravado devolve o gasto como o serviço gravaria.
func gravado(id string, in expcontract.GastoIn) expcontract.Gasto {
	return expcontract.Gasto{ID: id, ValorCentavos: in.ValorCentavos, Data: in.Data, Grupo: in.Grupo,
		CategoriaID: in.CategoriaID, Descricao: in.Descricao, Recorrente: in.Recorrente}
}

func (f *fakeGastos) Criar(_ context.Context, userID string, in expcontract.GastoIn) (expcontract.Gasto, error) {
	f.registrar("Criar", userID)
	f.in = in
	if f.err != nil {
		return expcontract.Gasto{}, f.err
	}
	return gravado("20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D", in), nil
}

func (f *fakeGastos) Obter(_ context.Context, userID, id string) (expcontract.Gasto, error) {
	f.registrar("Obter", userID)
	f.id = id
	return f.gasto, f.err
}

func (f *fakeGastos) Atualizar(_ context.Context, userID, id string, in expcontract.GastoIn) (expcontract.Gasto, error) {
	f.registrar("Atualizar", userID)
	f.id, f.in = id, in
	if f.err != nil {
		return expcontract.Gasto{}, f.err
	}
	return gravado("20261031-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D", in), nil
}

func (f *fakeGastos) Excluir(_ context.Context, userID, id string) error {
	f.registrar("Excluir", userID)
	f.id = id
	return f.err
}

func (f *fakeGastos) Listar(_ context.Context, userID string, in expcontract.ListarGastosIn) (expcontract.PaginaGastos, error) {
	f.registrar("Listar", userID)
	f.listarIn = in
	if f.pagina.Items == nil {
		f.pagina.Items = []expcontract.Gasto{}
	}
	return f.pagina, f.errListar
}

func (f *fakeGastos) ExisteNaCategoria(_ context.Context, userID, categoriaID string) (bool, error) {
	f.registrar("ExisteNaCategoria", userID)
	f.id = categoriaID
	return f.emUso, f.err
}

func (f *fakeGastos) ResumoMensal(_ context.Context, userID, mes string) (expcontract.ResumoMensalOut, error) {
	f.registrar("ResumoMensal", userID)
	f.mes = mes
	return f.resumo, f.errResumo
}

func montarGastos(t *testing.T, cats *fakeCategorias, gastos *fakeGastos) (http.Handler, string) {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Tokens: v, Me: &fakeMe{}, Categorias: cats, Gastos: gastos,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return h, emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
}

func lerGasto(t *testing.T, body []byte) gastoOut {
	t.Helper()
	var g gastoOut
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("corpo = %s", body)
	}
	return g
}

func TestLancarGastoDeCartao(t *testing.T) {
	// Cenário "Gasto de cartão": o grupo vem da categoria, não do cliente.
	cats, gastos := &fakeCategorias{lista: listaPadrao()}, &fakeGastos{}
	h, tok := montarGastos(t, cats, gastos)

	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","descricao":"Almoço"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	want := expcontract.GastoIn{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO", Descricao: "Almoço"}
	if gastos.in != want || gastos.userID != "u-1" || cats.id != "alimentacao" {
		t.Errorf("in = %+v, user %q, categoria %q", gastos.in, gastos.userID, cats.id)
	}
	g := lerGasto(t, rec.Body.Bytes())
	if g.Grupo != "CARTAO" || g.Categoria != (categoriaResumo{ID: "alimentacao", Nome: "Alimentação", Cor: "#E4572E"}) {
		t.Errorf("gasto = %+v", g)
	}
}

func TestLancarGastoFixoRecorrente(t *testing.T) {
	gastos := &fakeGastos{}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":150000,"data":"2026-11-05","categoriaId":"moradia","recorrente":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.in.Grupo != "FIXO" || !gastos.in.Recorrente {
		t.Errorf("in = %+v", gastos.in)
	}
}

func TestLancarComCategoriaInvalida(t *testing.T) {
	// Cenário "Categoria de outro usuário": recusa como se não existisse, com
	// erro de validação no campo categoriaId, sem chamar o expenses-service.
	casos := map[string]string{
		"de outro usuário": `{"valorCentavos":100,"data":"2026-11-03","categoriaId":"c-de-outro"}`,
		"vazia":            `{"valorCentavos":100,"data":"2026-11-03","categoriaId":""}`,
		"ausente":          `{"valorCentavos":100,"data":"2026-11-03"}`,
		"longa":            `{"valorCentavos":100,"data":"2026-11-03","categoriaId":"` + strings.Repeat("c", maxID+1) + `"}`,
	}
	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			gastos := &fakeGastos{}
			h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
			req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, corpo)
			p := problema(t, rec)
			if rec.Code != http.StatusUnprocessableEntity || p.Code != "validacao" || len(p.Errors) != 1 || p.Errors[0].Field != "categoriaId" {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			conforme(t, req, rec)
			if gastos.chamou() != "" {
				t.Errorf("o expenses-service foi chamado: %s", gastos.chamou())
			}
		})
	}
}

func TestLancarCorpoInvalido(t *testing.T) {
	casos := map[string]string{
		"vazio":          ``,
		"valor com casa": `{"valorCentavos":45.9,"data":"2026-11-03","categoriaId":"alimentacao"}`,
		"valor em texto": `{"valorCentavos":"4590","data":"2026-11-03","categoriaId":"alimentacao"}`,
		"grupo do front": `{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","grupo":"FIXO"}`,
		"campo a mais":   `{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","userId":"u-2"}`,
	}
	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			cats, gastos := &fakeCategorias{lista: listaPadrao()}, &fakeGastos{}
			h, tok := montarGastos(t, cats, gastos)
			_, rec := comCorpo(h, http.MethodPost, "/gastos", tok, corpo)
			if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "corpo_invalido" {
				t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if gastos.chamou() != "" || cats.chamada != "" {
				t.Errorf("serviços chamados: %s / %s", gastos.chamou(), cats.chamada)
			}
		})
	}
}

func TestValidacaoDoExpensesService(t *testing.T) {
	// Cenário "Valor inválido": o 422 do serviço chega com errors[].
	gastos := &fakeGastos{err: errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: "valorCentavos", Message: "deve ser maior que zero"})}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":0,"data":"2026-11-03","categoriaId":"alimentacao"}`)
	p := problema(t, rec)
	if rec.Code != http.StatusUnprocessableEntity || len(p.Errors) != 1 || p.Errors[0].Field != "valorCentavos" {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
}

func TestEditarTrocaDeGrupo(t *testing.T) {
	// Cenário "Troca de categoria entre grupos": Alimentação → Moradia.
	gastos := &fakeGastos{}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	id := "20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"
	req, rec := comCorpo(h, http.MethodPut, "/gastos/"+id, tok, `{"valorCentavos":9990,"data":"2026-10-31","categoriaId":"moradia"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.id != id || gastos.in.Grupo != "FIXO" || gastos.in.CategoriaID != "moradia" {
		t.Errorf("chamada = %q %+v", gastos.id, gastos.in)
	}
	// A data mudou: a resposta traz o ID novo.
	if g := lerGasto(t, rec.Body.Bytes()); g.ID == id || g.Grupo != "FIXO" || g.Categoria.Nome != "Moradia" {
		t.Errorf("gasto = %+v", g)
	}
}

func TestGastoAlheio(t *testing.T) {
	// Cenário "Acesso a gasto alheio": o 404 do serviço chega ao cliente.
	naoEncontrado := errs.NotFound(expcontract.CodigoNaoEncontrado, "Gasto não encontrado.")
	id := "20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"
	casos := []struct{ method, path, corpo string }{
		{http.MethodPut, "/gastos/" + id, `{"valorCentavos":1,"data":"2026-11-03","categoriaId":"alimentacao"}`},
		{http.MethodDelete, "/gastos/" + id, ""},
		{http.MethodGet, "/telas/gasto-form?id=" + id, ""},
	}
	for _, c := range casos {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, &fakeGastos{err: naoEncontrado})
			req, rec := comCorpo(h, c.method, c.path, tok, c.corpo)
			if rec.Code != http.StatusNotFound || problema(t, rec).Code != expcontract.CodigoNaoEncontrado {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			conforme(t, req, rec)
		})
	}
}

func TestExcluirGasto(t *testing.T) {
	gastos := &fakeGastos{}
	h, tok := montarGastos(t, &fakeCategorias{}, gastos)
	req, rec := fazer(h, http.MethodDelete, "/gastos/g-1", tok)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.chamou() != "Excluir" || gastos.id != "g-1" || gastos.userID != "u-1" {
		t.Errorf("chamada = %s %q", gastos.chamou(), gastos.id)
	}
	_, rec = fazer(h, http.MethodDelete, "/gastos/"+strings.Repeat("g", maxID+1), tok)
	if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "id_invalido" {
		t.Errorf("id longo: status = %d", rec.Code)
	}
	_, rec = comCorpo(h, http.MethodPut, "/gastos/"+strings.Repeat("g", maxID+1), tok, `{}`)
	if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "id_invalido" {
		t.Errorf("PUT id longo: status = %d", rec.Code)
	}
}

func TestTelaGastos(t *testing.T) {
	gastos := &fakeGastos{pagina: expcontract.PaginaGastos{
		Items: []expcontract.Gasto{
			{ID: "g-2", ValorCentavos: 150000, Data: "2026-11-05", Grupo: "FIXO", CategoriaID: "moradia", Recorrente: true},
			{ID: "g-1", ValorCentavos: 4590, Data: "2026-11-03", Grupo: "CARTAO", CategoriaID: "alimentacao", Descricao: "Almoço"},
			{ID: "g-0", ValorCentavos: 10, Data: "2026-11-01", Grupo: "CARTAO", CategoriaID: "c-excluida"},
		},
		NextCursor: "v1.abc.def",
	}}
	cats := &fakeCategorias{lista: listaPadrao()}
	h, tok := montarGastos(t, cats, gastos)

	req, rec := fazer(h, http.MethodGet, "/telas/gastos?mes=2026-11&grupo=CARTAO&categoriaId=alimentacao&cursor=v1.x.y&limit=10", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	want := expcontract.ListarGastosIn{Mes: "2026-11", Grupo: "CARTAO", CategoriaID: "alimentacao", Cursor: "v1.x.y", Limit: 10}
	if gastos.listarIn != want || gastos.userID != "u-1" || cats.userID != "u-1" {
		t.Errorf("listar = %+v", gastos.listarIn)
	}
	var got telaGastos
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Mes != "2026-11" || got.NextCursor != "v1.abc.def" || len(got.Itens) != 3 || len(got.Categorias.Cartao) != 2 {
		t.Fatalf("tela = %s", rec.Body.String())
	}
	if got.Itens[1].Categoria != (categoriaResumo{ID: "alimentacao", Nome: "Alimentação", Cor: "#E4572E"}) {
		t.Errorf("categoria resolvida = %+v", got.Itens[1].Categoria)
	}
	// Categoria excluída depois do gasto: aparece como "Removida".
	if got.Itens[2].Categoria != (categoriaResumo{ID: "c-excluida", Nome: "Removida", Cor: "#9E9E9E"}) {
		t.Errorf("órfã = %+v", got.Itens[2].Categoria)
	}
}

func TestTelaGastosMesVazio(t *testing.T) {
	// Cenário "Mês sem gastos": itens vazio como [] e sem nextCursor.
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, &fakeGastos{})
	req, rec := fazer(h, http.MethodGet, "/telas/gastos?mes=2026-12", tok)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"itens":[]`) || strings.Contains(rec.Body.String(), "nextCursor") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
}

func TestTelaGastosParametrosInvalidos(t *testing.T) {
	casos := map[string]string{
		"sem mês":         "",
		"mês 13":          "?mes=2026-13",
		"mês sem zero":    "?mes=2026-1",
		"grupo inválido":  "?mes=2026-11&grupo=OUTRO",
		"grupo vazio":     "?mes=2026-11&grupo=",
		"categoria vazia": "?mes=2026-11&categoriaId=",
		"limit zero":      "?mes=2026-11&limit=0",
		"limit 101":       "?mes=2026-11&limit=101",
		"limit texto":     "?mes=2026-11&limit=dez",
		"cursor vazio":    "?mes=2026-11&cursor=",
	}
	for nome, q := range casos {
		t.Run(nome, func(t *testing.T) {
			gastos := &fakeGastos{}
			h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
			_, rec := fazer(h, http.MethodGet, "/telas/gastos"+q, tok)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if gastos.chamou() != "" {
				t.Errorf("o serviço foi chamado: %s", gastos.chamou())
			}
		})
	}
}

func TestTelaGastosErros(t *testing.T) {
	// Cursor de outra listagem: o 400 do serviço chega ao cliente.
	gastos := &fakeGastos{errListar: errs.BadRequest("cursor_invalido", "Cursor de paginação inválido.")}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/gastos?mes=2026-11&cursor=v1.a.b", tok)
	if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "cursor_invalido" {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	// Categories-service fora do ar: 500 sem a causa.
	h, tok = montarGastos(t, &fakeCategorias{err: errs.Internal(io.ErrUnexpectedEOF)}, &fakeGastos{})
	_, rec = fazer(h, http.MethodGet, "/telas/gastos?mes=2026-11", tok)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "EOF") {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTelaGastoFormEdicao(t *testing.T) {
	gastos := &fakeGastos{gasto: expcontract.Gasto{ID: "g-1", ValorCentavos: 4590, Data: "2026-11-03", Grupo: "CARTAO", CategoriaID: "c-1", Descricao: "Ração"}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/gasto-form?id=g-1", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	var got telaGastoForm
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Gasto == nil || got.Gasto.ID != "g-1" || got.Gasto.Categoria.Nome != "Pet" || len(got.Categorias.Fixo) != 1 || gastos.id != "g-1" {
		t.Errorf("tela = %s", rec.Body.String())
	}
	_, rec = fazer(h, http.MethodGet, "/telas/gasto-form?id=", tok)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("id vazio: status = %d", rec.Code)
	}
}

func TestExcluirCategoriaEmUso(t *testing.T) {
	cats, gastos := &fakeCategorias{}, &fakeGastos{emUso: true}
	h, tok := montarGastos(t, cats, gastos)
	req, rec := fazer(h, http.MethodDelete, "/categorias/c-1", tok)
	if rec.Code != http.StatusConflict || problema(t, rec).Code != "categoria_em_uso" {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if cats.chamada != "" || gastos.id != "c-1" || gastos.userID != "u-1" {
		t.Errorf("categories chamado: %q; gastos %q", cats.chamada, gastos.id)
	}

	// Sem gastos: exclui.
	cats, gastos = &fakeCategorias{}, &fakeGastos{}
	h, tok = montarGastos(t, cats, gastos)
	if _, rec := fazer(h, http.MethodDelete, "/categorias/c-1", tok); rec.Code != http.StatusNoContent || cats.chamada != "Excluir" {
		t.Errorf("status = %d, chamada %q", rec.Code, cats.chamada)
	}

	// Falha ao checar: não exclui.
	cats, gastos = &fakeCategorias{}, &fakeGastos{err: errs.Internal(io.ErrUnexpectedEOF)}
	h, tok = montarGastos(t, cats, gastos)
	if _, rec := fazer(h, http.MethodDelete, "/categorias/c-1", tok); rec.Code != http.StatusInternalServerError || cats.chamada != "" {
		t.Errorf("status = %d, chamada %q", rec.Code, cats.chamada)
	}
}

func TestGastosExigemToken(t *testing.T) {
	gastos := &fakeGastos{}
	h, _ := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	for _, rota := range rotasGastos {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.Replace(path, "{id}", "g-1", 1)
		req, rec := fazer(h, method, path+"?mes=2026-11", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, quer 401", rota, rec.Code)
		}
		conforme(t, req, rec)
	}
	if gastos.chamou() != "" {
		t.Errorf("o serviço não devia ser chamado sem token (%s)", gastos.chamou())
	}
}

// Sem o expenses-service (Lambda antes da 8.5), as rotas de gastos seguem em
// 501 e a exclusão de categoria não consulta gastos.
func TestGastosSemServico(t *testing.T) {
	cats := &fakeCategorias{lista: listaPadrao()}
	h, tok := montarCategorias(t, cats)
	for _, rota := range rotasGastos {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.Replace(path, "{id}", "g-1", 1)
		_, rec := fazer(h, method, path, tok)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, quer 501", rota, rec.Code)
		}
	}
	if _, rec := fazer(h, http.MethodDelete, "/categorias/c-1", tok); rec.Code != http.StatusNoContent {
		t.Errorf("excluir categoria: status = %d", rec.Code)
	}
}
