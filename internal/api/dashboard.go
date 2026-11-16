package api

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-bff/internal/painel"
	catcontract "github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	expcontract "github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
)

// rotuloGrupo é o nome das fatias da visão por grupo, o mesmo que o app usa
// nas outras telas.
var rotuloGrupo = map[string]string{
	catcontract.GrupoCartao: "Cartão",
	catcontract.GrupoFixo:   "Fixo",
}

// Rótulo e cor dos gastos sem cartão nos totais, na visão por cartão e no
// detalhe das fatias.
const (
	rotuloSemCartao = "Sem cartão"
	corSemCartao    = "#9E9E9E"
)

// parteFatia é a ParteFatia do openapi: um pedaço do detalhe de uma fatia.
type parteFatia struct {
	Rotulo        string `json:"rotulo"`
	Cor           string `json:"cor,omitempty"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// fatia é a Fatia do openapi. Categorias trazem categoriaId, grupo e cor;
// grupos trazem grupo; cartões trazem cartaoId (ausente em "Sem cartão") e
// cor.
type fatia struct {
	CategoriaID       string       `json:"categoriaId,omitempty"`
	CartaoID          string       `json:"cartaoId,omitempty"`
	Grupo             string       `json:"grupo,omitempty"`
	Nome              string       `json:"nome"`
	Cor               string       `json:"cor,omitempty"`
	TotalCentavos     int64        `json:"totalCentavos"`
	PercentualDecimos int          `json:"percentualDecimos"`
	Detalhe           []parteFatia `json:"detalhe"`
}

// pizza são as cinco visões do gráfico (TelaDashboard.pizza).
type pizza struct {
	Todas   []fatia `json:"todas"`
	Cartao  []fatia `json:"CARTAO"`
	Fixo    []fatia `json:"FIXO"`
	Grupos  []fatia `json:"grupos"`
	Cartoes []fatia `json:"cartoes"`
}

// totalCartao é o TotalCartao do openapi.
type totalCartao struct {
	CartaoID      string `json:"cartaoId,omitempty"`
	Nome          string `json:"nome"`
	Cor           string `json:"cor,omitempty"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// telaDashboard é o TelaDashboard do openapi.
type telaDashboard struct {
	Mes             string                   `json:"mes"`
	TotalCentavos   int64                    `json:"totalCentavos"`
	TotaisPorGrupo  []expcontract.TotalGrupo `json:"totaisPorGrupo"`
	TotaisPorCartao []totalCartao            `json:"totaisPorCartao"`
	Pizza           pizza                    `json:"pizza"`
	Ultimos         []gastoOut               `json:"ultimos"`
}

func (a *api) telaDashboard(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	mes := r.URL.Query().Get("mes")
	if !padraoMes.MatchString(mes) {
		a.erro(w, r, parametroInvalido("mes (AAAA-MM)"))
		return
	}
	var res expcontract.ResumoMensalOut
	var cats catcontract.CategoriasPorGrupo
	var cartoes []expcontract.Cartao
	err := emParalelo(
		func() (err error) { res, err = a.deps.Gastos.ResumoMensal(r.Context(), c.UserID, mes); return err },
		func() (err error) { cats, err = a.deps.Categorias.Listar(r.Context(), c.UserID); return err },
		func() (err error) { cartoes, err = a.deps.Gastos.ListarCartoes(r.Context(), c.UserID); return err },
	)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, montarDashboard(mes, res, novasReferencias(cats, cartoes)))
}

// nomeECorDoCartao devolve o rótulo e a cor de um cartão nos totais e no
// detalhe; "" é "Sem cartão".
func (ref referencias) nomeECorDoCartao(id string) (string, string) {
	if id == "" {
		return rotuloSemCartao, corSemCartao
	}
	k := ref.cartao(id)
	return k.Apelido, k.Cor
}

// montarDashboard junta o resumo do expenses-service às categorias e aos
// cartões e calcula os percentuais de cada visão da pizza pelo maior resto.
func montarDashboard(mes string, res expcontract.ResumoMensalOut, ref referencias) telaDashboard {
	por := res.TotaisPorCategoriaECartao
	tela := telaDashboard{
		Mes:           mes,
		TotalCentavos: res.TotalCentavos,
		// Sempre CARTAO e FIXO, nessa ordem, a partir do que o serviço
		// mandou: um grupo ausente sai zerado.
		TotaisPorGrupo:  make([]expcontract.TotalGrupo, 0, 2),
		TotaisPorCartao: make([]totalCartao, 0, len(res.TotaisPorCartao)),
		Pizza: pizza{
			Todas:   fatiasDeCategorias(res.TotaisPorCategoria, "", ref, por),
			Cartao:  fatiasDeCategorias(res.TotaisPorCategoria, catcontract.GrupoCartao, ref, por),
			Fixo:    fatiasDeCategorias(res.TotaisPorCategoria, catcontract.GrupoFixo, ref, por),
			Grupos:  []fatia{},
			Cartoes: []fatia{},
		},
		Ultimos: make([]gastoOut, 0, len(res.Recentes)),
	}
	for _, grupo := range []string{catcontract.GrupoCartao, catcontract.GrupoFixo} {
		var total int64
		for _, t := range res.TotaisPorGrupo {
			if t.Grupo == grupo {
				total += t.TotalCentavos
			}
		}
		tela.TotaisPorGrupo = append(tela.TotaisPorGrupo, expcontract.TotalGrupo{Grupo: grupo, TotalCentavos: total})
		if total > 0 {
			tela.Pizza.Grupos = append(tela.Pizza.Grupos, fatia{
				Grupo: grupo, Nome: rotuloGrupo[grupo], TotalCentavos: total,
				Detalhe: detalhePorCartao(por, func(t expcontract.TotalCategoriaCartao) bool { return t.Grupo == grupo }, ref),
			})
		}
	}
	ordenarEPercentuar(tela.Pizza.Grupos)

	for _, t := range res.TotaisPorCartao {
		nome, cor := ref.nomeECorDoCartao(t.CartaoID)
		tela.TotaisPorCartao = append(tela.TotaisPorCartao, totalCartao{CartaoID: t.CartaoID, Nome: nome, Cor: cor, TotalCentavos: t.TotalCentavos})
		if t.TotalCentavos > 0 {
			cartao := t.CartaoID
			tela.Pizza.Cartoes = append(tela.Pizza.Cartoes, fatia{
				CartaoID: cartao, Nome: nome, Cor: cor, TotalCentavos: t.TotalCentavos,
				Detalhe: detalhePorGrupo(por, cartao),
			})
		}
	}
	ordenarEPercentuar(tela.Pizza.Cartoes)

	for _, g := range res.Recentes {
		tela.Ultimos = append(tela.Ultimos, ref.resolver(g))
	}
	return tela
}

// fatiasDeCategorias monta a visão de um grupo, ou de todos com grupo vazio.
// Na visão "todas", uma categoria que aparecer nos dois grupos vira uma fatia
// só, com o grupo do primeiro total (o maior, na ordem do serviço). Categoria que não está mais entre as
// visíveis aparece como "Removida". O detalhe divide a fatia por cartão.
func fatiasDeCategorias(totais []expcontract.TotalCategoria, grupo string, ref referencias, por []expcontract.TotalCategoriaCartao) []fatia {
	out := []fatia{}
	pos := map[string]int{}
	for _, t := range totais {
		if t.TotalCentavos <= 0 || (grupo != "" && t.Grupo != grupo) {
			continue
		}
		if i, ok := pos[t.CategoriaID]; ok {
			out[i].TotalCentavos += t.TotalCentavos
			continue
		}
		f := fatia{CategoriaID: t.CategoriaID, Grupo: t.Grupo, Nome: nomeCategoriaRemovida, Cor: corCategoriaRemovida, TotalCentavos: t.TotalCentavos}
		if c, ok := ref.categorias[t.CategoriaID]; ok {
			f.Nome, f.Cor = c.Nome, c.Cor
		}
		pos[t.CategoriaID] = len(out)
		out = append(out, f)
	}
	for i := range out {
		id := out[i].CategoriaID
		out[i].Detalhe = detalhePorCartao(por, func(t expcontract.TotalCategoriaCartao) bool {
			return t.CategoriaID == id && (grupo == "" || t.Grupo == grupo)
		}, ref)
	}
	ordenarEPercentuar(out)
	return out
}

// detalhePorCartao soma por cartão os totais aceitos pelo filtro.
func detalhePorCartao(por []expcontract.TotalCategoriaCartao, aceita func(expcontract.TotalCategoriaCartao) bool, ref referencias) []parteFatia {
	soma := map[string]int64{}
	for _, t := range por {
		if aceita(t) {
			soma[t.CartaoID] += t.TotalCentavos
		}
	}
	partes := make([]parteFatia, 0, len(soma))
	for id, total := range soma {
		if total <= 0 {
			continue
		}
		nome, cor := ref.nomeECorDoCartao(id)
		partes = append(partes, parteFatia{Rotulo: nome, Cor: cor, TotalCentavos: total})
	}
	return ordenarPartes(partes)
}

// detalhePorGrupo divide o total de um cartão entre os grupos.
func detalhePorGrupo(por []expcontract.TotalCategoriaCartao, cartaoID string) []parteFatia {
	soma := map[string]int64{}
	for _, t := range por {
		if t.CartaoID == cartaoID {
			soma[t.Grupo] += t.TotalCentavos
		}
	}
	partes := []parteFatia{}
	for _, grupo := range []string{catcontract.GrupoCartao, catcontract.GrupoFixo} {
		if soma[grupo] > 0 {
			partes = append(partes, parteFatia{Rotulo: rotuloGrupo[grupo], TotalCentavos: soma[grupo]})
		}
	}
	return ordenarPartes(partes)
}

// ordenarPartes põe as partes do maior valor para o menor; empates pelo
// rótulo.
func ordenarPartes(partes []parteFatia) []parteFatia {
	slices.SortFunc(partes, func(a, b parteFatia) int {
		return cmp.Or(cmp.Compare(b.TotalCentavos, a.TotalCentavos), strings.Compare(a.Rotulo, b.Rotulo))
	})
	return partes
}

// ordenarEPercentuar põe as fatias em ordem decrescente de valor (empates
// mantêm a ordem recebida) e preenche os percentuais, que somam 1000 décimos.
func ordenarEPercentuar(fs []fatia) {
	slices.SortStableFunc(fs, func(a, b fatia) int { return cmp.Compare(b.TotalCentavos, a.TotalCentavos) })
	valores := make([]int64, len(fs))
	for i, f := range fs {
		valores[i] = f.TotalCentavos
		if fs[i].Detalhe == nil {
			fs[i].Detalhe = []parteFatia{}
		}
	}
	for i, p := range painel.Decimos(valores) {
		fs[i].PercentualDecimos = p
	}
}
