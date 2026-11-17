package usuarios

import (
	"context"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// servicoColab imita as operações de colaboração e anota o que recebeu.
func servicoColab(chamadas *[]string) invoke.Transport {
	r := invoke.NewRouter(nil)
	anota := func(s string) { *chamadas = append(*chamadas, s) }
	r.Handle(contract.OpObterAcesso, invoke.Typed(func(_ context.Context, u string, in contract.ObterAcessoIn) (contract.Acesso, error) {
		anota("acesso " + u + " " + in.DonoID)
		return contract.Acesso{DonoID: in.DonoID, Papel: contract.PapelEditor}, nil
	}))
	r.Handle(contract.OpListarColaboradores, invoke.Typed(func(_ context.Context, u string, _ struct{}) (contract.Colaboradores, error) {
		anota("listar " + u)
		return contract.Colaboradores{Membros: []contract.Colaborador{{UserID: "b"}}}, nil
	}))
	r.Handle(contract.OpConvidar, invoke.Typed(func(_ context.Context, u string, in contract.ConvidarIn) (contract.Convite, error) {
		anota("convidar " + u + " " + in.Email + " " + in.Papel)
		return contract.Convite{DonoID: u, Email: in.Email}, nil
	}))
	r.Handle(contract.OpCancelarConvite, invoke.Typed(func(_ context.Context, u string, in contract.CancelarConviteIn) (struct{}, error) {
		anota("cancelar " + u + " " + in.Email)
		return struct{}{}, nil
	}))
	r.Handle(contract.OpListarConvitesRecebidos, invoke.Typed(func(_ context.Context, u string, in contract.ListarConvitesRecebidosIn) (contract.ConvitesRecebidos, error) {
		anota("recebidos " + u + " " + in.Email)
		return contract.ConvitesRecebidos{Items: []contract.Convite{{DonoID: "a"}}}, nil
	}))
	r.Handle(contract.OpAceitarConvite, invoke.Typed(func(_ context.Context, u string, in contract.ResponderConviteIn) (contract.Conta, error) {
		anota("aceitar " + u + " " + in.DonoID + " " + in.Email)
		return contract.Conta{DonoID: in.DonoID}, nil
	}))
	r.Handle(contract.OpRecusarConvite, invoke.Typed(func(_ context.Context, u string, in contract.ResponderConviteIn) (struct{}, error) {
		anota("recusar " + u + " " + in.DonoID + " " + in.Email)
		return struct{}{}, nil
	}))
	r.Handle(contract.OpMudarPapel, invoke.Typed(func(_ context.Context, u string, in contract.MudarPapelIn) (contract.Colaborador, error) {
		anota("papel " + u + " " + in.UserID + " " + in.Papel)
		return contract.Colaborador{UserID: in.UserID, Papel: in.Papel}, nil
	}))
	r.Handle(contract.OpRemoverColaborador, invoke.Typed(func(_ context.Context, u string, in contract.RemoverColaboradorIn) (struct{}, error) {
		anota("remover " + u + " " + in.UserID)
		return struct{}{}, nil
	}))
	r.Handle(contract.OpSairDaConta, invoke.Typed(func(_ context.Context, u string, in contract.SairDaContaIn) (struct{}, error) {
		anota("sair " + u + " " + in.DonoID)
		return struct{}{}, nil
	}))
	r.Handle(contract.OpListarMinhasContas, invoke.Typed(func(_ context.Context, u string, _ struct{}) (contract.MinhasContas, error) {
		anota("contas " + u)
		return contract.MinhasContas{Items: []contract.Conta{{DonoID: "a"}}}, nil
	}))
	r.Handle(contract.OpNomesDe, invoke.Typed(func(_ context.Context, u string, in contract.NomesDeIn) (contract.Nomes, error) {
		anota("nomes " + u)
		return contract.Nomes{Nomes: map[string]string{in.IDs[0]: "Ana"}}, nil
	}))
	return transporte{r}
}

func TestClientColaboracao(t *testing.T) {
	ctx := context.Background()
	var chamadas []string
	c := New(servicoColab(&chamadas))

	if a, err := c.ObterAcesso(ctx, "b", "a"); err != nil || a.Papel != contract.PapelEditor {
		t.Errorf("acesso = %+v, %v", a, err)
	}
	if l, err := c.ListarColaboradores(ctx, "a"); err != nil || len(l.Membros) != 1 {
		t.Errorf("listar = %+v, %v", l, err)
	}
	if cv, err := c.Convidar(ctx, "a", contract.ConvidarIn{Email: "b@x.com", Papel: contract.PapelLeitor}); err != nil || cv.Email != "b@x.com" {
		t.Errorf("convidar = %+v, %v", cv, err)
	}
	if err := c.CancelarConvite(ctx, "a", "b@x.com"); err != nil {
		t.Error(err)
	}
	if r, err := c.ConvitesRecebidos(ctx, "b", "b@x.com"); err != nil || len(r) != 1 {
		t.Errorf("recebidos = %+v, %v", r, err)
	}
	if ct, err := c.AceitarConvite(ctx, "b", "b@x.com", "a"); err != nil || ct.DonoID != "a" {
		t.Errorf("aceitar = %+v, %v", ct, err)
	}
	if err := c.RecusarConvite(ctx, "b", "b@x.com", "a"); err != nil {
		t.Error(err)
	}
	if col, err := c.MudarPapel(ctx, "a", "b", contract.PapelLeitor); err != nil || col.Papel != contract.PapelLeitor {
		t.Errorf("papel = %+v, %v", col, err)
	}
	if err := c.RemoverColaborador(ctx, "a", "b"); err != nil {
		t.Error(err)
	}
	if err := c.SairDaConta(ctx, "b", "a"); err != nil {
		t.Error(err)
	}
	if cs, err := c.MinhasContas(ctx, "b"); err != nil || len(cs) != 1 {
		t.Errorf("contas = %+v, %v", cs, err)
	}
	if n, err := c.NomesDe(ctx, "a", []string{"a"}); err != nil || n["a"] != "Ana" {
		t.Errorf("nomes = %+v, %v", n, err)
	}

	esperadas := []string{
		"acesso b a", "listar a", "convidar a b@x.com LEITOR", "cancelar a b@x.com", "recebidos b b@x.com",
		"aceitar b a b@x.com", "recusar b a b@x.com", "papel a b LEITOR", "remover a b", "sair b a", "contas b", "nomes a",
	}
	if len(chamadas) != len(esperadas) {
		t.Fatalf("chamadas = %q", chamadas)
	}
	for i, e := range esperadas {
		if chamadas[i] != e {
			t.Errorf("chamada %d = %q, esperava %q", i, chamadas[i], e)
		}
	}
}
