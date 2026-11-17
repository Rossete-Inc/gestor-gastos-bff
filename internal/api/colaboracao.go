package api

import (
	"context"
	"net/http"
	"strings"

	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// Rotas de colaboração (spec colaboracao, ADR 0010). As regras (limite,
// e-mail normalizado, convite repetido) são do users-service; o BFF resolve
// quem age, o e-mail verificado do token e a conta ativa.

// ColaboracaoService é o que as rotas usam do users-service;
// *usuarios.Client o satisfaz.
type ColaboracaoService interface {
	ObterAcesso(ctx context.Context, userID, donoID string) (contract.Acesso, error)
	ListarColaboradores(ctx context.Context, donoID string) (contract.Colaboradores, error)
	Convidar(ctx context.Context, donoID string, in contract.ConvidarIn) (contract.Convite, error)
	CancelarConvite(ctx context.Context, donoID, email string) error
	ConvitesRecebidos(ctx context.Context, userID, email string) ([]contract.Convite, error)
	AceitarConvite(ctx context.Context, userID, email, donoID string) (contract.Conta, error)
	RecusarConvite(ctx context.Context, userID, email, donoID string) error
	MudarPapel(ctx context.Context, donoID, userID, papel string) (contract.Colaborador, error)
	RemoverColaborador(ctx context.Context, donoID, userID string) error
	SairDaConta(ctx context.Context, userID, donoID string) error
	MinhasContas(ctx context.Context, userID string) ([]contract.Conta, error)
	NomesDe(ctx context.Context, userID string, ids []string) (map[string]string, error)
}

// Identidade lê e-mail e nome do usuário no Cognito com o access token;
// *cognito.Identidade a satisfaz. O convite vale para o e-mail do token,
// nunca para um e-mail enviado no corpo.
type Identidade interface {
	Dados(ctx context.Context, accessToken string) (email, nome string, err error)
}

// rotasColaboracao são servidas quando Deps.Colaboracao e Deps.Identidade
// estão presentes.
var rotasColaboracao = []string{
	"GET /contas",
	"POST /contas/convites/{donoId}/aceitar",
	"POST /contas/convites/{donoId}/recusar",
	"DELETE /contas/{donoId}/participacao",
	"GET /colaboradores",
	"POST /colaboradores",
	"PATCH /colaboradores/{userId}",
	"DELETE /colaboradores/{userId}",
	"DELETE /colaboradores/convites/{email}",
}

// maxEmail é o tamanho máximo de um e-mail (RFC 5321).
const maxEmail = 254

func (a *api) rotasDeColaboracao(mux *http.ServeMux) {
	mux.Handle("GET /contas", a.autenticado(http.HandlerFunc(a.listarContas)))
	mux.Handle("POST /contas/convites/{donoId}/aceitar", a.autenticado(http.HandlerFunc(a.aceitarConvite)))
	mux.Handle("POST /contas/convites/{donoId}/recusar", a.autenticado(http.HandlerFunc(a.recusarConvite)))
	mux.Handle("DELETE /contas/{donoId}/participacao", a.autenticado(http.HandlerFunc(a.sairDaConta)))
	mux.Handle("GET /colaboradores", a.propriaConta(http.HandlerFunc(a.listarColaboradores)))
	mux.Handle("POST /colaboradores", a.propriaConta(http.HandlerFunc(a.convidar)))
	mux.Handle("PATCH /colaboradores/{userId}", a.propriaConta(http.HandlerFunc(a.mudarPapel)))
	mux.Handle("DELETE /colaboradores/{userId}", a.propriaConta(http.HandlerFunc(a.removerColaborador)))
	mux.Handle("DELETE /colaboradores/convites/{email}", a.propriaConta(http.HandlerFunc(a.cancelarConvite)))
}

// contaOut é a Conta do openapi.
type contaOut struct {
	DonoID   string `json:"donoId"`
	NomeDono string `json:"nomeDono"`
	Papel    string `json:"papel"`
}

// conviteRecebido é o ConviteRecebido do openapi.
type conviteRecebido struct {
	DonoID   string `json:"donoId"`
	NomeDono string `json:"nomeDono"`
	Papel    string `json:"papel"`
	CriadoEm string `json:"criadoEm"`
}

// telaContas é o TelaContas do openapi: a própria conta primeiro, depois as
// contas em que o usuário colabora, e os convites recebidos.
type telaContas struct {
	Contas   []contaOut        `json:"contas"`
	Convites []conviteRecebido `json:"convites"`
}

func (a *api) listarContas(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	var email, nome string
	var contas []contract.Conta
	var convites []contract.Convite
	err := emParalelo(
		func() (err error) {
			if email, nome, err = a.deps.Identidade.Dados(r.Context(), bearer(r)); err != nil {
				return err
			}
			convites, err = a.deps.Colaboracao.ConvitesRecebidos(r.Context(), c.UserID, email)
			return err
		},
		func() (err error) { contas, err = a.deps.Colaboracao.MinhasContas(r.Context(), c.UserID); return err },
	)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	out := telaContas{
		Contas:   []contaOut{{DonoID: c.UserID, NomeDono: nome, Papel: contract.PapelDono}},
		Convites: make([]conviteRecebido, 0, len(convites)),
	}
	for _, k := range contas {
		out.Contas = append(out.Contas, contaOut(k))
	}
	for _, cv := range convites {
		out.Convites = append(out.Convites, conviteRecebido{
			DonoID: cv.DonoID, NomeDono: cv.NomeDono, Papel: cv.Papel, CriadoEm: cv.CriadoEm.UTC().Format(formatoInstante),
		})
	}
	escreverJSON(w, http.StatusOK, out)
}

// formatoInstante é o ISO 8601 UTC dos instantes no contrato.
const formatoInstante = "2006-01-02T15:04:05.000Z"

// responderConvite lê o dono do caminho e o e-mail verificado do token.
func (a *api) responderConvite(r *http.Request) (userID, email, donoID string, err error) {
	c, _ := auth.FromContext(r.Context())
	if donoID, err = idValido(r.PathValue("donoId")); err != nil {
		return "", "", "", err
	}
	email, _, err = a.deps.Identidade.Dados(r.Context(), bearer(r))
	return c.UserID, email, donoID, err
}

func (a *api) aceitarConvite(w http.ResponseWriter, r *http.Request) {
	userID, email, donoID, err := a.responderConvite(r)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	k, err := a.deps.Colaboracao.AceitarConvite(r.Context(), userID, email, donoID)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, contaOut(k))
}

func (a *api) recusarConvite(w http.ResponseWriter, r *http.Request) {
	userID, email, donoID, err := a.responderConvite(r)
	if err == nil {
		err = a.deps.Colaboracao.RecusarConvite(r.Context(), userID, email, donoID)
	}
	if err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) sairDaConta(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	donoID, err := idValido(r.PathValue("donoId"))
	if err == nil {
		err = a.deps.Colaboracao.SairDaConta(r.Context(), c.UserID, donoID)
	}
	if err != nil {
		a.erro(w, r, err)
		return
	}
	a.acessos.esquecer(c.UserID, donoID)
	w.WriteHeader(http.StatusNoContent)
}

// colaboradoresOut é o TelaColaboradores do openapi.
type colaboradoresOut struct {
	Membros  []colaboradorOut `json:"membros"`
	Convites []conviteEnviado `json:"convites"`
	Limite   int              `json:"limite"`
}

// colaboradorOut é o Colaborador do openapi.
type colaboradorOut struct {
	UserID string `json:"userId"`
	Nome   string `json:"nome"`
	Email  string `json:"email"`
	Papel  string `json:"papel"`
	Desde  string `json:"desde"`
}

// conviteEnviado é o ConviteEnviado do openapi.
type conviteEnviado struct {
	Email    string `json:"email"`
	Papel    string `json:"papel"`
	CriadoEm string `json:"criadoEm"`
}

func colaborador(m contract.Colaborador) colaboradorOut {
	return colaboradorOut{UserID: m.UserID, Nome: m.Nome, Email: m.Email, Papel: m.Papel, Desde: m.Desde.UTC().Format(formatoInstante)}
}

func enviado(cv contract.Convite) conviteEnviado {
	return conviteEnviado{Email: cv.Email, Papel: cv.Papel, CriadoEm: cv.CriadoEm.UTC().Format(formatoInstante)}
}

func (a *api) listarColaboradores(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	l, err := a.deps.Colaboracao.ListarColaboradores(r.Context(), c.UserID)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	out := colaboradoresOut{
		Membros:  make([]colaboradorOut, 0, len(l.Membros)),
		Convites: make([]conviteEnviado, 0, len(l.Convites)),
		Limite:   contract.MaxColaboradores,
	}
	for _, m := range l.Membros {
		out.Membros = append(out.Membros, colaborador(m))
	}
	for _, cv := range l.Convites {
		out.Convites = append(out.Convites, enviado(cv))
	}
	escreverJSON(w, http.StatusOK, out)
}

// conviteIn é o ConviteIn do openapi.
type conviteIn struct {
	Email string `json:"email"`
	Papel string `json:"papel"`
}

func (a *api) convidar(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	var in conviteIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	cv, err := a.deps.Colaboracao.Convidar(r.Context(), c.UserID, contract.ConvidarIn(in))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusCreated, enviado(cv))
}

// papelIn é o PapelIn do openapi.
type papelIn struct {
	Papel string `json:"papel"`
}

func (a *api) mudarPapel(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	userID, err := idValido(r.PathValue("userId"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	var in papelIn
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	m, err := a.deps.Colaboracao.MudarPapel(r.Context(), c.UserID, userID, in.Papel)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	a.acessos.esquecer(userID, c.UserID)
	escreverJSON(w, http.StatusOK, colaborador(m))
}

func (a *api) removerColaborador(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	userID, err := idValido(r.PathValue("userId"))
	if err == nil {
		err = a.deps.Colaboracao.RemoverColaborador(r.Context(), c.UserID, userID)
	}
	if err != nil {
		a.erro(w, r, err)
		return
	}
	a.acessos.esquecer(userID, c.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) cancelarConvite(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	email := strings.TrimSpace(r.PathValue("email"))
	var err error
	if email == "" || len(email) > maxEmail {
		err = errs.NotFound(contract.CodigoConviteNaoEncontrado, "Convite não encontrado.")
	} else {
		err = a.deps.Colaboracao.CancelarConvite(r.Context(), c.UserID, email)
	}
	if err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Autoria dos gastos (spec colaboracao, "Autoria dos lançamentos").

// pessoa é a Pessoa do openapi: um autor de gasto ou um membro da conta.
type pessoa struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

// nomeExColaborador aparece para o autor cujo perfil não existe mais.
const nomeExColaborador = "Ex-colaborador"

// maxNomesPorChamada é o limite de IDs do NomesDe.
const maxNomesPorChamada = 100

// nomes mapeia userId para o nome; nil = autoria desligada (sem o
// users-service), e os gastos saem sem lancadoPor.
type nomes map[string]string

func (n nomes) pessoa(id string) *pessoa {
	if n == nil || id == "" {
		return nil
	}
	nome, ok := n[id]
	if !ok {
		nome = nomeExColaborador
	}
	return &pessoa{ID: id, Nome: nome}
}

// autor é quem lançou o gasto: os anteriores à autoria são do dono.
func autor(g expcontract.Gasto, donoID string) string {
	if g.CriadoPor != "" {
		return g.CriadoPor
	}
	return donoID
}

// comAutoria preenche lancadoPor e editadoPor.
func (n nomes) comAutoria(out gastoOut, g expcontract.Gasto, donoID string) gastoOut {
	out.LancadoPor = n.pessoa(autor(g, donoID))
	out.EditadoPor = n.pessoa(g.AtualizadoPor)
	return out
}

// nomesDe resolve, em lote, os nomes que faltam em conhecidos: o dono, os
// autores e os editores dos gastos.
func (a *api) nomesDe(ctx context.Context, conta contaAtiva, gs []expcontract.Gasto, conhecidos map[string]string) (nomes, error) {
	if a.deps.Colaboracao == nil {
		return nil, nil
	}
	n := nomes{}
	for id, nome := range conhecidos {
		n[id] = nome
	}
	var faltam []string
	vistos := map[string]bool{}
	pedir := func(id string) {
		if _, ok := n[id]; id != "" && !ok && !vistos[id] {
			vistos[id] = true
			faltam = append(faltam, id)
		}
	}
	pedir(conta.DonoID)
	for _, g := range gs {
		pedir(autor(g, conta.DonoID))
		pedir(g.AtualizadoPor)
	}
	for len(faltam) > 0 {
		lote := faltam[:min(len(faltam), maxNomesPorChamada)]
		faltam = faltam[len(lote):]
		achados, err := a.deps.Colaboracao.NomesDe(ctx, conta.AutorID, lote)
		if err != nil {
			return nil, err
		}
		for id, nome := range achados {
			n[id] = nome
		}
	}
	return n, nil
}
