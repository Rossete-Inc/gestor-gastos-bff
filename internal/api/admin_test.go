package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// fakeAdmin registra o que a API pediu ao users-service.
type fakeAdmin struct {
	pagina    contract.PaginaUsuarios
	perfil    contract.Perfil
	err       error
	adminID   string
	listar    contract.ListarUsuariosIn
	atualizar contract.AtualizarUsuarioIn
	excluido  string
}

func (f *fakeAdmin) ListarUsuarios(_ context.Context, adminID string, in contract.ListarUsuariosIn) (contract.PaginaUsuarios, error) {
	f.adminID, f.listar = adminID, in
	return f.pagina, f.err
}

func (f *fakeAdmin) AtualizarUsuario(_ context.Context, adminID string, in contract.AtualizarUsuarioIn) (contract.Perfil, error) {
	f.adminID, f.atualizar = adminID, in
	return f.perfil, f.err
}

func (f *fakeAdmin) ExcluirUsuario(_ context.Context, adminID, id string) error {
	f.adminID, f.excluido = adminID, id
	return f.err
}

func perfilBia() contract.Perfil {
	return contract.Perfil{
		ID: "u-2", Email: "bia@x.com", Nome: "Bia", Papel: contract.PapelCliente,
		Status: contract.StatusAtivo, CriadoEm: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
	}
}

// montarAdmin devolve o handler, um token de admin ("adm") e um de cliente.
func montarAdmin(t *testing.T, f *fakeAdmin) (h http.Handler, admin, cliente string) {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	h = New(Deps{Tokens: v, Me: &fakeMe{}, Admin: f, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	return h,
		emissor.Token(t, "adm", authtest.Opcoes{Grupos: []string{"admin"}}),
		emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
}

func TestTelaAdminUsuarios(t *testing.T) {
	f := &fakeAdmin{pagina: contract.PaginaUsuarios{Items: []contract.Perfil{perfilBia(), perfilAna()}, NextCursor: "c2"}}
	h, admin, _ := montarAdmin(t, f)

	req, rec := comCorpo(h, http.MethodGet, "/admin/telas/usuarios?cursor=c1&limit=2", admin, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if f.adminID != "adm" || f.listar != (contract.ListarUsuariosIn{Cursor: "c1", Limit: 2}) {
		t.Errorf("pedido = %q %+v", f.adminID, f.listar)
	}
	corpo := rec.Body.String()
	if !strings.Contains(corpo, `"itens":[{"id":"u-2"`) || !strings.Contains(corpo, `"nextCursor":"c2"`) {
		t.Errorf("corpo = %s", corpo)
	}
}

func TestTelaAdminUsuariosVazia(t *testing.T) {
	h, admin, _ := montarAdmin(t, &fakeAdmin{})
	req, rec := comCorpo(h, http.MethodGet, "/admin/telas/usuarios", admin, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"itens":[]}` {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
}

func TestTelaAdminUsuariosQueryInvalida(t *testing.T) {
	f := &fakeAdmin{}
	h, admin, _ := montarAdmin(t, f)
	casos := map[string]string{
		"?limit=0":                             "parametro_invalido",
		"?limit=101":                           "parametro_invalido",
		"?limit=dez":                           "parametro_invalido",
		"?cursor=":                             contract.CodigoCursorInvalido,
		"?cursor=" + strings.Repeat("a", 2049): contract.CodigoCursorInvalido,
	}
	for q, code := range casos {
		req, rec := comCorpo(h, http.MethodGet, "/admin/telas/usuarios"+q, admin, "")
		if rec.Code != http.StatusBadRequest || problema(t, rec).Code != code {
			t.Errorf("%.30s: status = %d: %s", q, rec.Code, rec.Body.String())
		}
		conforme(t, req, rec)
	}
	if f.adminID != "" {
		t.Error("query inválida chegou ao users-service")
	}
}

func TestAtualizarUsuario(t *testing.T) {
	bloqueada := perfilBia()
	bloqueada.Status = contract.StatusBloqueado
	f := &fakeAdmin{perfil: bloqueada}
	h, admin, _ := montarAdmin(t, f)

	req, rec := comCorpo(h, http.MethodPatch, "/admin/usuarios/u-2", admin, `{"status":"BLOQUEADO","papel":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	quero := contract.AtualizarUsuarioIn{ID: "u-2", Status: contract.StatusBloqueado, Papel: contract.PapelAdmin}
	if f.adminID != "adm" || f.atualizar != quero {
		t.Errorf("pedido = %q %+v", f.adminID, f.atualizar)
	}
	if !strings.Contains(rec.Body.String(), `"status":"BLOQUEADO"`) {
		t.Errorf("corpo = %s", rec.Body.String())
	}
}

func TestAtualizarUsuarioCorpoInvalido(t *testing.T) {
	f := &fakeAdmin{}
	h, admin, _ := montarAdmin(t, f)
	for _, corpo := range []string{"", "[]", `{"status":"BLOQUEADO","nome":"x"}`, `{"status":1}`, `{} {}`} {
		req, rec := comCorpo(h, http.MethodPatch, "/admin/usuarios/u-2", admin, corpo)
		if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "corpo_invalido" {
			t.Errorf("%q: status = %d: %s", corpo, rec.Code, rec.Body.String())
		}
		conforme(t, req, rec)
	}
	if _, rec := comCorpo(h, http.MethodPatch, "/admin/usuarios/"+strings.Repeat("x", 65), admin, `{"status":"ATIVO"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("id longo: status = %d", rec.Code)
	}
	if f.adminID != "" {
		t.Error("corpo inválido chegou ao users-service")
	}
}

func TestExcluirUsuario(t *testing.T) {
	f := &fakeAdmin{}
	h, admin, _ := montarAdmin(t, f)
	req, rec := comCorpo(h, http.MethodDelete, "/admin/usuarios/u-2", admin, "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if f.adminID != "adm" || f.excluido != "u-2" {
		t.Errorf("pedido = %q %q", f.adminID, f.excluido)
	}
	if _, rec := comCorpo(h, http.MethodDelete, "/admin/usuarios/"+strings.Repeat("x", 65), admin, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("id longo: status = %d", rec.Code)
	}
}

// Os erros das regras da conta vêm do users-service e passam como estão.
func TestErrosDoUsersService(t *testing.T) {
	casos := []struct {
		nome, method, path, corpo string
		err                       error
		status                    int
		code                      string
	}{
		{"bloquear a si mesmo", http.MethodPatch, "/admin/usuarios/adm", `{"status":"BLOQUEADO"}`,
			errs.Conflict(contract.CodigoBloquearASiMesmo, "Você não pode bloquear a sua própria conta."), 409, contract.CodigoBloquearASiMesmo},
		{"último admin", http.MethodPatch, "/admin/usuarios/adm", `{"papel":"cliente"}`,
			errs.Conflict(contract.CodigoUltimoAdmin, "O sistema precisa de pelo menos um admin ativo."), 409, contract.CodigoUltimoAdmin},
		{"transição inválida", http.MethodPatch, "/admin/usuarios/u-9", `{"status":"ATIVO"}`,
			errs.Conflict(contract.CodigoTransicaoInvalida, "A conta não pode passar de EXCLUIDO para ATIVO."), 409, contract.CodigoTransicaoInvalida},
		{"validação", http.MethodPatch, "/admin/usuarios/u-2", `{"status":"EXCLUIDO"}`,
			errs.Validation("Dados inválidos.", errs.FieldError{Field: "status", Message: "deve ser ATIVO ou BLOQUEADO"}), 422, "validacao"},
		{"não encontrado", http.MethodPatch, "/admin/usuarios/u-9", `{"status":"ATIVO"}`,
			errs.NotFound(contract.CodigoUsuarioNaoEncontrado, "Usuário não encontrado."), 404, contract.CodigoUsuarioNaoEncontrado},
		{"excluir a si mesmo", http.MethodDelete, "/admin/usuarios/adm", "",
			errs.Conflict(contract.CodigoExcluirASiMesmo, "Você não pode excluir a sua própria conta."), 409, contract.CodigoExcluirASiMesmo},
		{"excluir inexistente", http.MethodDelete, "/admin/usuarios/u-9", "",
			errs.NotFound(contract.CodigoUsuarioNaoEncontrado, "Usuário não encontrado."), 404, contract.CodigoUsuarioNaoEncontrado},
		{"cursor adulterado", http.MethodGet, "/admin/telas/usuarios?cursor=xyz", "",
			errs.BadRequest(contract.CodigoCursorInvalido, "Cursor inválido."), 400, contract.CodigoCursorInvalido},
		{"serviço fora do ar", http.MethodGet, "/admin/telas/usuarios", "",
			errs.Internal(io.ErrUnexpectedEOF), 500, "erro_interno"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			h, admin, _ := montarAdmin(t, &fakeAdmin{err: c.err})
			req, rec := comCorpo(h, c.method, c.path, admin, c.corpo)
			if rec.Code != c.status || problema(t, rec).Code != c.code {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			conforme(t, req, rec)
			if c.status == 500 && strings.Contains(rec.Body.String(), "EOF") {
				t.Errorf("causa interna vazou: %s", rec.Body.String())
			}
		})
	}
}

// Cenário "Cliente tenta acessar área admin", com o users-service ligado: o
// 403 sai antes de qualquer chamada.
func TestClienteNaoChegaAoUsersService(t *testing.T) {
	f := &fakeAdmin{}
	h, _, cliente := montarAdmin(t, f)
	for _, r := range rotasAdmin {
		method, path, _ := strings.Cut(r, " ")
		path = strings.ReplaceAll(path, "{id}", "u-2")
		req, rec := comCorpo(h, method, path, cliente, `{"status":"BLOQUEADO"}`)
		if rec.Code != http.StatusForbidden || problema(t, rec).Code != "somente_admin" {
			t.Errorf("%s: status = %d: %s", r, rec.Code, rec.Body.String())
		}
		conforme(t, req, rec)
		if _, rec := comCorpo(h, method, path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem token: %d", r, rec.Code)
		}
	}
	if f.adminID != "" {
		t.Error("cliente chegou ao users-service")
	}
}
