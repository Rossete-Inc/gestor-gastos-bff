// Command local roda o BFF na máquina do desenvolvedor, contra o
// cognito-local do infra-base e o cmd/local de cada serviço (users-service
// na 8082 e categories-service na 8083).
//
//	set -a; source <infra-base>/local/.env.cognito; set +a
//	RUNTIME=local go run ./cmd/local
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/api"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/categorias"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/cognito"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/me"
	"github.com/Rossete-Inc/gestor-gastos-bff/internal/usuarios"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
)

const servico = "bff"

// Config é a configuração do cmd/local.
type Config struct {
	Base            config.Base
	Host            string
	Port            string
	CognitoIssuer   string
	CognitoClientID string
	CognitoJWKSURL  string
	CognitoEndpoint string
	UsersURL        string
	CategoriesURL   string
	CORSOrigins     []string
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=local e endpoints locais.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if !base.IsLocal() {
		return Config{}, errors.New("cmd/local exige RUNTIME=local")
	}
	r := config.NewReader(lookup)
	c := Config{
		Base:            base,
		Host:            r.Get("HOST", "127.0.0.1"),
		Port:            r.Get("PORT", "8080"),
		CognitoIssuer:   r.Require("COGNITO_ISSUER"),
		CognitoClientID: r.Require("COGNITO_CLIENT_ID"),
		CognitoJWKSURL:  r.Get("COGNITO_JWKS_URL", ""),
		CognitoEndpoint: r.Get("COGNITO_ENDPOINT", "http://localhost:9229"),
		UsersURL:        r.Get("USERS_SERVICE_URL", "http://127.0.0.1:8082"),
		CategoriesURL:   r.Get("CATEGORIES_SERVICE_URL", "http://127.0.0.1:8083"),
	}
	for _, o := range strings.Split(r.Get("CORS_ORIGINS", "http://localhost:8081"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	if err := r.Err(); err != nil {
		return Config{}, fmt.Errorf("%w (rode `source <infra-base>/local/.env.cognito` antes)", err)
	}
	for nome, e := range map[string]string{
		"COGNITO_ISSUER": c.CognitoIssuer, "COGNITO_ENDPOINT": c.CognitoEndpoint, "USERS_SERVICE_URL": c.UsersURL,
		"CATEGORIES_SERVICE_URL": c.CategoriesURL,
	} {
		if err := exigirLocal(nome, e); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func exigirLocal(nome, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s inválido: %q", nome, endpoint)
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "cognito", "host.docker.internal":
		return nil
	}
	return fmt.Errorf("%s %q não é local; o cmd/local só roda contra o ambiente local", nome, endpoint)
}

// NewHandler monta o BFF a partir da configuração.
func NewHandler(c Config) (http.Handler, error) {
	log := logging.New(logging.Options{Service: servico, Env: c.Base.Env, Level: c.Base.LogLevel})

	tokens, err := auth.NewValidator(auth.Config{Issuer: c.CognitoIssuer, ClientID: c.CognitoClientID, JWKSURL: c.CognitoJWKSURL})
	if err != nil {
		return nil, err
	}
	transporte, err := invoke.NewTransport(c.Base.Runtime, c.UsersURL, nil, nil)
	if err != nil {
		return nil, err
	}
	transporteCategorias, err := invoke.NewTransport(c.Base.Runtime, c.CategoriesURL, nil, nil)
	if err != nil {
		return nil, err
	}
	cognitoAPI := cip.New(cip.Options{
		Region:       c.Base.Region,
		BaseEndpoint: aws.String(c.CognitoEndpoint),
		Credentials:  aws.AnonymousCredentials{},
	})

	return api.New(api.Deps{
		Tokens:      tokens,
		Me:          me.New(usuarios.New(transporte), cognito.New(cognitoAPI)),
		Categorias:  categorias.New(transporteCategorias),
		Log:         log,
		CORSOrigins: c.CORSOrigins,
	}), nil
}

func main() {
	if err := run(context.Background(), os.LookupEnv); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupFunc) error {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return err
	}
	h, err := NewHandler(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	erro := make(chan error, 1)
	go func() { erro <- srv.ListenAndServe() }()
	fmt.Printf("BFF local em http://%s (users-service: %s, categories-service: %s, cognito: %s)\n",
		srv.Addr, cfg.UsersURL, cfg.CategoriesURL, cfg.CognitoEndpoint)

	select {
	case err := <-erro:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
