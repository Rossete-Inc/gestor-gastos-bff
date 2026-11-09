// Package me implementa o GET /me: devolve o perfil e, se ele ainda não
// existir, cria na hora (design.md: evento UserConfirmed perdido ou ambiente
// local sem o trigger Post Confirmation).
package me

import (
	"context"
	"errors"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// Usuarios é o que o /me usa do users-service.
type Usuarios interface {
	ObterPerfil(ctx context.Context, userID string) (contract.Perfil, error)
	CriarPerfil(ctx context.Context, userID string, in contract.CriarPerfilIn) (contract.Perfil, error)
}

// Identidade lê e-mail e nome do usuário no Cognito.
type Identidade interface {
	Dados(ctx context.Context, accessToken string) (email, nome string, err error)
}

// Service é o caso de uso do /me.
type Service struct {
	usuarios   Usuarios
	identidade Identidade
}

// New cria o Service.
func New(u Usuarios, i Identidade) *Service { return &Service{usuarios: u, identidade: i} }

var perfilNaoEncontrado = errs.NotFound("perfil_nao_encontrado", "")

// Obter devolve o perfil do dono do token, criando-o se preciso. O papel do
// perfil novo vem dos grupos do Cognito: admin se estiver no grupo admin,
// cliente em qualquer outro caso.
func (s *Service) Obter(ctx context.Context, c auth.Claims, accessToken string) (contract.Perfil, error) {
	p, err := s.usuarios.ObterPerfil(ctx, c.UserID)
	if !errors.Is(err, perfilNaoEncontrado) {
		return p, err
	}

	email, nome, err := s.identidade.Dados(ctx, accessToken)
	if err != nil {
		return contract.Perfil{}, err
	}
	papel := contract.PapelCliente
	if c.IsAdmin() {
		papel = contract.PapelAdmin
	}
	return s.usuarios.CriarPerfil(ctx, c.UserID, contract.CriarPerfilIn{Email: email, Nome: nome, Papel: papel})
}
