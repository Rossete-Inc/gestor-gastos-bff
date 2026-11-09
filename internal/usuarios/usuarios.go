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
