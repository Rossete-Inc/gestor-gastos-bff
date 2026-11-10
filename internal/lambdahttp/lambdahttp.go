// Package lambdahttp liga um http.Handler à Lambda atrás da HTTP API (payload
// 2.0). Assim, o mesmo handler da internal/api serve o cmd/local e a AWS:
// muda só o transporte.
package lambdahttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/events"
)

// Func é a assinatura aceita por lambda.Start.
type Func func(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)

// Handler converte o evento da HTTP API numa *http.Request, chama h e
// converte a resposta. Erro no retorno só para evento inválido: respostas de
// erro do BFF (4xx, 5xx) são respostas normais.
func Handler(h http.Handler) Func {
	return func(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		r, err := Request(ctx, ev)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{}, err
		}
		w := &resposta{header: http.Header{}}
		h.ServeHTTP(w, r)
		return w.evento(), nil
	}
}

// Request monta a requisição HTTP a partir do evento.
func Request(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (*http.Request, error) {
	corpo := []byte(ev.Body)
	if ev.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(ev.Body)
		if err != nil {
			return nil, fmt.Errorf("lambdahttp: corpo base64 inválido: %w", err)
		}
		corpo = b
	}

	caminho := ev.RawPath
	if caminho == "" {
		caminho = "/"
	}
	alvo := caminho
	if ev.RawQueryString != "" {
		alvo += "?" + ev.RawQueryString
	}
	metodo := ev.RequestContext.HTTP.Method
	if metodo == "" {
		return nil, errors.New("lambdahttp: evento sem método HTTP")
	}

	r, err := http.NewRequestWithContext(ctx, metodo, alvo, bytes.NewReader(corpo))
	if err != nil {
		return nil, fmt.Errorf("lambdahttp: requisição inválida: %w", err)
	}
	// O payload 2.0 junta valores repetidos com vírgula, que é equivalente
	// para os headers que o BFF lê.
	for k, v := range ev.Headers {
		r.Header.Set(k, v)
	}
	if len(ev.Cookies) > 0 {
		r.Header.Set("Cookie", strings.Join(ev.Cookies, "; "))
	}
	r.Host = r.Header.Get("Host")
	if r.Host == "" {
		r.Host = ev.RequestContext.DomainName
	}
	r.RemoteAddr = ev.RequestContext.HTTP.SourceIP
	r.RequestURI = alvo
	r.ContentLength = int64(len(corpo))
	return r, nil
}

// resposta acumula status, headers e corpo do handler.
type resposta struct {
	header http.Header
	status int
	corpo  bytes.Buffer
}

func (w *resposta) Header() http.Header { return w.header }

func (w *resposta) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}

func (w *resposta) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.corpo.Write(b)
}

func (w *resposta) evento() events.APIGatewayV2HTTPResponse {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	out := events.APIGatewayV2HTTPResponse{StatusCode: w.status, Headers: map[string]string{}}
	for k, v := range w.header {
		if http.CanonicalHeaderKey(k) == "Set-Cookie" {
			out.Cookies = append(out.Cookies, v...)
			continue
		}
		out.Headers[k] = strings.Join(v, ",")
	}
	// JSON e problem+json são texto; qualquer outra coisa que não seja UTF-8
	// vai em base64 para não corromper.
	if b := w.corpo.Bytes(); utf8.Valid(b) {
		out.Body = string(b)
	} else {
		out.Body = base64.StdEncoding.EncodeToString(b)
		out.IsBase64Encoded = true
	}
	return out
}
