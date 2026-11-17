package api

import (
	"net/http"
	"strings"
	"testing"

	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Spec parcelamentos-recorrencias no BFF: o lançamento repassa o
// parcelamento, a recorrência e a chave de idempotência; a edição e a
// exclusão repassam o escopo.

func TestLancarParcelado(t *testing.T) {
	gastos := &fakeGastos{serie: &expcontract.SerieGasto{ID: "S1", Tipo: expcontract.SerieParcelamento, Numero: 1, Total: 3}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok,
		`{"valorCentavos":120000,"data":"2026-11-10","categoriaId":"alimentacao","cartaoId":"01JC8Z6Q3N4W5X6Y7Z8A9B0CAR",`+
			`"parcelamento":{"modo":"TOTAL","parcelas":3},"chaveIdempotencia":"5f0c1d2e-envio"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	in := gastos.criarIn
	if in.Parcelamento == nil || *in.Parcelamento != (expcontract.ParcelamentoIn{Modo: "TOTAL", Parcelas: 3}) ||
		in.Recorrencia != nil || in.ChaveIdempotencia != "5f0c1d2e-envio" || in.Grupo != "CARTAO" {
		t.Errorf("repasse = %+v", in)
	}
	if g := lerGasto(t, rec.Body.Bytes()); g.Serie == nil || g.Serie.Numero != 1 || g.Serie.Total != 3 {
		t.Errorf("série no gasto = %+v", g.Serie)
	}
}

func TestLancarRecorrente(t *testing.T) {
	gastos := &fakeGastos{serie: &expcontract.SerieGasto{ID: "S2", Tipo: expcontract.SerieRecorrencia, Numero: 1}}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok,
		`{"valorCentavos":150000,"data":"2026-11-05","categoriaId":"moradia","recorrencia":{"mesFinal":"2027-10"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if r := gastos.criarIn.Recorrencia; r == nil || r.MesFinal != "2027-10" || gastos.criarIn.Parcelamento != nil {
		t.Errorf("recorrência = %+v", gastos.criarIn)
	}
}

func TestLancarSerieInvalida(t *testing.T) {
	// O expenses-service valida o parcelamento (parcelas de 2 a 72, parcela
	// atual até o total); o BFF devolve o 422 dele.
	gastos := &fakeGastos{err: errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: "parcelas", Message: "deve ser de 2 a 72"})}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := comCorpo(h, http.MethodPost, "/gastos", tok,
		`{"valorCentavos":100,"data":"2026-11-10","categoriaId":"moradia","parcelamento":{"modo":"TOTAL","parcelas":73}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	// Chave de idempotência longa demais nem chega ao serviço.
	gastos = &fakeGastos{}
	h, tok = montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	_, rec = comCorpo(h, http.MethodPost, "/gastos", tok,
		`{"valorCentavos":100,"data":"2026-11-10","categoriaId":"moradia","chaveIdempotencia":"`+strings.Repeat("k", 129)+`"}`)
	if rec.Code != http.StatusUnprocessableEntity || gastos.chamou() != "" {
		t.Errorf("chave longa: status = %d, chamou %q", rec.Code, gastos.chamou())
	}
}

func TestEscopoNaEdicaoENaExclusao(t *testing.T) {
	gastos := &fakeGastos{}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)

	req, rec := comCorpo(h, http.MethodPut, "/gastos/g-1?escopo=PROXIMAS", tok, `{"valorCentavos":160000,"data":"2027-03-05","categoriaId":"moradia"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.escopo != expcontract.EscopoProximas || gastos.id != "g-1" {
		t.Errorf("PUT repassou %q, %q", gastos.escopo, gastos.id)
	}

	req, rec = fazer(h, http.MethodDelete, "/gastos/g-1?escopo=TODAS", tok)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if gastos.escopo != expcontract.EscopoTodas {
		t.Errorf("DELETE repassou %q", gastos.escopo)
	}

	// Sem escopo, o serviço decide (avulso aceita; série é 422).
	if _, rec = fazer(h, http.MethodDelete, "/gastos/g-1", tok); rec.Code != http.StatusNoContent || gastos.escopo != "" {
		t.Errorf("sem escopo: status = %d, escopo %q", rec.Code, gastos.escopo)
	}

	for _, url := range []string{"/gastos/g-1?escopo=esta", "/gastos/g-1?escopo="} {
		_, rec = fazer(h, http.MethodDelete, url, tok)
		if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "parametro_invalido" {
			t.Errorf("%s: status = %d", url, rec.Code)
		}
		_, rec = comCorpo(h, http.MethodPut, url, tok, `{"valorCentavos":1,"data":"2027-03-05","categoriaId":"moradia"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: status = %d", url, rec.Code)
		}
	}
}

func TestEscopoObrigatorioNaSerie(t *testing.T) {
	gastos := &fakeGastos{err: errs.Validation("Escolha o alcance da alteração.", errs.FieldError{Field: "escopo", Message: "obrigatório"})}
	h, tok := montarGastos(t, &fakeCategorias{lista: listaPadrao()}, gastos)
	req, rec := fazer(h, http.MethodDelete, "/gastos/g-1", tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
}
