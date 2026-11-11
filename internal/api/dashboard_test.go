package api

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func lerDashboard(t *testing.T, body []byte) telaDashboard {
	t.Helper()
	var d telaDashboard
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("corpo = %s", body)
	}
	return d
}

func somaDecimos(fs []fatia) int {
	s := 0
	for _, f := range fs {
		s += f.PercentualDecimos
	}
	return s
}

// resumoNovembro: R$ 301,00 de cartão (Alimentação 225,00, Pet 75,00 e
// 1,00 numa categoria já excluída) e R$ 1.700,00 fixos (Moradia).
func resumoNovembro() expcontract.ResumoMensalOut {
	return expcontract.ResumoMensalOut{
		Mes:           "2026-11",
		TotalCentavos: 200_100,
		TotaisPorGrupo: []expcontract.TotalGrupo{
			{Grupo: "CARTAO", TotalCentavos: 30_100},
			{Grupo: "FIXO", TotalCentavos: 170_000},
		},
		TotaisPorCategoria: []expcontract.TotalCategoria{
			{CategoriaID: "moradia", Grupo: "FIXO", TotalCentavos: 170_000},
			{CategoriaID: "alimentacao", Grupo: "CARTAO", TotalCentavos: 22_500},
			{CategoriaID: "c-1", Grupo: "CARTAO", TotalCentavos: 7_500},
			{CategoriaID: "c-excluida", Grupo: "CARTAO", TotalCentavos: 100},
		},
		Recentes: []expcontract.Gasto{
			{ID: "g-2", ValorCentavos: 170_000, Data: "2026-11-05", Grupo: "FIXO", CategoriaID: "moradia", Recorrente: true},
			{ID: "g-1", ValorCentavos: 22_500, Data: "2026-11-03", Grupo: "CARTAO", CategoriaID: "alimentacao", Descricao: "Mercado"},
			{ID: "g-0", ValorCentavos: 100, Data: "2026-11-01", Grupo: "CARTAO", CategoriaID: "c-excluida"},
		},
	}
}

func TestTelaDashboard(t *testing.T) {
	gastos := &fakeGastos{resumo: resumoNovembro()}
	cats := &fakeCategorias{lista: listaPadrao()}
	h, tok := montarGastos(t, cats, gastos)

	req, rec := fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-11", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.mes != "2026-11" || gastos.userID != "u-1" || cats.userID != "u-1" {
		t.Errorf("chamadas: mês %q, user %q / %q", gastos.mes, gastos.userID, cats.userID)
	}
	d := lerDashboard(t, rec.Body.Bytes())

	// Cenário "Totais por grupo".
	if d.Mes != "2026-11" || d.TotalCentavos != 200_100 ||
		!slices.Equal(d.TotaisPorGrupo, []expcontract.TotalGrupo{{Grupo: "CARTAO", TotalCentavos: 30_100}, {Grupo: "FIXO", TotalCentavos: 170_000}}) {
		t.Errorf("totais = %+v", d)
	}

	// Visão só CARTAO: percentuais sobre o total do grupo, em ordem
	// decrescente, e a categoria excluída como "Removida". Cotas: 747,5;
	// 249,2; 3,3 → 747 + 249 + 3 = 999, e o maior resto (0,5) leva o décimo.
	querCartao := []fatia{
		{CategoriaID: "alimentacao", Nome: "Alimentação", Cor: "#E4572E", TotalCentavos: 22_500, PercentualDecimos: 748},
		{CategoriaID: "c-1", Nome: "Pet", Cor: "#17BEBB", TotalCentavos: 7_500, PercentualDecimos: 249},
		{CategoriaID: "c-excluida", Nome: "Removida", Cor: "#9E9E9E", TotalCentavos: 100, PercentualDecimos: 3},
	}
	if !slices.Equal(d.Pizza.Cartao, querCartao) {
		t.Errorf("CARTAO =\n%+v\nquero\n%+v", d.Pizza.Cartao, querCartao)
	}
	if !slices.Equal(d.Pizza.Fixo, []fatia{{CategoriaID: "moradia", Nome: "Moradia", Cor: "#4C6EF5", TotalCentavos: 170_000, PercentualDecimos: 1000}}) {
		t.Errorf("FIXO = %+v", d.Pizza.Fixo)
	}
	if len(d.Pizza.Todas) != 4 || d.Pizza.Todas[0].CategoriaID != "moradia" || d.Pizza.Todas[3].CategoriaID != "c-excluida" {
		t.Errorf("todas = %+v", d.Pizza.Todas)
	}
	// Cenário "Agrupamento por grupo".
	querGrupos := []fatia{
		{Grupo: "FIXO", Nome: "Fixo", TotalCentavos: 170_000, PercentualDecimos: 850},
		{Grupo: "CARTAO", Nome: "Cartão", TotalCentavos: 30_100, PercentualDecimos: 150},
	}
	if !slices.Equal(d.Pizza.Grupos, querGrupos) {
		t.Errorf("grupos = %+v", d.Pizza.Grupos)
	}
	// Cenário "Percentuais somam 100%", em todas as visões.
	for nome, fs := range map[string][]fatia{"todas": d.Pizza.Todas, "CARTAO": d.Pizza.Cartao, "FIXO": d.Pizza.Fixo, "grupos": d.Pizza.Grupos} {
		if somaDecimos(fs) != 1000 {
			t.Errorf("%s soma %d décimos", nome, somaDecimos(fs))
		}
	}

	if len(d.Ultimos) != 3 || d.Ultimos[0].ID != "g-2" ||
		d.Ultimos[1].Categoria != (categoriaResumo{ID: "alimentacao", Nome: "Alimentação", Cor: "#E4572E"}) ||
		d.Ultimos[2].Categoria.Nome != "Removida" {
		t.Errorf("últimos = %+v", d.Ultimos)
	}
}

func TestTelaDashboardParticipacaoPorCategoria(t *testing.T) {
	// Cenário "Participação por categoria": 750,00 e 250,00 só em CARTAO.
	gastos := &fakeGastos{resumo: expcontract.ResumoMensalOut{
		Mes: "2026-11", TotalCentavos: 100_000,
		TotaisPorGrupo: []expcontract.TotalGrupo{{Grupo: "CARTAO", TotalCentavos: 100_000}, {Grupo: "FIXO"}},
		TotaisPorCategoria: []expcontract.TotalCategoria{
			{CategoriaID: "alimentacao", Grupo: "CARTAO", TotalCentavos: 75_000},
			{CategoriaID: "c-1", Grupo: "CARTAO", TotalCentavos: 25_000},
		},
		Recentes: []expcontract.Gasto{},
	}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-11", tok)
	conforme(t, req, rec)
	d := lerDashboard(t, rec.Body.Bytes())
	if len(d.Pizza.Cartao) != 2 || d.Pizza.Cartao[0].PercentualDecimos != 750 || d.Pizza.Cartao[1].PercentualDecimos != 250 {
		t.Errorf("CARTAO = %+v", d.Pizza.Cartao)
	}
	// FIXO sem gastos: visão vazia, e a pizza por grupo fica com uma fatia.
	if len(d.Pizza.Fixo) != 0 || len(d.Pizza.Grupos) != 1 || d.Pizza.Grupos[0].PercentualDecimos != 1000 {
		t.Errorf("FIXO = %+v, grupos = %+v", d.Pizza.Fixo, d.Pizza.Grupos)
	}
}

func TestTelaDashboardMesmaCategoriaNosDoisGrupos(t *testing.T) {
	// Na visão "todas", a categoria vira uma fatia só, com a soma.
	res := montarDashboard("2026-11", expcontract.ResumoMensalOut{
		TotalCentavos: 750,
		TotaisPorCategoria: []expcontract.TotalCategoria{
			{CategoriaID: "x", Grupo: "FIXO", TotalCentavos: 300},
			{CategoriaID: "y", Grupo: "CARTAO", TotalCentavos: 350},
			{CategoriaID: "x", Grupo: "CARTAO", TotalCentavos: 100},
		},
	}, nil)
	quero := []fatia{
		{CategoriaID: "x", Nome: "Removida", Cor: "#9E9E9E", TotalCentavos: 400, PercentualDecimos: 533},
		{CategoriaID: "y", Nome: "Removida", Cor: "#9E9E9E", TotalCentavos: 350, PercentualDecimos: 467},
	}
	if !slices.Equal(res.Pizza.Todas, quero) {
		t.Errorf("todas = %+v", res.Pizza.Todas)
	}
	if len(res.Pizza.Cartao) != 2 || len(res.Pizza.Fixo) != 1 {
		t.Errorf("visões = %+v", res.Pizza)
	}
}

func TestTelaDashboardMesVazio(t *testing.T) {
	// Cenário "Mês vazio": totais zerados, os dois grupos e listas vazias.
	gastos := &fakeGastos{resumo: expcontract.ResumoMensalOut{
		Mes: "2026-12", TotaisPorGrupo: []expcontract.TotalGrupo{{Grupo: "CARTAO"}, {Grupo: "FIXO"}},
		TotaisPorCategoria: []expcontract.TotalCategoria{}, Recentes: []expcontract.Gasto{},
	}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-12", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	quero := `{"mes":"2026-12","totalCentavos":0,"totaisPorGrupo":[{"grupo":"CARTAO","totalCentavos":0},{"grupo":"FIXO","totalCentavos":0}],"pizza":{"todas":[],"CARTAO":[],"FIXO":[],"grupos":[]},"ultimos":[]}`
	if got := strings.TrimSpace(rec.Body.String()); got != quero {
		t.Errorf("corpo =\n%s\nquero\n%s", got, quero)
	}
}

func TestTelaDashboardGrupoAusenteNoResumo(t *testing.T) {
	// Mesmo que o serviço omitisse um grupo, a tela traz CARTAO e FIXO.
	d := montarDashboard("2026-11", expcontract.ResumoMensalOut{TotaisPorGrupo: []expcontract.TotalGrupo{{Grupo: "FIXO", TotalCentavos: 5}}}, nil)
	if !slices.Equal(d.TotaisPorGrupo, []expcontract.TotalGrupo{{Grupo: "CARTAO"}, {Grupo: "FIXO", TotalCentavos: 5}}) {
		t.Errorf("totais = %+v", d.TotaisPorGrupo)
	}
}

func TestTelaDashboardParametros(t *testing.T) {
	casos := map[string]string{"sem mês": "", "mês 13": "?mes=2026-13", "mês sem zero": "?mes=2026-1", "texto": "?mes=nov"}
	for nome, q := range casos {
		t.Run(nome, func(t *testing.T) {
			gastos := &fakeGastos{}
			h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
			_, rec := fazer(h, http.MethodGet, "/telas/dashboard"+q, tok)
			if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "parametro_invalido" {
				t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if gastos.chamou() != "" {
				t.Errorf("o serviço foi chamado: %s", gastos.chamou())
			}
		})
	}
}

func TestTelaDashboardErros(t *testing.T) {
	// Expenses-service fora do ar: 500 sem a causa.
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, &fakeGastos{errResumo: errs.Internal(io.ErrUnexpectedEOF)})
	req, rec := fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-11", tok)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "EOF") {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	// Categories-service fora do ar.
	h, tok = montarGastos(t, &fakeCategorias{err: errs.Internal(io.ErrUnexpectedEOF)}, &fakeGastos{resumo: resumoNovembro()})
	if _, rec = fazer(h, http.MethodGet, "/telas/dashboard?mes=2026-11", tok); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
	}
}
