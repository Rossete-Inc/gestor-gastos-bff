package gastos

import (
	"context"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// servicoDeCartoes imita as operações de cartão do expenses-service.
func servicoDeCartoes() *servico {
	s := &servico{r: invoke.NewRouter(nil)}
	k := contract.Cartao{ID: "k1", Apelido: "Nubank", Cor: "#8A05BE", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000}
	s.r.Handle(contract.OpListarCartoes, invoke.Typed(func(context.Context, string, struct{}) (any, error) {
		// Um serviço que omitisse a lista: o client devolve [] mesmo assim.
		return map[string]any{}, nil
	}))
	s.r.Handle(contract.OpCriarCartao, invoke.Typed(func(_ context.Context, _ string, in contract.CriarCartaoIn) (contract.Cartao, error) {
		c := k
		c.Apelido = in.Apelido
		return c, nil
	}))
	s.r.Handle(contract.OpAtualizarCartao, invoke.Typed(func(_ context.Context, _ string, in contract.AtualizarCartaoIn) (contract.Cartao, error) {
		c := k
		c.DiaFechamento = in.DiaFechamento
		return c, nil
	}))
	s.r.Handle(contract.OpArquivarCartao, invoke.Typed(func(_ context.Context, _ string, in contract.ArquivarCartaoIn) (contract.Cartao, error) {
		c := k
		c.Arquivado = in.Arquivado
		return c, nil
	}))
	s.r.Handle(contract.OpExcluirCartao, invoke.Typed(func(context.Context, string, contract.ExcluirCartaoIn) (any, error) {
		return nil, errs.Conflict(contract.CodigoCartaoEmUso, "em uso")
	}))
	s.r.Handle(contract.OpResumoCartoes, invoke.Typed(func(_ context.Context, _ string, in contract.ResumoCartoesIn) (any, error) {
		return map[string]any{"mes": in.Mes}, nil
	}))
	return s
}

func TestCartoes(t *testing.T) {
	ctx := context.Background()
	s := servicoDeCartoes()
	c := New(s)

	lista, err := c.ListarCartoes(ctx, "u-1")
	if err != nil || lista == nil || len(lista) != 0 || s.ultimo.Operation != contract.OpListarCartoes || s.ultimo.UserID != "u-1" {
		t.Fatalf("listar = %#v, %v (%+v)", lista, err, s.ultimo)
	}

	in := contract.CartaoIn{Apelido: "Inter", Cor: "#FF7A00", DiaFechamento: 5, DiaVencimento: 15, LimiteCentavos: 1}
	k, err := c.CriarCartao(ctx, "u-1", in)
	if err != nil || k.Apelido != "Inter" {
		t.Errorf("criar = %+v, %v", k, err)
	}
	if string(s.ultimo.Payload) != `{"apelido":"Inter","cor":"#FF7A00","diaFechamento":5,"diaVencimento":15,"limiteCentavos":1}` {
		t.Errorf("payload = %s", s.ultimo.Payload)
	}

	if k, err := c.AtualizarCartao(ctx, "u-1", "k1", in); err != nil || k.DiaFechamento != 5 || string(s.ultimo.Payload) != `{"id":"k1","apelido":"Inter","cor":"#FF7A00","diaFechamento":5,"diaVencimento":15,"limiteCentavos":1}` {
		t.Errorf("atualizar = %+v, %v (%s)", k, err, s.ultimo.Payload)
	}
	if k, err := c.ArquivarCartao(ctx, "u-1", "k1", true); err != nil || !k.Arquivado || string(s.ultimo.Payload) != `{"id":"k1","arquivado":true}` {
		t.Errorf("arquivar = %+v, %v (%s)", k, err, s.ultimo.Payload)
	}
	if err := c.ExcluirCartao(ctx, "u-1", "k1"); errs.As(err) == nil || errs.As(err).Code != contract.CodigoCartaoEmUso {
		t.Errorf("excluir em uso = %v", err)
	}

	r, err := c.ResumoCartoes(ctx, "u-1", "2026-12", "2026-11-16")
	if err != nil || r.Mes != "2026-12" || r.Items == nil || string(s.ultimo.Payload) != `{"mes":"2026-12","hoje":"2026-11-16"}` {
		t.Errorf("resumo = %#v, %v (%s)", r, err, s.ultimo.Payload)
	}
}

func TestResumoMensalListasDeCartaoNaoNulas(t *testing.T) {
	r, err := New(novoServico()).ResumoMensal(context.Background(), "u-1", "2026-11")
	if err != nil || r.TotaisPorCartao == nil || r.TotaisPorCategoriaECartao == nil {
		t.Errorf("listas nulas: %#v, %v", r, err)
	}
}
