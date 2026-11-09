// Package cognito lê os dados do usuário no Cognito (ou no cognito-local)
// com o próprio access token, pela API GetUser. O access token não traz
// e-mail nem nome, e o /me precisa deles para criar o perfil sob demanda.
package cognito

import (
	"context"
	"errors"
	"fmt"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// API é o subconjunto do client Cognito usado aqui.
type API interface {
	GetUser(ctx context.Context, in *cip.GetUserInput, opt ...func(*cip.Options)) (*cip.GetUserOutput, error)
}

// Identidade implementa me.Identidade.
type Identidade struct{ api API }

// New cria a Identidade.
func New(api API) *Identidade { return &Identidade{api: api} }

// Dados devolve e-mail e nome do dono do token. Token revogado (por exemplo
// depois de um bloqueio com global sign-out) vira 401.
func (i *Identidade) Dados(ctx context.Context, accessToken string) (email, nome string, err error) {
	out, err := i.api.GetUser(ctx, &cip.GetUserInput{AccessToken: aws.String(accessToken)})
	var naoAutorizado *types.NotAuthorizedException
	switch {
	case errors.As(err, &naoAutorizado):
		return "", "", errs.Unauthorized("token_invalido", "Sessão inválida ou expirada. Faça login novamente.").WithCause(err)
	case err != nil:
		return "", "", errs.Internal(fmt.Errorf("cognito GetUser: %w", err))
	}
	for _, a := range out.UserAttributes {
		switch aws.ToString(a.Name) {
		case "email":
			email = aws.ToString(a.Value)
		case "name":
			nome = aws.ToString(a.Value)
		}
	}
	if email == "" || nome == "" {
		return "", "", errs.Internal(errors.New("cognito GetUser: usuário sem email ou name"))
	}
	return email, nome, nil
}
