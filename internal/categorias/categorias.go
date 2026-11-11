// Package categorias é o client do categories-service, sobre o invoke da
// go-commons. Esconde os nomes das operações e o formato do envelope.
//
// Os erros do serviço (404 categoria_nao_encontrada, 409 categoria_duplicada,
// 403 categoria_padrao, 422 validacao) chegam como *errs.Error e são
// repassados ao cliente do BFF sem tradução: os códigos são os do openapi.yaml.
package categorias

import (
	"context"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// Client chama o categories-service.
type Client struct {
	c *invoke.Client
}

// New cria o client sobre o transporte escolhido pelo RUNTIME (Lambda na AWS,
// HTTP para o cmd/local do serviço).
func New(t invoke.Transport) *Client { return &Client{c: invoke.NewClient(t)} }

// Listar devolve as categorias visíveis ao usuário (padrão e personalizadas),
// por grupo e em ordem alfabética.
func (c *Client) Listar(ctx context.Context, userID string) (contract.CategoriasPorGrupo, error) {
	var out contract.CategoriasPorGrupo
	err := c.c.Call(ctx, contract.OpListarCategorias, userID, nil, &out)
	return comGruposVazios(out), err
}

// Obter devolve uma categoria visível ao usuário; inexistente ou de outro
// usuário é errs.NotFound "categoria_nao_encontrada".
func (c *Client) Obter(ctx context.Context, userID, id string) (contract.Categoria, error) {
	var out contract.Categoria
	err := c.c.Call(ctx, contract.OpObterCategoria, userID, contract.ObterCategoriaIn{ID: id}, &out)
	return out, err
}

// Criar cria uma categoria personalizada do usuário.
func (c *Client) Criar(ctx context.Context, userID string, in contract.CriarCategoriaIn) (contract.Categoria, error) {
	var out contract.Categoria
	err := c.c.Call(ctx, contract.OpCriarCategoria, userID, in, &out)
	return out, err
}

// Excluir apaga uma categoria personalizada do usuário.
func (c *Client) Excluir(ctx context.Context, userID, id string) error {
	return c.c.Call(ctx, contract.OpExcluirCategoria, userID, contract.ExcluirCategoriaIn{ID: id}, nil)
}

// comGruposVazios garante `[]` em vez de `null` nos grupos, como exige o
// schema CategoriasPorGrupo.
func comGruposVazios(c contract.CategoriasPorGrupo) contract.CategoriasPorGrupo {
	if c.Cartao == nil {
		c.Cartao = []contract.Categoria{}
	}
	if c.Fixo == nil {
		c.Fixo = []contract.Categoria{}
	}
	return c
}
