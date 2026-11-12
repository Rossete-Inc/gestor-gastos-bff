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

// servicoAdmin imita as operações de admin, devolvendo o que recebeu.
func servicoAdmin(t *testing.T) invoke.Transport {
	r := invoke.NewRouter(nil)
	r.Handle(contract.OpListarUsuarios, invoke.Typed(func(_ context.Context, adm string, in contract.ListarUsuariosIn) (contract.PaginaUsuarios, error) {
		if adm != "adm" || in.Cursor != "c1" || in.Limit != 5 {
			t.Errorf("listar: %q %+v", adm, in)
		}
		return contract.PaginaUsuarios{Items: []contract.Perfil{{ID: "u-2"}}, NextCursor: "c2"}, nil
	}))
	r.Handle(contract.OpAtualizarUsuario, invoke.Typed(func(_ context.Context, adm string, in contract.AtualizarUsuarioIn) (contract.Perfil, error) {
		if in.ID == "adm" {
			return contract.Perfil{}, errs.Conflict(contract.CodigoBloquearASiMesmo, "a si mesmo")
		}
		return contract.Perfil{ID: in.ID, Status: in.Status, Papel: in.Papel}, nil
	}))
	r.Handle(contract.OpExcluirUsuario, invoke.Typed(func(_ context.Context, adm string, in contract.ExcluirUsuarioIn) (struct{}, error) {
		if in.ID != "u-2" {
			return struct{}{}, errs.NotFound(contract.CodigoUsuarioNaoEncontrado, "não encontrado")
		}
		return struct{}{}, nil
	}))
	return transporte{r}
}

func TestClientAdmin(t *testing.T) {
	ctx := context.Background()
	c := New(servicoAdmin(t))

	p, err := c.ListarUsuarios(ctx, "adm", contract.ListarUsuariosIn{Cursor: "c1", Limit: 5})
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != "u-2" || p.NextCursor != "c2" {
		t.Errorf("listar = %+v, %v", p, err)
	}
	u, err := c.AtualizarUsuario(ctx, "adm", contract.AtualizarUsuarioIn{ID: "u-2", Status: contract.StatusBloqueado})
	if err != nil || u.Status != contract.StatusBloqueado {
		t.Errorf("atualizar = %+v, %v", u, err)
	}
	if _, err := c.AtualizarUsuario(ctx, "adm", contract.AtualizarUsuarioIn{ID: "adm", Status: contract.StatusBloqueado}); !errors.Is(err, errs.Conflict(contract.CodigoBloquearASiMesmo, "")) {
		t.Errorf("a si mesmo = %v", err)
	}
	if err := c.ExcluirUsuario(ctx, "adm", "u-2"); err != nil {
		t.Errorf("excluir = %v", err)
	}
	if err := c.ExcluirUsuario(ctx, "adm", "u-9"); !errors.Is(err, errs.NotFound(contract.CodigoUsuarioNaoEncontrado, "")) {
		t.Errorf("excluir inexistente = %v", err)
	}
}
