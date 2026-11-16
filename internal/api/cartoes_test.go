package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Specs cartoes-credito, lancamento-gastos (cartão no lançamento) e
// painel-gastos (totais por cartão e detalhe da fatia).

const (
	idNubank = "01JC8Z6Q3N4W5X6Y7Z8A9B0NUB"
	idInter  = "01JC8Z6Q3N4W5X6Y7Z8A9B0INT"
	idVelho  = "01JC8Z6Q3N4W5X6Y7Z8A9B0VEL"
)

func cartoesPadrao() []expcontract.Cartao {
	return []expcontract.Cartao{
		{ID: idInter, Apelido: "Inter", Cor: "#FF7A00", DiaFechamento: 5, DiaVencimento: 15, LimiteCentavos: 200_000},
		{ID: idNubank, Apelido: "Nubank", Cor: "#8A05BE", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000},
		{ID: idVelho, Apelido: "Antigo", Cor: "#000000", DiaFechamento: 1, DiaVencimento: 10, LimiteCentavos: 1, Arquivado: true},
	}
}

// montarComRelogio é o montarGastos com o relógio fixo em agora.
func montarComRelogio(t *testing.T, gastos *fakeGastos, agora time.Time) (http.Handler, string) {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Tokens: v, Me: &fakeMe{}, Categorias: &fakeCategorias{lista: listaPadrao()}, Gastos: gastos,
		Agora: func() time.Time { return agora },
		Log:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return h, emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
}

func TestGastoDeCartaoExigeCartao(t *testing.T) {
	// Cenário "Gasto de cartão sem cartão".
	gastos := &fakeGastos{cartoes: cartoesPadrao()}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":100,"data":"2026-11-03","categoriaId":"alimentacao"}`)
	p := problema(t, rec)
	if rec.Code != http.StatusUnprocessableEntity || len(p.Errors) != 1 || p.Errors[0].Field != "cartaoId" {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if c := gastos.chamou(); c != "ListarCartoes" {
		t.Errorf("chamadas = %s", c)
	}
}

func TestGastoComCartaoResolvido(t *testing.T) {
	// Cenários "Gasto de cartão" e "Gasto fixo no cartão": o cartão é
	// repassado, e a resposta traz o resumo dele e o mês de referência.
	gastos := &fakeGastos{cartoes: cartoesPadrao()}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	for _, corpo := range []string{
		`{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","cartaoId":"` + idNubank + `"}`,
		`{"valorCentavos":3990,"data":"2026-11-10","categoriaId":"moradia","cartaoId":"` + idNubank + `"}`,
	} {
		req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, corpo)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		conforme(t, req, rec)
		g := lerGasto(t, rec.Body.Bytes())
		if gastos.in.CartaoID != idNubank || g.Cartao == nil || *g.Cartao != (cartaoResumo{ID: idNubank, Apelido: "Nubank", Cor: "#8A05BE"}) || g.MesReferencia != "2026-12" {
			t.Errorf("gasto = %+v (in %+v)", g, gastos.in)
		}
	}

	// Cenário "Gasto fixo sem cartão": aceito, sem cartão e no mês da data.
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":150000,"data":"2026-11-05","categoriaId":"moradia"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if g := lerGasto(t, rec.Body.Bytes()); g.Cartao != nil || g.MesReferencia != "2026-11" {
		t.Errorf("fixo sem cartão = %+v", g)
	}

	// Cartão arquivado ou inexistente: o 422 do serviço chega ao cliente.
	gastos.err = errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: "cartaoId", Message: "cartão arquivado"})
	req, rec = comCorpo(h, http.MethodPost, "/gastos", tok, `{"valorCentavos":1,"data":"2026-11-03","categoriaId":"alimentacao","cartaoId":"`+idVelho+`"}`)
	if p := problema(t, rec); rec.Code != http.StatusUnprocessableEntity || p.Errors[0].Field != "cartaoId" {
		t.Errorf("arquivado = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
}

func TestTelaGastosFiltroPorCartao(t *testing.T) {
	gastos := &fakeGastos{cartoes: cartoesPadrao(), pagina: expcontract.PaginaGastos{Items: []expcontract.Gasto{
		{ID: "g-1", ValorCentavos: 100, Data: "2026-11-03", MesReferencia: "2026-12", Grupo: "CARTAO", CategoriaID: "alimentacao", CartaoID: idNubank},
		{ID: "g-2", ValorCentavos: 200, Data: "2026-11-04", MesReferencia: "2026-12", Grupo: "CARTAO", CategoriaID: "c-1", CartaoID: "01JC8Z6Q3N4W5X6Y7Z8A9B0SUM"},
	}}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/gastos?mes=2026-12&cartaoId=SEM_CARTAO", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.listarIn.CartaoID != expcontract.SemCartao {
		t.Errorf("filtro repassado = %+v", gastos.listarIn)
	}
	var tela telaGastos
	if err := json.Unmarshal(rec.Body.Bytes(), &tela); err != nil {
		t.Fatal(err)
	}
	if len(tela.Cartoes) != 3 || tela.Itens[0].Cartao.Apelido != "Nubank" || tela.Itens[1].Cartao.Apelido != apelidoCartaoRemovido {
		t.Errorf("tela = %+v", tela)
	}

	if _, rec := fazer(h, http.MethodGet, "/telas/gastos?mes=2026-12&cartaoId=", tok); rec.Code != http.StatusBadRequest {
		t.Errorf("cartaoId vazio = %d", rec.Code)
	}
}

func TestTelaGastoFormCartoes(t *testing.T) {
	// Formulário novo: só os cartões ativos.
	gastos := &fakeGastos{cartoes: cartoesPadrao()}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/gasto-form", tok)
	conforme(t, req, rec)
	var tela telaGastoForm
	_ = json.Unmarshal(rec.Body.Bytes(), &tela)
	if len(tela.Cartoes) != 2 {
		t.Errorf("novo = %+v", tela.Cartoes)
	}

	// Edição de um gasto do cartão arquivado: ele continua na lista.
	gastos.gasto = expcontract.Gasto{ID: "g-1", ValorCentavos: 1, Data: "2026-11-03", MesReferencia: "2026-11", Grupo: "CARTAO", CategoriaID: "alimentacao", CartaoID: idVelho}
	req, rec = fazer(h, http.MethodGet, "/telas/gasto-form?id=g-1", tok)
	conforme(t, req, rec)
	tela = telaGastoForm{}
	_ = json.Unmarshal(rec.Body.Bytes(), &tela)
	if len(tela.Cartoes) != 3 || tela.Gasto == nil || tela.Gasto.Cartao.Apelido != "Antigo" {
		t.Errorf("edição = %+v", tela)
	}
}

func TestDashboardPorCartaoEDetalhe(t *testing.T) {
	// Cenários "Totais por cartão", "Agrupamento por cartão" e "Detalhe da
	// fatia do gráfico": Alimentação R$ 420,00 = Nubank 300 + Inter 120.
	gastos := &fakeGastos{cartoes: cartoesPadrao(), resumo: expcontract.ResumoMensalOut{
		Mes: "2026-12", TotalCentavos: 192_000,
		TotaisPorGrupo: []expcontract.TotalGrupo{{Grupo: "CARTAO", TotalCentavos: 42_000}, {Grupo: "FIXO", TotalCentavos: 150_000}},
		TotaisPorCategoria: []expcontract.TotalCategoria{
			{CategoriaID: "moradia", Grupo: "FIXO", TotalCentavos: 150_000},
			{CategoriaID: "alimentacao", Grupo: "CARTAO", TotalCentavos: 42_000},
		},
		TotaisPorCartao: []expcontract.TotalCartao{
			{CartaoID: "", TotalCentavos: 140_000},
			{CartaoID: idNubank, TotalCentavos: 40_000},
			{CartaoID: idInter, TotalCentavos: 12_000},
		},
		TotaisPorCategoriaECartao: []expcontract.TotalCategoriaCartao{
			{CategoriaID: "alimentacao", Grupo: "CARTAO", CartaoID: idNubank, TotalCentavos: 30_000},
			{CategoriaID: "alimentacao", Grupo: "CARTAO", CartaoID: idInter, TotalCentavos: 12_000},
			{CategoriaID: "moradia", Grupo: "FIXO", CartaoID: "", TotalCentavos: 140_000},
			{CategoriaID: "moradia", Grupo: "FIXO", CartaoID: idNubank, TotalCentavos: 10_000},
		},
		Recentes: []expcontract.Gasto{},
	}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-12", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	d := lerDashboard(t, rec.Body.Bytes())

	quero := []totalCartao{
		{Nome: "Sem cartão", Cor: corSemCartao, TotalCentavos: 140_000},
		{CartaoID: idNubank, Nome: "Nubank", Cor: "#8A05BE", TotalCentavos: 40_000},
		{CartaoID: idInter, Nome: "Inter", Cor: "#FF7A00", TotalCentavos: 12_000},
	}
	if len(d.TotaisPorCartao) != 3 || d.TotaisPorCartao[0] != quero[0] || d.TotaisPorCartao[1] != quero[1] || d.TotaisPorCartao[2] != quero[2] {
		t.Errorf("totais por cartão = %+v", d.TotaisPorCartao)
	}
	if len(d.Pizza.Cartoes) != 3 || somaDecimos(d.Pizza.Cartoes) != 1000 || d.Pizza.Cartoes[1].CartaoID != idNubank {
		t.Errorf("pizza por cartão = %+v", d.Pizza.Cartoes)
	}
	// Visão por cartão: o detalhe divide por grupo.
	nubank := d.Pizza.Cartoes[1].Detalhe
	if len(nubank) != 2 || nubank[0] != (parteFatia{Rotulo: "Cartão", TotalCentavos: 30_000}) || nubank[1] != (parteFatia{Rotulo: "Fixo", TotalCentavos: 10_000}) {
		t.Errorf("detalhe do Nubank = %+v", nubank)
	}
	// Visão por categoria: o detalhe divide por cartão.
	var alimentacao fatia
	for _, f := range d.Pizza.Cartao {
		if f.CategoriaID == "alimentacao" {
			alimentacao = f
		}
	}
	if alimentacao.Grupo != "CARTAO" || len(alimentacao.Detalhe) != 2 ||
		alimentacao.Detalhe[0] != (parteFatia{Rotulo: "Nubank", Cor: "#8A05BE", TotalCentavos: 30_000}) ||
		alimentacao.Detalhe[1] != (parteFatia{Rotulo: "Inter", Cor: "#FF7A00", TotalCentavos: 12_000}) {
		t.Errorf("detalhe de Alimentação = %+v", alimentacao)
	}
	// Visão por grupo: Fixo dividido em "Sem cartão" e Nubank.
	if f := d.Pizza.Grupos[0]; f.Grupo != "FIXO" || len(f.Detalhe) != 2 || f.Detalhe[0].Rotulo != rotuloSemCartao {
		t.Errorf("grupo Fixo = %+v", f)
	}
}

func TestRotasDeCartoes(t *testing.T) {
	k := cartoesPadrao()[1]
	gastos := &fakeGastos{cartoes: cartoesPadrao(), cartao: k}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)

	req, rec := fazer(h, http.MethodGet, "/cartoes", tok)
	conforme(t, req, rec)
	var lista listaCartoes
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil || len(lista.Itens) != 3 {
		t.Errorf("listar = %s", rec.Body.String())
	}

	corpo := `{"apelido":"Nubank","cor":"#8A05BE","diaFechamento":25,"diaVencimento":5,"limiteCentavos":500000}`
	req, rec = comCorpo(h, http.MethodPost, "/cartoes", tok, corpo)
	if rec.Code != http.StatusCreated || gastos.cartaoIn.DiaFechamento != 25 || gastos.userID != "u-1" {
		t.Errorf("criar = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	req, rec = comCorpo(h, http.MethodPut, "/cartoes/"+idNubank, tok, corpo)
	if rec.Code != http.StatusOK || gastos.id != idNubank {
		t.Errorf("editar = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	req, rec = comCorpo(h, http.MethodPatch, "/cartoes/"+idNubank, tok, `{"arquivado":true}`)
	if rec.Code != http.StatusOK || !gastos.arquivado {
		t.Errorf("arquivar = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if _, rec = comCorpo(h, http.MethodPatch, "/cartoes/"+idNubank, tok, `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("patch sem arquivado = %d", rec.Code)
	}
	if _, rec = comCorpo(h, http.MethodPost, "/cartoes", tok, `{"apelido":"x","userId":"u-2"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("campo fora do contrato = %d", rec.Code)
	}

	// Cenário "Excluir cartão com gastos": o 409 do serviço chega ao app.
	gastos.errCartao = errs.Conflict(expcontract.CodigoCartaoEmUso, "Este cartão tem gastos vinculados. Arquive-o em vez de excluir.")
	req, rec = fazer(h, http.MethodDelete, "/cartoes/"+idNubank, tok)
	if p := problema(t, rec); rec.Code != http.StatusConflict || p.Code != expcontract.CodigoCartaoEmUso {
		t.Errorf("excluir em uso = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	gastos.errCartao = nil
	req, rec = fazer(h, http.MethodDelete, "/cartoes/"+idInter, tok)
	if rec.Code != http.StatusNoContent {
		t.Errorf("excluir = %d", rec.Code)
	}
	conforme(t, req, rec)
}

func TestTelaCartoesHojeNoBrasil(t *testing.T) {
	// 01:00 UTC de 17/11 ainda é 16/11 em Brasília.
	gastos := &fakeGastos{resumoCartoes: expcontract.ResumoCartoesOut{Items: []expcontract.ResumoCartao{{
		Cartao: cartoesPadrao()[1], FaturaCentavos: 120_000, LimiteUsadoCentavos: 123_000,
		LimiteDisponivelCentavos: 377_000, Vencimento: "2026-12-05",
	}}}}
	h, tok := montarComRelogio(t, gastos, time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC))
	req, rec := fazer(h, http.MethodGet, "/telas/cartoes?mes=2026-12", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	var tela telaCartoes
	if err := json.Unmarshal(rec.Body.Bytes(), &tela); err != nil {
		t.Fatal(err)
	}
	if gastos.hoje != "2026-11-16" || tela.Hoje != "2026-11-16" || tela.Mes != "2026-12" || len(tela.Cartoes) != 1 || tela.Cartoes[0].LimiteDisponivelCentavos != 377_000 {
		t.Errorf("tela = %+v (hoje repassado %s)", tela, gastos.hoje)
	}
	if _, rec := fazer(h, http.MethodGet, "/telas/cartoes?mes=2026-13", tok); rec.Code != http.StatusBadRequest {
		t.Errorf("mês inválido = %d", rec.Code)
	}
}
