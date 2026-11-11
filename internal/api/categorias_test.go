package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/authtest"
	catcontract "github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// fakeCategorias imita o client do categories-service e guarda a última
// chamada.
type fakeCategorias struct {
	lista   catcontract.CategoriasPorGrupo
	err     error
	chamada string
	userID  string
	id      string
	in      catcontract.CriarCategoriaIn
}

func (f *fakeCategorias) Listar(_ context.Context, userID string) (catcontract.CategoriasPorGrupo, error) {
	f.chamada, f.userID = "Listar", userID
	return f.lista, f.err
}

// Obter procura na lista, como o serviço: fora dela é 404.
func (f *fakeCategorias) Obter(_ context.Context, userID, id string) (catcontract.Categoria, error) {
	f.chamada, f.userID, f.id = "Obter", userID, id
	if f.err != nil {
		return catcontract.Categoria{}, f.err
	}
	for _, c := range append(f.lista.Cartao, f.lista.Fixo...) {
		if c.ID == id {
			return c, nil
		}
	}
	return catcontract.Categoria{}, errs.NotFound(catcontract.CodigoNaoEncontrada, "Categoria não encontrada.")
}

func (f *fakeCategorias) Criar(_ context.Context, userID string, in catcontract.CriarCategoriaIn) (catcontract.Categoria, error) {
	f.chamada, f.userID, f.in = "Criar", userID, in
	if f.err != nil {
		return catcontract.Categoria{}, f.err
	}
	return catcontract.Categoria{ID: "c-1", Nome: in.Nome, Grupo: in.Grupo, Cor: in.Cor}, nil
}

func (f *fakeCategorias) Excluir(_ context.Context, userID, id string) error {
	f.chamada, f.userID, f.id = "Excluir", userID, id
	return f.err
}

func listaPadrao() catcontract.CategoriasPorGrupo {
	return catcontract.CategoriasPorGrupo{
		Cartao: []catcontract.Categoria{
			{ID: "alimentacao", Nome: "Alimentação", Grupo: "CARTAO", Cor: "#E4572E", Padrao: true},
			{ID: "c-1", Nome: "Pet", Grupo: "CARTAO", Cor: "#17BEBB"},
		},
		Fixo: []catcontract.Categoria{
			{ID: "moradia", Nome: "Moradia", Grupo: "FIXO", Cor: "#4C6EF5", Padrao: true},
		},
	}
}

func montarCategorias(t *testing.T, cats *fakeCategorias) (http.Handler, string) {
	t.Helper()
	emissor := authtest.New(t)
	v, err := auth.NewValidator(auth.Config{Issuer: emissor.Issuer, ClientID: authtest.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Tokens: v, Me: &fakeMe{}, Categorias: cats,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return h, emissor.Token(t, "u-1", authtest.Opcoes{Grupos: []string{"cliente"}})
}

func comCorpo(h http.Handler, method, path, token, corpo string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(corpo))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return req, rec
}

func TestListarCategorias(t *testing.T) {
	cats := &fakeCategorias{lista: listaPadrao()}
	h, tok := montarCategorias(t, cats)

	req, rec := fazer(h, http.MethodGet, "/categorias", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if cats.chamada != "Listar" || cats.userID != "u-1" {
		t.Errorf("chamada = %s para %q", cats.chamada, cats.userID)
	}
	var got catcontract.CategoriasPorGrupo
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Cartao) != 2 || len(got.Fixo) != 1 || !got.Cartao[0].Padrao || got.Cartao[1].Padrao {
		t.Errorf("corpo = %s", rec.Body.String())
	}
}

func TestCriarCategoria(t *testing.T) {
	cats := &fakeCategorias{}
	h, tok := montarCategorias(t, cats)

	req, rec := comCorpo(h, http.MethodPost, "/categorias", tok, `{"nome":"Pet","grupo":"CARTAO","cor":"#17BEBB"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	want := catcontract.CriarCategoriaIn{Nome: "Pet", Grupo: "CARTAO", Cor: "#17BEBB"}
	if cats.chamada != "Criar" || cats.userID != "u-1" || cats.in != want {
		t.Errorf("chamada = %s %q %+v", cats.chamada, cats.userID, cats.in)
	}
}

func TestCriarCategoriaCorpoInvalido(t *testing.T) {
	casos := map[string]string{
		"vazio":              ``,
		"não é JSON":         `nome=Pet`,
		"campo a mais":       `{"nome":"Pet","grupo":"CARTAO","cor":"#17BEBB","padrao":true}`,
		"tipo errado":        `{"nome":1,"grupo":"CARTAO","cor":"#17BEBB"}`,
		"dois objetos":       `{"nome":"Pet","grupo":"CARTAO","cor":"#17BEBB"}{}`,
		"grande demais":      `{"nome":"` + strings.Repeat("a", maxCorpo) + `","grupo":"CARTAO","cor":"#17BEBB"}`,
		"array em vez de {}": `[]`,
	}
	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			cats := &fakeCategorias{}
			h, tok := montarCategorias(t, cats)
			_, rec := comCorpo(h, http.MethodPost, "/categorias", tok, corpo)
			if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "corpo_invalido" {
				t.Errorf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if cats.chamada != "" {
				t.Errorf("o serviço não devia ser chamado (%s)", cats.chamada)
			}
		})
	}
}

// Os erros do categories-service chegam ao cliente com o status e o código
// do contrato.
func TestErrosDoCategoriesService(t *testing.T) {
	casos := []struct {
		nome, method, path, corpo string
		err                       error
		status                    int
		code                      string
	}{
		{"nome duplicado", http.MethodPost, "/categorias", `{"nome":"pet","grupo":"CARTAO","cor":"#17BEBB"}`,
			errs.Conflict(catcontract.CodigoDuplicada, "Já existe uma categoria com esse nome no grupo."), 409, catcontract.CodigoDuplicada},
		{"validação", http.MethodPost, "/categorias", `{"nome":"","grupo":"OUTRO","cor":"azul"}`,
			errs.Validation("Dados da categoria inválidos.",
				errs.FieldError{Field: "nome", Message: "obrigatório"},
				errs.FieldError{Field: "grupo", Message: "deve ser CARTAO ou FIXO"},
				errs.FieldError{Field: "cor", Message: "deve estar no formato #RRGGBB"}), 422, "validacao"},
		{"excluir padrão", http.MethodDelete, "/categorias/alimentacao", "",
			errs.Forbidden(catcontract.CodigoPadrao, "Categoria padrão não pode ser excluída."), 403, catcontract.CodigoPadrao},
		{"excluir de outro usuário", http.MethodDelete, "/categorias/c-9", "",
			errs.NotFound(catcontract.CodigoNaoEncontrada, "Categoria não encontrada."), 404, catcontract.CodigoNaoEncontrada},
		{"serviço fora do ar", http.MethodGet, "/categorias", "",
			errs.Internal(io.ErrUnexpectedEOF), 500, "erro_interno"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			h, tok := montarCategorias(t, &fakeCategorias{err: c.err})
			req, rec := comCorpo(h, c.method, c.path, tok, c.corpo)
			if rec.Code != c.status || problema(t, rec).Code != c.code {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			conforme(t, req, rec)
			if c.status == 422 && len(problema(t, rec).Errors) != 3 {
				t.Errorf("errors[] = %s", rec.Body.String())
			}
			if c.status == 500 && strings.Contains(rec.Body.String(), "EOF") {
				t.Errorf("causa interna vazou: %s", rec.Body.String())
			}
		})
	}
}

func TestExcluirCategoria(t *testing.T) {
	cats := &fakeCategorias{}
	h, tok := montarCategorias(t, cats)

	req, rec := fazer(h, http.MethodDelete, "/categorias/c-1", tok)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	if cats.chamada != "Excluir" || cats.userID != "u-1" || cats.id != "c-1" {
		t.Errorf("chamada = %s %q %q", cats.chamada, cats.userID, cats.id)
	}
}

func TestExcluirCategoriaIDLongo(t *testing.T) {
	cats := &fakeCategorias{}
	h, tok := montarCategorias(t, cats)
	_, rec := fazer(h, http.MethodDelete, "/categorias/"+strings.Repeat("x", maxID+1), tok)
	if rec.Code != http.StatusBadRequest || problema(t, rec).Code != "id_invalido" || cats.chamada != "" {
		t.Errorf("status = %d: %s (chamada %q)", rec.Code, rec.Body.String(), cats.chamada)
	}
}

func TestTelaGastoForm(t *testing.T) {
	cats := &fakeCategorias{lista: listaPadrao()}
	h, tok := montarCategorias(t, cats)

	req, rec := fazer(h, http.MethodGet, "/telas/gasto-form", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	conforme(t, req, rec)
	var got struct {
		Categorias catcontract.CategoriasPorGrupo `json:"categorias"`
		Gasto      json.RawMessage                `json:"gasto"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Categorias.Cartao) != 2 || got.Gasto != nil || cats.userID != "u-1" {
		t.Errorf("corpo = %s", rec.Body.String())
	}

	// Sem o expenses-service (Lambda antes da 8.5), a edição segue em 501.
	_, rec = fazer(h, http.MethodGet, "/telas/gasto-form?id=g-1", tok)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("com id: status = %d, quer 501", rec.Code)
	}
}

func TestCategoriasExigemToken(t *testing.T) {
	cats := &fakeCategorias{lista: listaPadrao()}
	h, _ := montarCategorias(t, cats)
	for _, rota := range rotasCategorias {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.Replace(path, "{id}", "c-1", 1)
		req, rec := fazer(h, method, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, quer 401", rota, rec.Code)
		}
		conforme(t, req, rec)
	}
	if cats.chamada != "" {
		t.Errorf("o serviço não devia ser chamado sem token (%s)", cats.chamada)
	}
}

// Sem o categories-service (Lambda antes da 7.6), as rotas seguem em 501.
func TestCategoriasSemServico(t *testing.T) {
	h, emissor, _ := montar(t, &fakeMe{})
	tok := emissor.Token(t, "u-1", authtest.Opcoes{})
	for _, rota := range rotasCategorias {
		method, path, _ := strings.Cut(rota, " ")
		path = strings.Replace(path, "{id}", "c-1", 1)
		_, rec := fazer(h, method, path, tok)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, quer 501", rota, rec.Code)
		}
	}
}
