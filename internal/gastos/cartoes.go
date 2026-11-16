package gastos

import (
	"context"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
)

// Cartões moram no expenses-service (ADR 0009). Os erros (404
// cartao_nao_encontrado, 409 cartao_em_uso e limite_cartoes, 422 validacao)
// são repassados sem tradução.

// ListarCartoes devolve os cartões do usuário, ativos e arquivados, em ordem
// de apelido. A lista vem sempre não nula.
func (c *Client) ListarCartoes(ctx context.Context, userID string) ([]contract.Cartao, error) {
	var out contract.ListarCartoesOut
	err := c.c.Call(ctx, contract.OpListarCartoes, userID, struct{}{}, &out)
	if out.Items == nil {
		out.Items = []contract.Cartao{}
	}
	return out.Items, err
}

// CriarCartao cadastra um cartão.
func (c *Client) CriarCartao(ctx context.Context, userID string, in contract.CartaoIn) (contract.Cartao, error) {
	var out contract.Cartao
	err := c.c.Call(ctx, contract.OpCriarCartao, userID, in, &out)
	return out, err
}

// AtualizarCartao edita um cartão.
func (c *Client) AtualizarCartao(ctx context.Context, userID, id string, in contract.CartaoIn) (contract.Cartao, error) {
	var out contract.Cartao
	err := c.c.Call(ctx, contract.OpAtualizarCartao, userID, contract.AtualizarCartaoIn{ID: id, CartaoIn: in}, &out)
	return out, err
}

// ArquivarCartao arquiva (true) ou reativa (false) um cartão.
func (c *Client) ArquivarCartao(ctx context.Context, userID, id string, arquivado bool) (contract.Cartao, error) {
	var out contract.Cartao
	err := c.c.Call(ctx, contract.OpArquivarCartao, userID, contract.ArquivarCartaoIn{ID: id, Arquivado: arquivado}, &out)
	return out, err
}

// ExcluirCartao exclui um cartão sem gastos.
func (c *Client) ExcluirCartao(ctx context.Context, userID, id string) error {
	return c.c.Call(ctx, contract.OpExcluirCartao, userID, contract.ExcluirCartaoIn{ID: id}, nil)
}

// ResumoCartoes devolve a fatura do mês e o limite de cada cartão na data
// hoje (AAAA-MM-DD). A lista vem sempre não nula.
func (c *Client) ResumoCartoes(ctx context.Context, userID, mes, hoje string) (contract.ResumoCartoesOut, error) {
	var out contract.ResumoCartoesOut
	err := c.c.Call(ctx, contract.OpResumoCartoes, userID, contract.ResumoCartoesIn{Mes: mes, Hoje: hoje}, &out)
	if out.Items == nil {
		out.Items = []contract.ResumoCartao{}
	}
	return out, err
}
