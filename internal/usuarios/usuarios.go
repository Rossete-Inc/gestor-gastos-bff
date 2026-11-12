// Package usuarios é o client do users-service, sobre o invoke da
// go-commons. Esconde os nomes das operações e o formato do envelope.
package usuarios

import (
	"context"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// Client chama o users-service.
type Client struct {
	c *invoke.Client
}

// New cria o client sobre o transporte escolhido pelo RUNTIME (Lambda na AWS,
// HTTP para o cmd/local do serviço).
func New(t invoke.Transport) *Client { return &Client{c: invoke.NewClient(t)} }

// ObterPerfil devolve o perfil do usuário; inexistente é errs.NotFound
// "perfil_nao_encontrado".
func (u *Client) ObterPerfil(ctx context.Context, userID string) (contract.Perfil, error) {
	var p contract.Perfil
	err := u.c.Call(ctx, contract.OpObterPerfil, userID, nil, &p)
	return p, err
}

// CriarPerfil cria o perfil do usuário; é idempotente no serviço.
func (u *Client) CriarPerfil(ctx context.Context, userID string, in contract.CriarPerfilIn) (contract.Perfil, error) {
	var p contract.Perfil
	err := u.c.Call(ctx, contract.OpCriarPerfil, userID, in, &p)
	return p, err
}

// ListarUsuarios devolve uma página de perfis, do mais novo para o mais
// antigo. adminID é o admin que pede.
func (u *Client) ListarUsuarios(ctx context.Context, adminID string, in contract.ListarUsuariosIn) (contract.PaginaUsuarios, error) {
	var p contract.PaginaUsuarios
	err := u.c.Call(ctx, contract.OpListarUsuarios, adminID, in, &p)
	return p, err
}

// AtualizarUsuario bloqueia, reativa ou troca o papel de in.ID. As regras
// (a si mesmo, último admin, transição) são do users-service.
func (u *Client) AtualizarUsuario(ctx context.Context, adminID string, in contract.AtualizarUsuarioIn) (contract.Perfil, error) {
	var p contract.Perfil
	err := u.c.Call(ctx, contract.OpAtualizarUsuario, adminID, in, &p)
	return p, err
}

// ExcluirUsuario exclui a conta id; o users-service publica UserDeleted.
func (u *Client) ExcluirUsuario(ctx context.Context, adminID, id string) error {
	return u.c.Call(ctx, contract.OpExcluirUsuario, adminID, contract.ExcluirUsuarioIn{ID: id}, nil)
}
