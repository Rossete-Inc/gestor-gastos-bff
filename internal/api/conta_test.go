package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// fakeColab imita o users-service: papéis por (usuário, dono), membros e
// convites da conta e nomes dos perfis. Anota cada chamada.
type fakeColab struct {
	mu       sync.Mutex
	chamadas []string
	papeis   map[[2]string]string
	membros  []contract.Colaborador
	convites []contract.Convite
	contas   []contract.Conta
	nomes    map[string]string
	err      error // devolvido por todas as operações, se presente
	errNomes error
	lotes    []int
}

func (f *fakeColab) anotar(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chamadas = append(f.chamadas, fmt.Sprintf(format, args...))
}

func (f *fakeColab) chamou() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.chamadas, ";")
}

func (f *fakeColab) ObterAcesso(_ context.Context, userID, donoID string) (contract.Acesso, error) {
	f.anotar("acesso %s %s", userID, donoID)
	if f.err != nil {
		return contract.Acesso{}, f.err
	}
	p, ok := f.papeis[[2]string{userID, donoID}]
	if !ok {
		return contract.Acesso{}, errs.NotFound(contract.CodigoContaNaoEncontrada, "Conta não encontrada.")
	}
	return contract.Acesso{DonoID: donoID, Papel: p}, nil
}

func (f *fakeColab) ListarColaboradores(_ context.Context, donoID string) (contract.Colaboradores, error) {
	f.anotar("listar %s", donoID)
	return contract.Colaboradores{Membros: f.membros, Convites: f.convites}, f.err
}

func (f *fakeColab) Convidar(_ context.Context, donoID string, in contract.ConvidarIn) (contract.Convite, error) {
	f.anotar("convidar %s %s %s", donoID, in.Email, in.Papel)
	return contract.Convite{DonoID: donoID, Email: strings.ToLower(in.Email), Papel: in.Papel, CriadoEm: instante}, f.err
}

func (f *fakeColab) CancelarConvite(_ context.Context, donoID, email string) error {
	f.anotar("cancelar %s %s", donoID, email)
	return f.err
}

func (f *fakeColab) ConvitesRecebidos(_ context.Context, userID, email string) ([]contract.Convite, error) {
	f.anotar("recebidos %s %s", userID, email)
	return f.convites, f.err
}

func (f *fakeColab) AceitarConvite(_ context.Context, userID, email, donoID string) (contract.Conta, error) {
	f.anotar("aceitar %s %s %s", userID, email, donoID)
	return contract.Conta{DonoID: donoID, NomeDono: "Ana", Papel: contract.PapelEditor}, f.err
}

func (f *fakeColab) RecusarConvite(_ context.Context, userID, email, donoID string) error {
	f.anotar("recusar %s %s %s", userID, email, donoID)
	return f.err
}

func (f *fakeColab) MudarPapel(_ context.Context, donoID, userID, papel string) (contract.Colaborador, error) {
	f.anotar("papel %s %s %s", donoID, userID, papel)
	return contract.Colaborador{UserID: userID, Nome: "Bruno", Email: "bruno@x.com", Papel: papel, Desde: instante}, f.err
}

func (f *fakeColab) RemoverColaborador(_ context.Context, donoID, userID string) error {
	f.anotar("remover %s %s", donoID, userID)
	return f.err
}

func (f *fakeColab) SairDaConta(_ context.Context, userID, donoID string) error {
	f.anotar("sair %s %s", userID, donoID)
	return f.err
}

func (f *fakeColab) MinhasContas(_ context.Context, userID string) ([]contract.Conta, error) {
	f.anotar("contas %s", userID)
	return f.contas, f.err
}

func (f *fakeColab) NomesDe(_ context.Context, userID string, ids []string) (map[string]string, error) {
	f.anotar("nomes %s %s", userID, strings.Join(ids, ","))
	f.mu.Lock()
	f.lotes = append(f.lotes, len(ids))
	f.mu.Unlock()
	out := map[string]string{}
	for _, id := range ids {
		if n, ok := f.nomes[id]; ok {
			out[id] = n
		}
	}
	return out, f.errNomes
}

// fakeIdentidade devolve o e-mail e o nome do dono do token.
type fakeIdentidade struct {
	email, nome string
	err         error
}

func (f *fakeIdentidade) Dados(context.Context, string) (string, string, error) {
	return f.email, f.nome, f.err
}

var instante = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// cenarioConta monta a API com a Ana (dona), o Bruno (u-1, quem faz as
// requisições) como editor dela e o relógio controlável.
type cenarioConta struct {
	h       http.Handler
	tok     string
	gastos  *fakeGastos
	cats    *fakeCategorias
	colab   *fakeColab
	agora   time.Time
	emissor *authtest.Emissor
}

func montarConta(t *testing.T, papel string) *cenarioConta {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	c := &cenarioConta{
		gastos: &fakeGastos{cartoes: cartoesPadrao()},
		cats:   &fakeCategorias{lista: listaPadrao()},
		colab: &fakeColab{
			papeis: map[[2]string]string{{"u-1", "ana"}: papel},
			nomes:  map[string]string{"ana": "Ana", "u-1": "Bruno"},
		},
		agora:   instante,
		emissor: emissor,
	}
	c.h = New(Deps{
		Tokens: v, Me: &fakeMe{}, Categorias: c.cats, Gastos: c.gastos,
		Colaboracao: c.colab, Identidade: &fakeIdentidade{email: "bruno@x.com", nome: "Bruno"},
		Agora: func() time.Time { return c.agora },
		Log:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	c.tok = emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
	return c
}

// na faz a requisição com X-Conta (vazio = sem o cabeçalho).
func (c *cenarioConta) na(conta, method, path, corpo string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(corpo))
	req.Header.Set("Authorization", "Bearer "+c.tok)
	if corpo != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if conta != "" {
		req.Header.Set(CabecalhoConta, conta)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return req, rec
}

const corpoGasto = `{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"moradia"}`

const corpoCartao = `{"apelido":"Nubank","cor":"#8A05BE","diaFechamento":25,"diaVencimento":5,"limiteCentavos":500000}`

// rotasDeEscrita são todas as escritas de gastos, cartões e categorias.
var rotasDeEscrita = []struct{ method, path, corpo string }{
	{http.MethodPost, "/gastos", corpoGasto},
	{http.MethodPut, "/gastos/g-1", corpoGasto},
	{http.MethodDelete, "/gastos/g-1", ""},
	{http.MethodPost, "/cartoes", corpoCartao},
	{http.MethodPut, "/cartoes/k-1", corpoCartao},
	{http.MethodPatch, "/cartoes/k-1", `{"arquivado":true}`},
	{http.MethodDelete, "/cartoes/k-1", ""},
	{http.MethodPost, "/categorias", `{"nome":"Escola","grupo":"FIXO","cor":"#17BEBB"}`},
	{http.MethodDelete, "/categorias/c-1", ""},
}

// rotasDeLeitura são as leituras de dados da conta.
var rotasDeLeitura = []string{
	"/telas/dashboard?mes=2026-11",
	"/telas/gastos?mes=2026-11",
	"/telas/gasto-form",
	"/telas/cartoes?mes=2026-11",
	"/cartoes",
	"/categorias",
}

func TestEditorEscreveNaContaDoDono(t *testing.T) {
	// Cenário "Editor lança gasto": o gasto fica na conta da Ana, com o
	// Bruno como autor.
	c := montarConta(t, contract.PapelEditor)
	req, rec := c.na("ana", http.MethodPost, "/gastos", corpoGasto)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if c.gastos.userID != "ana" || c.gastos.in.AutorID != "u-1" {
		t.Errorf("userId = %q, autor = %q", c.gastos.userID, c.gastos.in.AutorID)
	}
	// A categoria foi conferida na conta da Ana.
	if c.cats.userID != "ana" {
		t.Errorf("categoria conferida em %q", c.cats.userID)
	}
	if g := lerGasto(t, rec.Body.Bytes()); g.LancadoPor == nil || *g.LancadoPor != (pessoa{ID: "ana", Nome: "Ana"}) {
		// O fake grava sem CriadoPor: o gasto aparece como do dono.
		t.Errorf("lançado por = %+v", g.LancadoPor)
	}

	// Todas as escritas chegam aos serviços com o userId da Ana.
	for _, rota := range rotasDeEscrita {
		c.gastos.userID, c.cats.userID = "", ""
		_, rec := c.na("ana", rota.method, rota.path, rota.corpo)
		if rec.Code >= 300 {
			t.Errorf("%s %s: status = %d: %s", rota.method, rota.path, rec.Code, rec.Body.String())
			continue
		}
		if c.gastos.userID != "ana" && c.cats.userID != "ana" {
			t.Errorf("%s %s: userId = %q / %q", rota.method, rota.path, c.gastos.userID, c.cats.userID)
		}
	}
}

func TestLeitorSoConsulta(t *testing.T) {
	// Cenário "Leitor tenta lançar": 403 e nada muda.
	c := montarConta(t, contract.PapelLeitor)
	for _, rota := range rotasDeEscrita {
		req, rec := c.na("ana", rota.method, rota.path, rota.corpo)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d", rota.method, rota.path, rec.Code)
			continue
		}
		conforme(t, req, rec)
		if p := problema(t, rec); p.Code != "somente_leitura" {
			t.Errorf("%s %s: code = %q", rota.method, rota.path, p.Code)
		}
	}
	if ch := c.gastos.chamou(); ch != "" || c.cats.chamada != "" {
		t.Errorf("serviços chamados: %q / %q", ch, c.cats.chamada)
	}
	// As leituras funcionam, na conta da Ana.
	for _, path := range rotasDeLeitura {
		req, rec := c.na("ana", http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d: %s", path, rec.Code, rec.Body.String())
			continue
		}
		conforme(t, req, rec)
		if c.gastos.userID != "ana" && c.cats.userID != "ana" {
			t.Errorf("GET %s: userId = %q / %q", path, c.gastos.userID, c.cats.userID)
		}
	}
}

func TestContaAlheiaSemAcesso(t *testing.T) {
	// Cenário "Conta alheia sem convite": 404 e nenhum dado.
	c := montarConta(t, contract.PapelEditor)
	for _, path := range rotasDeLeitura {
		req, rec := c.na("carla", http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d", path, rec.Code)
			continue
		}
		conforme(t, req, rec)
		if p := problema(t, rec); p.Code != contract.CodigoContaNaoEncontrada {
			t.Errorf("GET %s: code = %q", path, p.Code)
		}
	}
	for _, rota := range rotasDeEscrita {
		if _, rec := c.na("carla", rota.method, rota.path, rota.corpo); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d", rota.method, rota.path, rec.Code)
		}
	}
	if ch := c.gastos.chamou(); ch != "" || c.cats.chamada != "" {
		t.Errorf("serviços chamados: %q / %q", ch, c.cats.chamada)
	}
	// X-Conta longo demais nem chega ao users-service.
	antes := c.colab.chamou()
	if _, rec := c.na(strings.Repeat("x", 65), http.MethodGet, "/cartoes", ""); rec.Code != http.StatusNotFound {
		t.Errorf("X-Conta longo: status = %d", rec.Code)
	}
	if c.colab.chamou() != antes {
		t.Errorf("consultou o acesso de um X-Conta inválido")
	}
}

func TestPropriaContaNaoConsulta(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	for _, conta := range []string{"", "u-1", " u-1 "} {
		if _, rec := c.na(conta, http.MethodGet, "/cartoes", ""); rec.Code != http.StatusOK || c.gastos.userID != "u-1" {
			t.Errorf("X-Conta %q: status = %d, userId = %q", conta, rec.Code, c.gastos.userID)
		}
	}
	if strings.Contains(c.colab.chamou(), "acesso") {
		t.Errorf("consultou o acesso da própria conta: %s", c.colab.chamou())
	}
}

func TestCacheDoAcesso(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	contar := func() int { return strings.Count(c.colab.chamou(), "acesso u-1 ana") }

	c.na("ana", http.MethodGet, "/cartoes", "")
	c.agora = c.agora.Add(9 * time.Second)
	c.na("ana", http.MethodGet, "/cartoes", "")
	if n := contar(); n != 1 {
		t.Errorf("consultas em 9 s = %d, queria 1", n)
	}
	// Vencido o prazo, a troca de papel passa a valer.
	c.colab.papeis[[2]string{"u-1", "ana"}] = contract.PapelLeitor
	c.agora = c.agora.Add(2 * time.Second)
	if _, rec := c.na("ana", http.MethodPost, "/cartoes", corpoCartao); rec.Code != http.StatusForbidden {
		t.Errorf("depois do prazo: status = %d", rec.Code)
	}
	if n := contar(); n != 2 {
		t.Errorf("consultas = %d, queria 2", n)
	}

	// Sem acesso não entra no cache: cada requisição consulta.
	c.na("carla", http.MethodGet, "/cartoes", "")
	c.na("carla", http.MethodGet, "/cartoes", "")
	if n := strings.Count(c.colab.chamou(), "acesso u-1 carla"); n != 2 {
		t.Errorf("consultas sem acesso = %d, queria 2", n)
	}
}

func TestFalhaAoConsultarAcesso(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	c.colab.err = errs.Internal(errors.New("users-service fora"))
	if _, rec := c.na("ana", http.MethodGet, "/cartoes", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rec.Code)
	}
	// A falha não fica no cache.
	c.colab.err = nil
	if _, rec := c.na("ana", http.MethodGet, "/cartoes", ""); rec.Code != http.StatusOK {
		t.Errorf("depois da falha: status = %d", rec.Code)
	}
}

func TestSemColaboracaoSoAPropriaConta(t *testing.T) {
	cats, gastos := &fakeCategorias{lista: listaPadrao()}, &fakeGastos{}
	h, tok := montarGastos(t, cats, gastos)
	req := httptest.NewRequest(http.MethodGet, "/cartoes", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(CabecalhoConta, "ana")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
	for _, rota := range rotasColaboracao {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.NewReplacer("{donoId}", "ana", "{userId}", "b", "{email}", "b@x.com").Replace(path)
		_, rec := fazer(h, method, path, tok)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d", rota, rec.Code)
		}
	}
}

func TestAutoriaNaListaDeGastos(t *testing.T) {
	// Cenários "Lançado por colaborador", "Filtro por pessoa" e
	// "Ex-colaborador".
	c := montarConta(t, contract.PapelEditor)
	c.colab.membros = []contract.Colaborador{{UserID: "u-1", Nome: "Bruno", Email: "bruno@x.com", Papel: contract.PapelEditor, Desde: instante}}
	base := expcontract.Gasto{ValorCentavos: 100, Data: "2026-11-03", MesReferencia: "2026-11", Grupo: "FIXO", CategoriaID: "moradia"}
	antigo, doBruno, doEx := base, base, base
	antigo.ID = "g-1"
	doBruno.ID, doBruno.CriadoPor, doBruno.AtualizadoPor = "g-2", "u-1", "ana"
	doEx.ID, doEx.CriadoPor = "g-3", "u-9"
	c.gastos.pagina = expcontract.PaginaGastos{Items: []expcontract.Gasto{antigo, doBruno, doEx}}

	req, rec := c.na("ana", http.MethodGet, "/telas/gastos?mes=2026-11&autorId=u-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if c.gastos.listarIn.AutorID != "u-1" || c.gastos.userID != "ana" {
		t.Errorf("listar = %+v em %q", c.gastos.listarIn, c.gastos.userID)
	}
	var tela telaGastos
	if err := json.Unmarshal(rec.Body.Bytes(), &tela); err != nil {
		t.Fatal(err)
	}
	lancados := []pessoa{{"ana", "Ana"}, {"u-1", "Bruno"}, {"u-9", nomeExColaborador}}
	for i, g := range tela.Itens {
		if g.LancadoPor == nil || *g.LancadoPor != lancados[i] {
			t.Errorf("gasto %d lançado por %+v, queria %+v", i, g.LancadoPor, lancados[i])
		}
	}
	if e := tela.Itens[1].EditadoPor; e == nil || *e != (pessoa{"ana", "Ana"}) || tela.Itens[0].EditadoPor != nil {
		t.Errorf("editado por = %+v / %+v", e, tela.Itens[0].EditadoPor)
	}
	if len(tela.Pessoas) != 2 || tela.Pessoas[0] != (pessoa{"ana", "Ana"}) || tela.Pessoas[1] != (pessoa{"u-1", "Bruno"}) {
		t.Errorf("pessoas = %+v", tela.Pessoas)
	}
	// O nome do membro veio da lista de colaboradores: só o dono e o
	// ex-colaborador foram pedidos ao NomesDe, por quem age.
	if !strings.Contains(c.colab.chamou(), "nomes u-1 ana,u-9") {
		t.Errorf("chamadas = %s", c.colab.chamou())
	}

	for _, q := range []string{"autorId=", "autorId=" + strings.Repeat("x", 65)} {
		if _, rec := c.na("ana", http.MethodGet, "/telas/gastos?mes=2026-11&"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", q, rec.Code)
		}
	}
}

func TestAutoriaFalhas(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	c.colab.errNomes = errs.Internal(errors.New("fora"))
	if _, rec := c.na("", http.MethodGet, "/telas/gastos?mes=2026-11", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("lista: status = %d", rec.Code)
	}
	if _, rec := c.na("", http.MethodPost, "/gastos", corpoGasto); rec.Code != http.StatusInternalServerError {
		t.Errorf("lançamento: status = %d", rec.Code)
	}
}

func TestNomesEmLotes(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	a := &api{deps: Deps{Colaboracao: c.colab}}
	gs := make([]expcontract.Gasto, 150)
	for i := range gs {
		gs[i].CriadoPor = fmt.Sprintf("p-%d", i)
	}
	n, err := a.nomesDe(context.Background(), contaAtiva{DonoID: "ana", AutorID: "u-1"}, gs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.colab.lotes) != 2 || c.colab.lotes[0] != 100 || c.colab.lotes[1] != 51 {
		t.Errorf("lotes = %v", c.colab.lotes)
	}
	if n["ana"] != "Ana" {
		t.Errorf("nomes = %v", n)
	}
	// Sem o users-service, a autoria fica desligada.
	if n, err := (&api{}).nomesDe(context.Background(), contaAtiva{}, gs, nil); n != nil || err != nil {
		t.Errorf("sem colaboração = %v, %v", n, err)
	}
	if p := nomes(nil).pessoa("x"); p != nil {
		t.Errorf("pessoa sem nomes = %+v", p)
	}
}

func mustValidator(t *testing.T, c *cenarioConta) *auth.Validator {
	t.Helper()
	v, err := auth.NewValidator(auth.Config{Issuer: c.emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
