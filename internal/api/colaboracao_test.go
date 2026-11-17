package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

func TestListarContas(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	c.colab.contas = []contract.Conta{{DonoID: "ana", NomeDono: "Ana", Papel: contract.PapelEditor}}
	c.colab.convites = []contract.Convite{{DonoID: "dani", NomeDono: "Dani", Email: "bruno@x.com", Papel: contract.PapelLeitor, CriadoEm: instante}}

	req, rec := c.na("", http.MethodGet, "/contas", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	var tela telaContas
	if err := json.Unmarshal(rec.Body.Bytes(), &tela); err != nil {
		t.Fatal(err)
	}
	want := []contaOut{{"u-1", "Bruno", contract.PapelDono}, {"ana", "Ana", contract.PapelEditor}}
	if len(tela.Contas) != 2 || tela.Contas[0] != want[0] || tela.Contas[1] != want[1] {
		t.Errorf("contas = %+v", tela.Contas)
	}
	if len(tela.Convites) != 1 || tela.Convites[0] != (conviteRecebido{"dani", "Dani", contract.PapelLeitor, "2026-11-17T12:00:00.000Z"}) {
		t.Errorf("convites = %+v", tela.Convites)
	}
	// Os convites são do e-mail do token.
	if !strings.Contains(c.colab.chamou(), "recebidos u-1 bruno@x.com") {
		t.Errorf("chamadas = %s", c.colab.chamou())
	}

	// Sem contas nem convites, as listas vêm vazias, não nulas.
	c.colab.contas, c.colab.convites = nil, nil
	if _, rec := c.na("", http.MethodGet, "/contas", ""); !strings.Contains(rec.Body.String(), `"convites":[]`) {
		t.Errorf("corpo = %s", rec.Body.String())
	}
}

func TestResponderConvite(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	req, rec := c.na("", http.MethodPost, "/contas/convites/ana/aceitar", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("aceitar: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if !strings.Contains(rec.Body.String(), `"donoId":"ana"`) {
		t.Errorf("corpo = %s", rec.Body.String())
	}
	req, rec = c.na("", http.MethodPost, "/contas/convites/ana/recusar", "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("recusar: status = %d", rec.Code)
	}
	conforme(t, req, rec)
	if ch := c.colab.chamou(); !strings.Contains(ch, "aceitar u-1 bruno@x.com ana") || !strings.Contains(ch, "recusar u-1 bruno@x.com ana") {
		t.Errorf("chamadas = %s", ch)
	}

	// Convite de outra pessoa: o users-service responde 404.
	c.colab.err = errs.NotFound(contract.CodigoConviteNaoEncontrado, "Convite não encontrado.")
	for _, acao := range []string{"aceitar", "recusar"} {
		req, rec := c.na("", http.MethodPost, "/contas/convites/ana/"+acao, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", acao, rec.Code)
		}
		conforme(t, req, rec)
	}
	// ID inválido no caminho.
	c.colab.err = nil
	if _, rec := c.na("", http.MethodPost, "/contas/convites/"+strings.Repeat("x", 65)+"/aceitar", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("id longo: status = %d", rec.Code)
	}
}

func TestResponderConviteSemIdentidade(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	h := New(Deps{
		Tokens: mustValidator(t, c), Me: &fakeMe{}, Colaboracao: c.colab,
		Identidade: &fakeIdentidade{err: errs.Unauthorized("token_invalido", "Sessão inválida.")},
	})
	c.h = h
	if _, rec := c.na("", http.MethodPost, "/contas/convites/ana/aceitar", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
	if strings.Contains(c.colab.chamou(), "aceitar") {
		t.Errorf("aceitou sem e-mail: %s", c.colab.chamou())
	}
}

func TestSairDaConta(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	// Põe o acesso no cache.
	if _, rec := c.na("ana", http.MethodGet, "/cartoes", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	req, rec := c.na("", http.MethodDelete, "/contas/ana/participacao", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sair: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	// Depois de sair, a conta da Ana é 404 na hora, sem esperar o cache.
	delete(c.colab.papeis, [2]string{"u-1", "ana"})
	if _, rec := c.na("ana", http.MethodGet, "/cartoes", ""); rec.Code != http.StatusNotFound {
		t.Errorf("depois de sair: status = %d", rec.Code)
	}
	c.colab.err = errs.NotFound(contract.CodigoContaNaoEncontrada, "Conta não encontrada.")
	if _, rec := c.na("", http.MethodDelete, "/contas/ana/participacao", ""); rec.Code != http.StatusNotFound {
		t.Errorf("sair de novo: status = %d", rec.Code)
	}
}

func TestGestaoDosColaboradores(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)
	c.colab.membros = []contract.Colaborador{{UserID: "b", Nome: "Bruno", Email: "bruno@x.com", Papel: contract.PapelEditor, Desde: instante}}
	c.colab.convites = []contract.Convite{{DonoID: "u-1", Email: "carla@x.com", Papel: contract.PapelLeitor, CriadoEm: instante}}

	req, rec := c.na("", http.MethodGet, "/colaboradores", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	var tela colaboradoresOut
	if err := json.Unmarshal(rec.Body.Bytes(), &tela); err != nil {
		t.Fatal(err)
	}
	if tela.Limite != contract.MaxColaboradores || len(tela.Membros) != 1 || tela.Membros[0].Desde != "2026-11-17T12:00:00.000Z" ||
		len(tela.Convites) != 1 || tela.Convites[0] != (conviteEnviado{"carla@x.com", contract.PapelLeitor, "2026-11-17T12:00:00.000Z"}) {
		t.Errorf("tela = %+v", tela)
	}

	req, rec = c.na("", http.MethodPost, "/colaboradores", `{"email":"Dani@x.com","papel":"EDITOR"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("convidar: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	req, rec = c.na("", http.MethodPatch, "/colaboradores/b", `{"papel":"LEITOR"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("papel: status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)

	req, rec = c.na("", http.MethodDelete, "/colaboradores/b", "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("remover: status = %d", rec.Code)
	}
	conforme(t, req, rec)

	req, rec = c.na("", http.MethodDelete, "/colaboradores/convites/carla@x.com", "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("cancelar: status = %d", rec.Code)
	}
	conforme(t, req, rec)

	want := "listar u-1;convidar u-1 Dani@x.com EDITOR;papel u-1 b LEITOR;remover u-1 b;cancelar u-1 carla@x.com"
	if ch := c.colab.chamou(); ch != want {
		t.Errorf("chamadas = %s", ch)
	}
}

func TestGestaoErros(t *testing.T) {
	c := montarConta(t, contract.PapelEditor)

	// Corpo inválido não chega ao serviço.
	for _, rota := range []struct{ method, path, corpo string }{
		{http.MethodPost, "/colaboradores", `{"email":"x@x.com","extra":1}`},
		{http.MethodPatch, "/colaboradores/b", `{`},
	} {
		if _, rec := c.na("", rota.method, rota.path, rota.corpo); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d", rota.method, rota.path, rec.Code)
		}
	}
	for _, rota := range []struct{ method, path string }{
		{http.MethodPatch, "/colaboradores/" + strings.Repeat("x", 65)},
		{http.MethodDelete, "/colaboradores/" + strings.Repeat("x", 65)},
	} {
		if _, rec := c.na("", rota.method, rota.path, `{"papel":"LEITOR"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d", rota.method, rota.path, rec.Code)
		}
	}
	if _, rec := c.na("", http.MethodDelete, "/colaboradores/convites/"+strings.Repeat("x", 255), ""); rec.Code != http.StatusNotFound {
		t.Errorf("e-mail longo: status = %d", rec.Code)
	}
	if c.colab.chamou() != "" {
		t.Errorf("chamadas = %s", c.colab.chamou())
	}

	// Erros do users-service passam adiante.
	casos := []struct {
		err    error
		method string
		path   string
		corpo  string
		status int
	}{
		{errs.Conflict(contract.CodigoLimiteColaboradores, "Limite de 5."), http.MethodPost, "/colaboradores", `{"email":"x@x.com","papel":"EDITOR"}`, http.StatusConflict},
		{errs.Validation("Inválido.", errs.FieldError{Field: "email", Message: "inválido"}), http.MethodPost, "/colaboradores", `{"email":"x","papel":"EDITOR"}`, http.StatusUnprocessableEntity},
		{errs.NotFound(contract.CodigoColaboradorNaoEncontrado, "Não."), http.MethodPatch, "/colaboradores/b", `{"papel":"LEITOR"}`, http.StatusNotFound},
		{errs.NotFound(contract.CodigoColaboradorNaoEncontrado, "Não."), http.MethodDelete, "/colaboradores/b", "", http.StatusNotFound},
		{errs.NotFound(contract.CodigoConviteNaoEncontrado, "Não."), http.MethodDelete, "/colaboradores/convites/x@x.com", "", http.StatusNotFound},
		{errs.Internal(errors.New("fora")), http.MethodGet, "/colaboradores", "", http.StatusInternalServerError},
		{errs.Internal(errors.New("fora")), http.MethodGet, "/contas", "", http.StatusInternalServerError},
	}
	for _, k := range casos {
		c.colab.err = k.err
		req, rec := c.na("", k.method, k.path, k.corpo)
		if rec.Code != k.status {
			t.Errorf("%s %s: status = %d, queria %d", k.method, k.path, rec.Code, k.status)
			continue
		}
		conforme(t, req, rec)
	}
}

func TestColaboradorNaoGerencia(t *testing.T) {
	// Cenário "Colaborador tenta convidar": com a conta da Ana ativa, as
	// rotas de gestão respondem 403.
	c := montarConta(t, contract.PapelEditor)
	for _, rota := range []struct{ method, path, corpo string }{
		{http.MethodGet, "/colaboradores", ""},
		{http.MethodPost, "/colaboradores", `{"email":"x@x.com","papel":"EDITOR"}`},
		{http.MethodPatch, "/colaboradores/b", `{"papel":"LEITOR"}`},
		{http.MethodDelete, "/colaboradores/b", ""},
		{http.MethodDelete, "/colaboradores/convites/x@x.com", ""},
	} {
		req, rec := c.na("ana", rota.method, rota.path, rota.corpo)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d", rota.method, rota.path, rec.Code)
			continue
		}
		conforme(t, req, rec)
		if p := problema(t, rec); p.Code != "somente_dono" {
			t.Errorf("code = %q", p.Code)
		}
	}
	if c.colab.chamou() != "" {
		t.Errorf("chamadas = %s", c.colab.chamou())
	}
	// Com o próprio ID no cabeçalho, vale.
	if _, rec := c.na("u-1", http.MethodGet, "/colaboradores", ""); rec.Code != http.StatusOK {
		t.Errorf("própria conta: status = %d", rec.Code)
	}
}
