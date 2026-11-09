// Package authtest emite access tokens no formato do Cognito, assinados por
// uma chave de teste servida num JWKS local. Só é importado por testes.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ClientID é o app client dos tokens emitidos.
const ClientID = "app-client-teste"

// Emissor é um User Pool falso.
type Emissor struct {
	Issuer string
	key    *rsa.PrivateKey
}

// New sobe o JWKS e devolve o emissor; o servidor fecha no fim do teste.
func New(t *testing.T) *Emissor {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "teste", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return &Emissor{Issuer: srv.URL, key: key}
}

// Opcoes ajusta o token.
type Opcoes struct {
	Grupos   []string
	ClientID string        // padrão ClientID
	Validade time.Duration // padrão 15 min; negativo gera token vencido
}

// Token emite um access token para o usuário sub.
func (e *Emissor) Token(t *testing.T, sub string, o Opcoes) string {
	t.Helper()
	if o.ClientID == "" {
		o.ClientID = ClientID
	}
	if o.Validade == 0 {
		o.Validade = 15 * time.Minute
	}
	agora := time.Now()
	claims := jwt.MapClaims{
		"sub": sub, "iss": e.Issuer, "token_use": "access", "client_id": o.ClientID,
		"username": sub, "iat": agora.Add(-time.Minute).Unix(), "exp": agora.Add(o.Validade).Unix(),
	}
	if len(o.Grupos) > 0 {
		claims["cognito:groups"] = o.Grupos
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "teste"
	s, err := tok.SignedString(e.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
