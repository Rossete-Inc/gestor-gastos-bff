package categorias

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// servico imita o categories-service atrás do invoke.Router e guarda o
// último envelope recebido.
type servico struct {
	r       *invoke.Router
	ultimo  invoke.Request
	listar  contract.CategoriasPorGrupo
	criadas map[string]contract.Categoria
}

func novoServico() *servico {
	s := &servico{r: invoke.NewRouter(nil), criadas: map[string]contract.Categoria{}}
	s.r.Handle(contract.OpListarCategorias, invoke.Typed(func(_ context.Context, _ string, _ struct{}) (contract.CategoriasPorGrupo, error) {
		return s.listar, nil
	}))
	s.r.Handle(contract.OpObterCategoria, invoke.Typed(func(_ context.Context, _ string, in contract.ObterCategoriaIn) (contract.Categoria, error) {
		if c, ok := s.criadas[in.ID]; ok {
			return c, nil
		}
		return contract.Categoria{}, errs.NotFound(contract.CodigoNaoEncontrada, "Categoria não encontrada.")
	}))
	s.r.Handle(contract.OpCriarCategoria, invoke.Typed(func(_ context.Context, _ string, in contract.CriarCategoriaIn) (contract.Categoria, error) {
		c := contract.Categoria{ID: "c-1", Nome: in.Nome, Grupo: in.Grupo, Cor: in.Cor}
		s.criadas[c.ID] = c
		return c, nil
	}))
	s.r.Handle(contract.OpExcluirCategoria, invoke.Typed(func(_ context.Context, _ string, in contract.ExcluirCategoriaIn) (struct{}, error) {
		if in.ID == "alimentacao" {
			return struct{}{}, errs.Forbidden(contract.CodigoPadrao, "Categoria padrão não pode ser excluída.")
		}
		delete(s.criadas, in.ID)
		return struct{}{}, nil
	}))
	return s
}

func (s *servico) Send(ctx context.Context, req invoke.Request) (invoke.Response, error) {
	s.ultimo = req
	return s.r.Dispatch(ctx, req), nil
}

func TestListarCompletaGruposVazios(t *testing.T) {
	s := novoServico()
	s.listar = contract.CategoriasPorGrupo{Cartao: []contract.Categoria{{ID: "alimentacao"}}}
	got, err := New(s).Listar(context.Background(), "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cartao) != 1 || got.Fixo == nil || len(got.Fixo) != 0 {
		t.Errorf("listar = %+v", got)
	}
	if s.ultimo.Operation != contract.OpListarCategorias || s.ultimo.UserID != "u-1" {
		t.Errorf("envelope = %+v", s.ultimo)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"CARTAO":[{"id":"alimentacao","nome":"","grupo":"","cor":"","padrao":false}],"FIXO":[]}` {
		t.Errorf("json = %s", b)
	}
}

func TestCriarObterExcluir(t *testing.T) {
	ctx := context.Background()
	s := novoServico()
	c := New(s)

	criada, err := c.Criar(ctx, "u-1", contract.CriarCategoriaIn{Nome: "Pet", Grupo: contract.GrupoCartao, Cor: "#E4572E"})
	if err != nil || criada.ID != "c-1" || criada.Nome != "Pet" {
		t.Fatalf("criar = %+v, %v", criada, err)
	}
	if s.ultimo.UserID != "u-1" || string(s.ultimo.Payload) != `{"nome":"Pet","grupo":"CARTAO","cor":"#E4572E"}` {
		t.Errorf("envelope = %+v (payload %s)", s.ultimo, s.ultimo.Payload)
	}

	obtida, err := c.Obter(ctx, "u-1", "c-1")
	if err != nil || obtida != criada {
		t.Errorf("obter = %+v, %v", obtida, err)
	}

	if err := c.Excluir(ctx, "u-1", "c-1"); err != nil {
		t.Fatal(err)
	}
	if string(s.ultimo.Payload) != `{"id":"c-1"}` {
		t.Errorf("payload = %s", s.ultimo.Payload)
	}
	if _, err := c.Obter(ctx, "u-1", "c-1"); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("depois de excluir: %v", err)
	}
}

func TestRepassaErroDoServico(t *testing.T) {
	err := New(novoServico()).Excluir(context.Background(), "u-1", "alimentacao")
	if !errors.Is(err, errs.Forbidden(contract.CodigoPadrao, "")) {
		t.Errorf("erro = %v", err)
	}
}
