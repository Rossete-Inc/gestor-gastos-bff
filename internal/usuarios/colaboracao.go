package usuarios

import (
	"context"

	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// Operações de colaboração (ADR 0010). O userID do envelope é sempre quem
// age; nas operações de gestão, é o dono da conta.

// ObterAcesso devolve o papel de userID na conta de donoID; sem acesso é
// errs.NotFound "conta_nao_encontrada".
func (u *Client) ObterAcesso(ctx context.Context, userID, donoID string) (contract.Acesso, error) {
	var a contract.Acesso
	err := u.c.Call(ctx, contract.OpObterAcesso, userID, contract.ObterAcessoIn{DonoID: donoID}, &a)
	return a, err
}

// ListarColaboradores devolve os membros e os convites pendentes da conta.
func (u *Client) ListarColaboradores(ctx context.Context, donoID string) (contract.Colaboradores, error) {
	var c contract.Colaboradores
	err := u.c.Call(ctx, contract.OpListarColaboradores, donoID, nil, &c)
	return c, err
}

// Convidar cria um convite na conta de donoID.
func (u *Client) Convidar(ctx context.Context, donoID string, in contract.ConvidarIn) (contract.Convite, error) {
	var c contract.Convite
	err := u.c.Call(ctx, contract.OpConvidar, donoID, in, &c)
	return c, err
}

// CancelarConvite apaga o convite pendente para email.
func (u *Client) CancelarConvite(ctx context.Context, donoID, email string) error {
	return u.c.Call(ctx, contract.OpCancelarConvite, donoID, contract.CancelarConviteIn{Email: email}, nil)
}

// ConvitesRecebidos lista os convites pendentes para o e-mail de userID.
func (u *Client) ConvitesRecebidos(ctx context.Context, userID, email string) ([]contract.Convite, error) {
	var c contract.ConvitesRecebidos
	err := u.c.Call(ctx, contract.OpListarConvitesRecebidos, userID, contract.ListarConvitesRecebidosIn{Email: email}, &c)
	return c.Items, err
}

// AceitarConvite torna userID membro da conta de donoID.
func (u *Client) AceitarConvite(ctx context.Context, userID, email, donoID string) (contract.Conta, error) {
	var c contract.Conta
	err := u.c.Call(ctx, contract.OpAceitarConvite, userID, contract.ResponderConviteIn{DonoID: donoID, Email: email}, &c)
	return c, err
}

// RecusarConvite apaga o convite de donoID para email.
func (u *Client) RecusarConvite(ctx context.Context, userID, email, donoID string) error {
	return u.c.Call(ctx, contract.OpRecusarConvite, userID, contract.ResponderConviteIn{DonoID: donoID, Email: email}, nil)
}

// MudarPapel troca o papel do membro userID na conta de donoID.
func (u *Client) MudarPapel(ctx context.Context, donoID, userID, papel string) (contract.Colaborador, error) {
	var c contract.Colaborador
	err := u.c.Call(ctx, contract.OpMudarPapel, donoID, contract.MudarPapelIn{UserID: userID, Papel: papel}, &c)
	return c, err
}

// RemoverColaborador tira userID da conta de donoID.
func (u *Client) RemoverColaborador(ctx context.Context, donoID, userID string) error {
	return u.c.Call(ctx, contract.OpRemoverColaborador, donoID, contract.RemoverColaboradorIn{UserID: userID}, nil)
}

// SairDaConta tira userID da conta de donoID, a pedido do próprio membro.
func (u *Client) SairDaConta(ctx context.Context, userID, donoID string) error {
	return u.c.Call(ctx, contract.OpSairDaConta, userID, contract.SairDaContaIn{DonoID: donoID}, nil)
}

// MinhasContas lista as contas em que userID colabora.
func (u *Client) MinhasContas(ctx context.Context, userID string) ([]contract.Conta, error) {
	var c contract.MinhasContas
	err := u.c.Call(ctx, contract.OpListarMinhasContas, userID, nil, &c)
	return c.Items, err
}

// NomesDe devolve o nome de cada ID com perfil; IDs sem perfil ficam de fora.
func (u *Client) NomesDe(ctx context.Context, userID string, ids []string) (map[string]string, error) {
	var n contract.Nomes
	err := u.c.Call(ctx, contract.OpNomesDe, userID, contract.NomesDeIn{IDs: ids}, &n)
	return n.Nomes, err
}
