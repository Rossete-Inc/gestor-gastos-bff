package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func valido() map[string]string {
	return map[string]string{
		"RUNTIME":           "local",
		"COGNITO_ISSUER":    "http://localhost:9229/local_abc",
		"COGNITO_CLIENT_ID": "client",
	}
}

func TestLoadConfigPadroes(t *testing.T) {
	c, err := LoadConfig(env(valido()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "127.0.0.1" || c.Port != "8080" || c.UsersURL != "http://127.0.0.1:8082" || c.CategoriesURL != "http://127.0.0.1:8083" || c.ExpensesURL != "http://127.0.0.1:8084" ||
		c.CognitoEndpoint != "http://localhost:9229" || len(c.CORSOrigins) != 1 || c.CORSOrigins[0] != "http://localhost:8081" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadConfigCORS(t *testing.T) {
	m := valido()
	m["CORS_ORIGINS"] = " http://localhost:8081 , http://192.168.0.10:8081 ,"
	c, err := LoadConfig(env(m))
	if err != nil || len(c.CORSOrigins) != 2 || c.CORSOrigins[1] != "http://192.168.0.10:8081" {
		t.Errorf("CORS = %v, %v", c.CORSOrigins, err)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]func(m map[string]string){
		"sem RUNTIME": func(m map[string]string) { delete(m, "RUNTIME") },
		"RUNTIME=aws": func(m map[string]string) { m["RUNTIME"], m["ENV"] = "aws", "dev" },
		"sem issuer":  func(m map[string]string) { delete(m, "COGNITO_ISSUER") },
		"sem client":  func(m map[string]string) { delete(m, "COGNITO_CLIENT_ID") },
		"Cognito da AWS": func(m map[string]string) {
			m["COGNITO_ISSUER"] = "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_x"
		},
		"users-service remoto": func(m map[string]string) { m["USERS_SERVICE_URL"] = "https://exemplo.com" },
		"categories-service remoto": func(m map[string]string) {
			m["CATEGORIES_SERVICE_URL"] = "https://exemplo.com"
		},
		"expenses-service remoto": func(m map[string]string) {
			m["EXPENSES_SERVICE_URL"] = "https://exemplo.com"
		},
		"endpoint inválido": func(m map[string]string) { m["COGNITO_ENDPOINT"] = "localhost:9229" },
	}
	for nome, mudar := range casos {
		t.Run(nome, func(t *testing.T) {
			m := valido()
			mudar(m)
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
}

func TestNewHandler(t *testing.T) {
	c, err := LoadConfig(env(valido()))
	if err != nil {
		t.Fatal(err)
	}
	if h, err := NewHandler(c); err != nil || h == nil {
		t.Errorf("NewHandler = %v, %v", h, err)
	}
}

func TestRunConfigInvalida(t *testing.T) {
	if err := run(context.Background(), env(map[string]string{})); err == nil {
		t.Error("esperava erro")
	}
}

func TestRunSobeEDesliga(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, porta, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()

	m := valido()
	m["PORT"] = porta
	m["LOG_LEVEL"] = "error"
	ctx, cancel := context.WithCancel(context.Background())
	terminou := make(chan error, 1)
	go func() { terminou <- run(ctx, env(m)) }()

	// Sem token: 401 sem precisar do cognito-local, que só é consultado com
	// um token presente.
	var status int
	for range 50 {
		if resp, err := http.Get("http://127.0.0.1:" + porta + "/me"); err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("GET /me sem token = %d, quer 401", status)
	}

	cancel()
	select {
	case err := <-terminou:
		if err != nil {
			t.Errorf("encerramento: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("o servidor não encerrou")
	}
}

func TestRunPortaOcupada(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, porta, _ := net.SplitHostPort(l.Addr().String())
	m := valido()
	m["PORT"] = porta
	if err := run(context.Background(), env(m)); err == nil {
		t.Error("esperava erro com a porta ocupada")
	}
}
