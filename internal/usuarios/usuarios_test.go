package usuarios

import (
	"context"
	"errors"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// servico imita o users-service atrás do invoke.Router.
func servico() invoke.Transport {
	perfis := map[string]contract.Perfil{}
	r := invoke.NewRouter(nil)
	r.Handle(contract.OpObterPerfil, invoke.Typed(func(_ context.Context, id string, _ struct{}) (contract.Perfil, error) {
		if p, ok := perfis[id]; ok {
			return p, nil
		}
		return contract.Perfil{}, errs.NotFound("perfil_nao_encontrado", "Perfil não encontrado.")
	}))
	r.Handle(contract.OpCriarPerfil, invoke.Typed(func(_ context.Context, id string, in contract.CriarPerfilIn) (contract.Perfil, error) {
		perfis[id] = contract.Perfil{ID: id, Email: in.Email, Nome: in.Nome, Papel: in.Papel, Status: contract.StatusAtivo}
		return perfis[id], nil
	}))
	return transporte{r}
}

type transporte struct{ r *invoke.Router }

func (t transporte) Send(ctx context.Context, req invoke.Request) (invoke.Response, error) {
	return t.r.Dispatch(ctx, req), nil
}

func TestClient(t *testing.T) {
	ctx := context.Background()
	c := New(servico())

	_, err := c.ObterPerfil(ctx, "u-1")
	if !errors.Is(err, errs.NotFound("perfil_nao_encontrado", "")) {
		t.Fatalf("erro = %v", err)
	}
	criado, err := c.CriarPerfil(ctx, "u-1", contract.CriarPerfilIn{Email: "a@x.com", Nome: "Ana", Papel: contract.PapelAdmin})
	if err != nil || criado.Papel != contract.PapelAdmin {
		t.Fatalf("criado = %+v, %v", criado, err)
	}
	obtido, err := c.ObterPerfil(ctx, "u-1")
	if err != nil || obtido != criado {
		t.Errorf("obtido = %+v, %v", obtido, err)
	}
}
