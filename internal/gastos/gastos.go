// Package gastos é o client do expenses-service, sobre o invoke da
// go-commons. Esconde os nomes das operações e o formato do envelope.
//
// Os erros do serviço (404 gasto_nao_encontrado, 422 validacao, 400
// cursor_invalido) chegam como *errs.Error e são repassados ao cliente do BFF
// sem tradução: os códigos são os do openapi.yaml.
package gastos

import (
	"context"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// Client chama o expenses-service.
type Client struct {
	c *invoke.Client
}

// New cria o client sobre o transporte escolhido pelo RUNTIME (Lambda na AWS,
// HTTP para o cmd/local do serviço).
func New(t invoke.Transport) *Client { return &Client{c: invoke.NewClient(t)} }

// Criar lança um gasto. in.Grupo é o grupo da categoria, que o BFF obtém do
// categories-service.
func (c *Client) Criar(ctx context.Context, userID string, in contract.GastoIn) (contract.Gasto, error) {
	var out contract.Gasto
	err := c.c.Call(ctx, contract.OpCriarGasto, userID, in, &out)
	return out, err
}

// Obter devolve um gasto do usuário.
func (c *Client) Obter(ctx context.Context, userID, id string) (contract.Gasto, error) {
	var out contract.Gasto
	err := c.c.Call(ctx, contract.OpObterGasto, userID, contract.ObterGastoIn{ID: id}, &out)
	return out, err
}

// Atualizar substitui os dados de um gasto. Se a data muda, o gasto devolvido
// tem outro ID.
func (c *Client) Atualizar(ctx context.Context, userID, id string, in contract.GastoIn) (contract.Gasto, error) {
	var out contract.Gasto
	err := c.c.Call(ctx, contract.OpAtualizarGasto, userID, contract.AtualizarGastoIn{ID: id, GastoIn: in}, &out)
	return out, err
}

// Excluir apaga um gasto do usuário.
func (c *Client) Excluir(ctx context.Context, userID, id string) error {
	return c.c.Call(ctx, contract.OpExcluirGasto, userID, contract.ExcluirGastoIn{ID: id}, nil)
}

// Listar devolve uma página dos gastos do mês.
func (c *Client) Listar(ctx context.Context, userID string, in contract.ListarGastosIn) (contract.PaginaGastos, error) {
	var out contract.PaginaGastos
	err := c.c.Call(ctx, contract.OpListarGastos, userID, in, &out)
	if out.Items == nil {
		out.Items = []contract.Gasto{}
	}
	return out, err
}

// ExisteNaCategoria informa se o usuário tem algum gasto na categoria.
func (c *Client) ExisteNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error) {
	var out contract.ExisteGastoNaCategoriaOut
	err := c.c.Call(ctx, contract.OpExisteGastoNaCategoria, userID, contract.ExisteGastoNaCategoriaIn{CategoriaID: categoriaID}, &out)
	return out.Existe, err
}

// ResumoMensal devolve os totais do mês e os 5 gastos mais recentes, para o
// painel. As listas vêm sempre não nulas.
func (c *Client) ResumoMensal(ctx context.Context, userID, mes string) (contract.ResumoMensalOut, error) {
	var out contract.ResumoMensalOut
	err := c.c.Call(ctx, contract.OpResumoMensal, userID, contract.ResumoMensalIn{Mes: mes}, &out)
	if out.TotaisPorGrupo == nil {
		out.TotaisPorGrupo = []contract.TotalGrupo{}
	}
	if out.TotaisPorCategoria == nil {
		out.TotaisPorCategoria = []contract.TotalCategoria{}
	}
	if out.Recentes == nil {
		out.Recentes = []contract.Gasto{}
	}
	return out, err
}
