package lambdahttp

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func evento(metodo, caminho string) events.APIGatewayV2HTTPRequest {
	ev := events.APIGatewayV2HTTPRequest{
		Version: "2.0",
		RawPath: caminho,
		Headers: map[string]string{"authorization": "Bearer abc", "host": "api.exemplo"},
	}
	ev.RequestContext.HTTP.Method = metodo
	ev.RequestContext.HTTP.SourceIP = "203.0.113.1"
	ev.RequestContext.DomainName = "api.exemplo"
	return ev
}

func TestHandlerRepassaRequisicaoEResposta(t *testing.T) {
	ev := evento(http.MethodPost, "/gastos/abc")
	ev.RawQueryString = "mes=2026-11&grupo=moradia"
	ev.Body = `{"valorCentavos":1999}`
	ev.Cookies = []string{"a=1", "b=2"}

	var visto *http.Request
	var corpo string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto = r
		b, _ := io.ReadAll(r.Body)
		corpo = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "x=1")
		w.Header().Add("Vary", "Origin")
		w.Header().Add("Vary", "Accept")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	out, err := Handler(h)(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if visto.Method != http.MethodPost || visto.URL.Path != "/gastos/abc" || visto.URL.Query().Get("grupo") != "moradia" {
		t.Errorf("requisição = %s %s", visto.Method, visto.URL)
	}
	if visto.Header.Get("Authorization") != "Bearer abc" || visto.Header.Get("Cookie") != "a=1; b=2" {
		t.Errorf("headers = %v", visto.Header)
	}
	if visto.Host != "api.exemplo" || visto.RemoteAddr != "203.0.113.1" || visto.ContentLength != int64(len(ev.Body)) {
		t.Errorf("host/remote/len = %s %s %d", visto.Host, visto.RemoteAddr, visto.ContentLength)
	}
	if corpo != ev.Body {
		t.Errorf("corpo = %q", corpo)
	}
	if out.StatusCode != http.StatusCreated || out.Body != `{"ok":true}` || out.IsBase64Encoded {
		t.Errorf("resposta = %+v", out)
	}
	if out.Headers["Content-Type"] != "application/json" || out.Headers["Vary"] != "Origin,Accept" {
		t.Errorf("headers da resposta = %v", out.Headers)
	}
	if len(out.Cookies) != 1 || out.Cookies[0] != "x=1" {
		t.Errorf("cookies = %v", out.Cookies)
	}
	if _, ok := out.Headers["Set-Cookie"]; ok {
		t.Error("Set-Cookie deveria ir em Cookies")
	}
}

func TestCorpoBase64(t *testing.T) {
	ev := evento(http.MethodPut, "/gastos/1")
	ev.Body = base64.StdEncoding.EncodeToString([]byte("olá"))
	ev.IsBase64Encoded = true

	var corpo string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		corpo = string(b)
		_, _ = w.Write([]byte{0xff, 0xfe, 0x00})
	})
	out, err := Handler(h)(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if corpo != "olá" {
		t.Errorf("corpo = %q", corpo)
	}
	if out.StatusCode != http.StatusOK || !out.IsBase64Encoded || out.Body != base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0x00}) {
		t.Errorf("resposta = %+v", out)
	}
}

func TestSemCorpoNemStatus(t *testing.T) {
	ev := evento(http.MethodGet, "")
	delete(ev.Headers, "host")
	var visto *http.Request
	out, err := Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { visto = r }))(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if visto.URL.Path != "/" || visto.Host != "api.exemplo" {
		t.Errorf("path/host = %q %q", visto.URL.Path, visto.Host)
	}
	if out.StatusCode != http.StatusOK || out.Body != "" {
		t.Errorf("resposta = %+v", out)
	}
}

func TestPrimeiroStatusVale(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.WriteHeader(http.StatusOK)
	})
	out, err := Handler(h)(context.Background(), evento(http.MethodGet, "/me"))
	if err != nil || out.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, %v", out.StatusCode, err)
	}
}

func TestEventoInvalido(t *testing.T) {
	casos := map[string]func(ev *events.APIGatewayV2HTTPRequest){
		"sem método":      func(ev *events.APIGatewayV2HTTPRequest) { ev.RequestContext.HTTP.Method = "" },
		"base64 inválido": func(ev *events.APIGatewayV2HTTPRequest) { ev.Body, ev.IsBase64Encoded = "%%%", true },
		"método inválido": func(ev *events.APIGatewayV2HTTPRequest) { ev.RequestContext.HTTP.Method = "GE T" },
	}
	for nome, mudar := range casos {
		t.Run(nome, func(t *testing.T) {
			ev := evento(http.MethodGet, "/me")
			mudar(&ev)
			chamou := false
			_, err := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { chamou = true }))(context.Background(), ev)
			if err == nil || !strings.HasPrefix(err.Error(), "lambdahttp:") || chamou {
				t.Errorf("err = %v, chamou = %v", err, chamou)
			}
		})
	}
}
