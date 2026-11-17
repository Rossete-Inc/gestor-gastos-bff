package api

import (
	"net/http"
	"time"

	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Rotas de cartões (spec cartoes-credito). As regras (apelido único, limite
// de 20, fatura e limite usado) ficam no expenses-service (ADR 0009); o BFF
// valida o corpo, repassa os erros e calcula o "hoje" do usuário.

// fusoBrasilia é America/Sao_Paulo, em UTC-3 fixo: o Brasil não tem horário
// de verão desde 2019, e um fuso fixo não depende da base de fusos da Lambda.
var fusoBrasilia = time.FixedZone("BRT", -3*60*60)

func (a *api) rotasDeCartoes(mux *http.ServeMux) {
	mux.Handle("GET /telas/cartoes", a.naConta(http.HandlerFunc(a.telaCartoes)))
	mux.Handle("GET /cartoes", a.naConta(http.HandlerFunc(a.listarCartoes)))
	mux.Handle("POST /cartoes", a.naConta(http.HandlerFunc(a.criarCartao)))
	mux.Handle("PUT /cartoes/{id}", a.naConta(http.HandlerFunc(a.atualizarCartao)))
	mux.Handle("PATCH /cartoes/{id}", a.naConta(http.HandlerFunc(a.arquivarCartao)))
	mux.Handle("DELETE /cartoes/{id}", a.naConta(http.HandlerFunc(a.excluirCartao)))
}

// hoje é a data de hoje no Brasil, no formato AAAA-MM-DD.
func (a *api) hoje() string {
	agora := time.Now
	if a.deps.Agora != nil {
		agora = a.deps.Agora
	}
	return agora().In(fusoBrasilia).Format("2006-01-02")
}

// cartaoIn é o CartaoIn do openapi.
type cartaoIn struct {
	Apelido        string `json:"apelido"`
	Cor            string `json:"cor"`
	DiaFechamento  int    `json:"diaFechamento"`
	DiaVencimento  int    `json:"diaVencimento"`
	LimiteCentavos int64  `json:"limiteCentavos"`
}

func (in cartaoIn) contract() expcontract.CartaoIn {
	return expcontract.CartaoIn{
		Apelido: in.Apelido, Cor: in.Cor, DiaFechamento: in.DiaFechamento,
		DiaVencimento: in.DiaVencimento, LimiteCentavos: in.LimiteCentavos,
	}
}

// cartaoPatch é o CartaoPatch do openapi; o ponteiro distingue false de
// ausente.
type cartaoPatch struct {
	Arquivado *bool `json:"arquivado"`
}

// listaCartoes é o ListaCartoes do openapi.
type listaCartoes struct {
	Itens []expcontract.Cartao `json:"itens"`
}

func (a *api) listarCartoes(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	cartoes, err := a.deps.Gastos.ListarCartoes(r.Context(), c.DonoID)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, listaCartoes{Itens: naoNulo(cartoes)})
}

func (a *api) criarCartao(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	var in cartaoIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	k, err := a.deps.Gastos.CriarCartao(r.Context(), c.DonoID, in.contract())
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusCreated, k)
}

func (a *api) atualizarCartao(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	var in cartaoIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	k, err := a.deps.Gastos.AtualizarCartao(r.Context(), c.DonoID, id, in.contract())
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, k)
}

func (a *api) arquivarCartao(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	var in cartaoPatch
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	if in.Arquivado == nil {
		a.erro(w, r, errs.BadRequest("corpo_invalido", "Corpo da requisição inválido: informe arquivado."))
		return
	}
	k, err := a.deps.Gastos.ArquivarCartao(r.Context(), c.DonoID, id, *in.Arquivado)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, k)
}

func (a *api) excluirCartao(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	if err := a.deps.Gastos.ExcluirCartao(r.Context(), c.DonoID, id); err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// telaCartoes é o TelaCartoes do openapi.
type telaCartoes struct {
	Mes     string                     `json:"mes"`
	Hoje    string                     `json:"hoje"`
	Cartoes []expcontract.ResumoCartao `json:"cartoes"`
}

func (a *api) telaCartoes(w http.ResponseWriter, r *http.Request) {
	c := contaDe(r.Context())
	mes := r.URL.Query().Get("mes")
	if !padraoMes.MatchString(mes) {
		a.erro(w, r, parametroInvalido("mes (AAAA-MM)"))
		return
	}
	hoje := a.hoje()
	res, err := a.deps.Gastos.ResumoCartoes(r.Context(), c.DonoID, mes, hoje)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, telaCartoes{Mes: mes, Hoje: hoje, Cartoes: naoNulo(res.Items)})
}
