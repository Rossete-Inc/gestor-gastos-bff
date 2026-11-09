package me

import (
	"context"
	"errors"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

type fakeUsuarios struct {
	perfis   map[string]contract.Perfil
	obterErr error
	criado   *contract.CriarPerfilIn
}

func (f *fakeUsuarios) ObterPerfil(_ context.Context, id string) (contract.Perfil, error) {
	if f.obterErr != nil {
		return contract.Perfil{}, f.obterErr
	}
	if p, ok := f.perfis[id]; ok {
		return p, nil
	}
	return contract.Perfil{}, errs.NotFound("perfil_nao_encontrado", "Perfil não encontrado.")
}

func (f *fakeUsuarios) CriarPerfil(_ context.Context, id string, in contract.CriarPerfilIn) (contract.Perfil, error) {
	f.criado = &in
	p := contract.Perfil{ID: id, Email: in.Email, Nome: in.Nome, Papel: in.Papel, Status: contract.StatusAtivo}
	f.perfis[id] = p
	return p, nil
}

type fakeIdentidade struct {
	chamadas int
	token    string
	err      error
}

func (f *fakeIdentidade) Dados(_ context.Context, token string) (string, string, error) {
	f.chamadas++
	f.token = token
	return "ana@x.com", "Ana", f.err
}

func TestPerfilExistente(t *testing.T) {
	u := &fakeUsuarios{perfis: map[string]contract.Perfil{"u-1": {ID: "u-1", Nome: "Ana"}}}
	id := &fakeIdentidade{}
	p, err := New(u, id).Obter(context.Background(), auth.Claims{UserID: "u-1"}, "tok")
	if err != nil || p.Nome != "Ana" {
		t.Fatalf("perfil = %+v, %v", p, err)
	}
	if id.chamadas != 0 || u.criado != nil {
		t.Error("perfil existente não deve consultar o Cognito nem criar")
	}
}

func TestPerfilAusenteECriado(t *testing.T) {
	casos := map[string]struct {
		grupos []string
		papel  string
	}{
		"admin":     {[]string{"admin"}, contract.PapelAdmin},
		"cliente":   {[]string{"cliente"}, contract.PapelCliente},
		"sem grupo": {nil, contract.PapelCliente},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			u := &fakeUsuarios{perfis: map[string]contract.Perfil{}}
			id := &fakeIdentidade{}
			p, err := New(u, id).Obter(context.Background(), auth.Claims{UserID: "u-1", Groups: c.grupos}, "tok-1")
			if err != nil {
				t.Fatal(err)
			}
			if id.token != "tok-1" || u.criado == nil {
				t.Fatalf("token = %q, criado = %v", id.token, u.criado)
			}
			if *u.criado != (contract.CriarPerfilIn{Email: "ana@x.com", Nome: "Ana", Papel: c.papel}) {
				t.Errorf("criado = %+v", *u.criado)
			}
			if p.ID != "u-1" || p.Papel != c.papel {
				t.Errorf("perfil = %+v", p)
			}
		})
	}
}

func TestFalhas(t *testing.T) {
	t.Run("users-service fora", func(t *testing.T) {
		u := &fakeUsuarios{perfis: map[string]contract.Perfil{}, obterErr: errs.Internal(errors.New("timeout"))}
		id := &fakeIdentidade{}
		_, err := New(u, id).Obter(context.Background(), auth.Claims{UserID: "u-1"}, "tok")
		if errs.KindOf(err) != errs.KindInternal || id.chamadas != 0 {
			t.Errorf("erro = %v, chamadas ao Cognito = %d", err, id.chamadas)
		}
	})
	t.Run("Cognito recusa o token", func(t *testing.T) {
		u := &fakeUsuarios{perfis: map[string]contract.Perfil{}}
		id := &fakeIdentidade{err: errs.Unauthorized("token_invalido", "x")}
		_, err := New(u, id).Obter(context.Background(), auth.Claims{UserID: "u-1"}, "tok")
		if errs.KindOf(err) != errs.KindUnauthorized || u.criado != nil {
			t.Errorf("erro = %v, criado = %v", err, u.criado)
		}
	})
}
