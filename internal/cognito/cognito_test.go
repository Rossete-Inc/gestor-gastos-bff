package cognito

import (
	"context"
	"errors"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

type fakeAPI struct {
	attrs map[string]string
	err   error
	token string
}

func (f *fakeAPI) GetUser(_ context.Context, in *cip.GetUserInput, _ ...func(*cip.Options)) (*cip.GetUserOutput, error) {
	f.token = aws.ToString(in.AccessToken)
	if f.err != nil {
		return nil, f.err
	}
	out := &cip.GetUserOutput{}
	for k, v := range f.attrs {
		out.UserAttributes = append(out.UserAttributes, types.AttributeType{Name: aws.String(k), Value: aws.String(v)})
	}
	return out, nil
}

func TestDados(t *testing.T) {
	api := &fakeAPI{attrs: map[string]string{"email": "ana@x.com", "name": "Ana", "sub": "u-1", "email_verified": "true"}}
	email, nome, err := New(api).Dados(context.Background(), "tok")
	if err != nil || email != "ana@x.com" || nome != "Ana" || api.token != "tok" {
		t.Errorf("Dados = %q, %q, %v (token %q)", email, nome, err, api.token)
	}
}

func TestErros(t *testing.T) {
	casos := map[string]struct {
		api  *fakeAPI
		kind errs.Kind
	}{
		"token revogado": {&fakeAPI{err: &types.NotAuthorizedException{Message: aws.String("Access Token has been revoked")}}, errs.KindUnauthorized},
		"Cognito fora":   {&fakeAPI{err: errors.New("timeout")}, errs.KindInternal},
		"sem name":       {&fakeAPI{attrs: map[string]string{"email": "a@x.com"}}, errs.KindInternal},
		"sem email":      {&fakeAPI{attrs: map[string]string{"name": "Ana"}}, errs.KindInternal},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			_, _, err := New(c.api).Dados(context.Background(), "tok")
			if err == nil || errs.KindOf(err) != c.kind {
				t.Errorf("erro = %v", err)
			}
		})
	}
}
