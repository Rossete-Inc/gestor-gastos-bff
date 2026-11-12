package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// AdminService é o que as rotas /admin/* usam do users-service;
// *usuarios.Client o satisfaz. O adminID é o sub de quem pede: o serviço usa
// para as regras "a si mesmo".
type AdminService interface {
	ListarUsuarios(ctx context.Context, adminID string, in contract.ListarUsuariosIn) (contract.PaginaUsuarios, error)
	AtualizarUsuario(ctx context.Context, adminID string, in contract.AtualizarUsuarioIn) (contract.Perfil, error)
	ExcluirUsuario(ctx context.Context, adminID, id string) error
}

// rotasAdmin são servidas pelo users-service quando Deps.Admin está presente.
var rotasAdmin = []string{
	"GET /admin/telas/usuarios",
	"PATCH /admin/usuarios/{id}",
	"DELETE /admin/usuarios/{id}",
}

// maxCursor é o tamanho máximo do cursor aceito na query.
const maxCursor = 2048

// rotasDeAdmin registra as rotas. A checagem do grupo admin acontece aqui,
// pelo token; as regras da conta (último admin, a si mesmo, transições)
// ficam no users-service, que responde 409 ou 422.
func (a *api) rotasDeAdmin(mux *http.ServeMux) {
	admin := func(h http.HandlerFunc) http.Handler { return a.autenticado(somenteAdmin(h)) }
	mux.Handle("GET /admin/telas/usuarios", admin(a.telaAdminUsuarios))
	mux.Handle("PATCH /admin/usuarios/{id}", admin(a.atualizarUsuario))
	mux.Handle("DELETE /admin/usuarios/{id}", admin(a.excluirUsuario))
}

// telaAdminUsuarios é o TelaAdminUsuarios do openapi. O users-service devolve
// "items"; a tela usa "itens", como as outras telas do BFF.
type telaAdminUsuarios struct {
	Itens      []contract.Perfil `json:"itens"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

func (a *api) telaAdminUsuarios(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	in, err := paginaDaQuery(r)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	p, err := a.deps.Admin.ListarUsuarios(r.Context(), c.UserID, in)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	itens := p.Items
	if itens == nil {
		itens = []contract.Perfil{}
	}
	escreverJSON(w, http.StatusOK, telaAdminUsuarios{Itens: itens, NextCursor: p.NextCursor})
}

// paginaDaQuery lê cursor e limit de /admin/telas/usuarios. O conteúdo do
// cursor é validado pelo users-service (400 cursor_invalido).
func paginaDaQuery(r *http.Request) (contract.ListarUsuariosIn, error) {
	q := r.URL.Query()
	in := contract.ListarUsuariosIn{Cursor: q.Get("cursor")}
	if q.Has("cursor") && (in.Cursor == "" || len(in.Cursor) > maxCursor) {
		return in, errs.BadRequest(contract.CodigoCursorInvalido, "Cursor de paginação inválido. Volte para a primeira página.")
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

// usuarioPatch é o UsuarioPatch do openapi. O BFF só confere o formato do
// JSON; valores fora do enum e corpo sem campo nenhum voltam 422 do
// users-service.
type usuarioPatch struct {
	Status string `json:"status"`
	Papel  string `json:"papel"`
}

func (a *api) atualizarUsuario(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	var in usuarioPatch
	if err := lerJSON(w, r, &in); err != nil {
		a.erro(w, r, err)
		return
	}
	p, err := a.deps.Admin.AtualizarUsuario(r.Context(), c.UserID, contract.AtualizarUsuarioIn{ID: id, Status: in.Status, Papel: in.Papel})
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, p)
}

func (a *api) excluirUsuario(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, err := idValido(r.PathValue("id"))
	if err != nil {
		a.erro(w, r, err)
		return
	}
	if err := a.deps.Admin.ExcluirUsuario(r.Context(), c.UserID, id); err != nil {
		a.erro(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
