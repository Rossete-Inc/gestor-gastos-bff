package gastos

import (
	"context"
	"errors"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// servico imita o expenses-service atrás do invoke.Router e guarda o último
// envelope recebido.
type servico struct {
	r      *invoke.Router
	ultimo invoke.Request
	gastos map[string]contract.Gasto
}

func novoServico() *servico {
	s := &servico{r: invoke.NewRouter(nil), gastos: map[string]contract.Gasto{}}
	naoEncontrado := errs.NotFound(contract.CodigoNaoEncontrado, "Gasto não encontrado.")
	s.r.Handle(contract.OpCriarGasto, invoke.Typed(func(_ context.Context, _ string, in contract.GastoIn) (contract.Gasto, error) {
		g := contract.Gasto{ID: "g-1", ValorCentavos: in.ValorCentavos, Data: in.Data, Grupo: in.Grupo, CategoriaID: in.CategoriaID}
		s.gastos[g.ID] = g
		return g, nil
	}))
	s.r.Handle(contract.OpObterGasto, invoke.Typed(func(_ context.Context, _ string, in contract.ObterGastoIn) (contract.Gasto, error) {
		if g, ok := s.gastos[in.ID]; ok {
			return g, nil
		}
		return contract.Gasto{}, naoEncontrado
	}))
	s.r.Handle(contract.OpAtualizarGasto, invoke.Typed(func(_ context.Context, _ string, in contract.AtualizarGastoIn) (contract.Gasto, error) {
		if _, ok := s.gastos[in.ID]; !ok {
			return contract.Gasto{}, naoEncontrado
		}
		delete(s.gastos, in.ID)
		g := contract.Gasto{ID: "g-2", ValorCentavos: in.ValorCentavos, Data: in.Data, Grupo: in.Grupo, CategoriaID: in.CategoriaID}
		s.gastos[g.ID] = g
		return g, nil
	}))
	s.r.Handle(contract.OpExcluirGasto, invoke.Typed(func(_ context.Context, _ string, in contract.ExcluirGastoIn) (any, error) {
		if _, ok := s.gastos[in.ID]; !ok {
			return nil, naoEncontrado
		}
		delete(s.gastos, in.ID)
		return nil, nil
	}))
	s.r.Handle(contract.OpListarGastos, invoke.Typed(func(_ context.Context, _ string, in contract.ListarGastosIn) (contract.PaginaGastos, error) {
		var out contract.PaginaGastos
		for _, g := range s.gastos {
			out.Items = append(out.Items, g)
		}
		return out, nil
	}))
	s.r.Handle(contract.OpExisteGastoNaCategoria, invoke.Typed(func(_ context.Context, _ string, in contract.ExisteGastoNaCategoriaIn) (contract.ExisteGastoNaCategoriaOut, error) {
		for _, g := range s.gastos {
			if g.CategoriaID == in.CategoriaID {
				return contract.ExisteGastoNaCategoriaOut{Existe: true}, nil
			}
		}
		return contract.ExisteGastoNaCategoriaOut{}, nil
	}))
	return s
}

func (s *servico) Send(ctx context.Context, req invoke.Request) (invoke.Response, error) {
	s.ultimo = req
	return s.r.Dispatch(ctx, req), nil
}

func TestCiclo(t *testing.T) {
	ctx := context.Background()
	s := novoServico()
	c := New(s)

	pg, err := c.Listar(ctx, "u-1", contract.ListarGastosIn{Mes: "2026-11"})
	if err != nil || pg.Items == nil || len(pg.Items) != 0 {
		t.Fatalf("lista vazia = %+v, %v", pg, err)
	}
	if string(s.ultimo.Payload) != `{"mes":"2026-11"}` {
		t.Errorf("payload = %s", s.ultimo.Payload)
	}

	in := contract.GastoIn{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO"}
	g, err := c.Criar(ctx, "u-1", in)
	if err != nil || g.ID != "g-1" || s.ultimo.Operation != contract.OpCriarGasto || s.ultimo.UserID != "u-1" {
		t.Fatalf("criar = %+v, %v (%+v)", g, err, s.ultimo)
	}
	if string(s.ultimo.Payload) != `{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","grupo":"CARTAO","recorrente":false}` {
		t.Errorf("payload = %s", s.ultimo.Payload)
	}

	if lido, err := c.Obter(ctx, "u-1", "g-1"); err != nil || lido != g {
		t.Errorf("obter = %+v, %v", lido, err)
	}
	if existe, err := c.ExisteNaCategoria(ctx, "u-1", "alimentacao"); err != nil || !existe {
		t.Errorf("existe = %v, %v", existe, err)
	}
	if pg, _ := c.Listar(ctx, "u-1", contract.ListarGastosIn{Mes: "2026-11"}); len(pg.Items) != 1 {
		t.Errorf("lista = %+v", pg)
	}

	novo, err := c.Atualizar(ctx, "u-1", "g-1", contract.GastoIn{ValorCentavos: 1, Data: "2026-10-31", CategoriaID: "internet", Grupo: "FIXO"})
	if err != nil || novo.ID != "g-2" || novo.Grupo != "FIXO" {
		t.Fatalf("atualizar = %+v, %v", novo, err)
	}
	if string(s.ultimo.Payload) != `{"id":"g-1","valorCentavos":1,"data":"2026-10-31","categoriaId":"internet","grupo":"FIXO","recorrente":false}` {
		t.Errorf("payload = %s", s.ultimo.Payload)
	}

	if err := c.Excluir(ctx, "u-1", "g-2"); err != nil {
		t.Fatal(err)
	}
	if existe, _ := c.ExisteNaCategoria(ctx, "u-1", "internet"); existe {
		t.Error("ainda em uso depois de excluir")
	}
}

func TestRepassaErroDoServico(t *testing.T) {
	_, err := New(novoServico()).Obter(context.Background(), "u-1", "g-9")
	if !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrado, "")) {
		t.Errorf("erro = %v", err)
	}
}
