package api

import (
	"cmp"
	"net/http"
	"slices"

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

// fatia é a Fatia do openapi. Categorias trazem categoriaId e cor; grupos
// trazem grupo.
type fatia struct {
	CategoriaID       string `json:"categoriaId,omitempty"`
	Grupo             string `json:"grupo,omitempty"`
	Nome              string `json:"nome"`
	Cor               string `json:"cor,omitempty"`
	TotalCentavos     int64  `json:"totalCentavos"`
	PercentualDecimos int    `json:"percentualDecimos"`
}

// pizza são as quatro visões do gráfico (TelaDashboard.pizza).
type pizza struct {
	Todas  []fatia `json:"todas"`
	Cartao []fatia `json:"CARTAO"`
	Fixo   []fatia `json:"FIXO"`
	Grupos []fatia `json:"grupos"`
}

// telaDashboard é o TelaDashboard do openapi.
type telaDashboard struct {
	Mes            string                   `json:"mes"`
	TotalCentavos  int64                    `json:"totalCentavos"`
	TotaisPorGrupo []expcontract.TotalGrupo `json:"totaisPorGrupo"`
	Pizza          pizza                    `json:"pizza"`
	Ultimos        []gastoOut               `json:"ultimos"`
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
	err := emParalelo(
		func() (err error) { res, err = a.deps.Gastos.ResumoMensal(r.Context(), c.UserID, mes); return err },
		func() (err error) { cats, err = a.deps.Categorias.Listar(r.Context(), c.UserID); return err },
	)
	if err != nil {
		a.erro(w, r, err)
		return
	}
	escreverJSON(w, http.StatusOK, montarDashboard(mes, res, porID(cats)))
}

// montarDashboard junta o resumo do expenses-service às categorias e calcula
// os percentuais de cada visão da pizza pelo maior resto.
func montarDashboard(mes string, res expcontract.ResumoMensalOut, cats map[string]catcontract.Categoria) telaDashboard {
	tela := telaDashboard{
		Mes:           mes,
		TotalCentavos: res.TotalCentavos,
		// Sempre CARTAO e FIXO, nessa ordem, a partir do que o serviço
		// mandou: um grupo ausente sai zerado.
		TotaisPorGrupo: make([]expcontract.TotalGrupo, 0, 2),
		Pizza: pizza{
			Todas:  fatiasDeCategorias(res.TotaisPorCategoria, "", cats),
			Cartao: fatiasDeCategorias(res.TotaisPorCategoria, catcontract.GrupoCartao, cats),
			Fixo:   fatiasDeCategorias(res.TotaisPorCategoria, catcontract.GrupoFixo, cats),
			Grupos: []fatia{},
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
			tela.Pizza.Grupos = append(tela.Pizza.Grupos, fatia{Grupo: grupo, Nome: rotuloGrupo[grupo], TotalCentavos: total})
		}
	}
	ordenarEPercentuar(tela.Pizza.Grupos)
	for _, g := range res.Recentes {
		tela.Ultimos = append(tela.Ultimos, resolver(g, cats))
	}
	return tela
}

// fatiasDeCategorias monta a visão de um grupo, ou de todos com grupo vazio.
// Na visão "todas", uma categoria que aparecer nos dois grupos vira uma fatia
// só. Categoria que não está mais entre as visíveis aparece como "Removida".
func fatiasDeCategorias(totais []expcontract.TotalCategoria, grupo string, cats map[string]catcontract.Categoria) []fatia {
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
		f := fatia{CategoriaID: t.CategoriaID, Nome: nomeCategoriaRemovida, Cor: corCategoriaRemovida, TotalCentavos: t.TotalCentavos}
		if c, ok := cats[t.CategoriaID]; ok {
			f.Nome, f.Cor = c.Nome, c.Cor
		}
		pos[t.CategoriaID] = len(out)
		out = append(out, f)
	}
	ordenarEPercentuar(out)
	return out
}

// ordenarEPercentuar põe as fatias em ordem decrescente de valor (empates
// mantêm a ordem recebida) e preenche os percentuais, que somam 1000 décimos.
func ordenarEPercentuar(fs []fatia) {
	slices.SortStableFunc(fs, func(a, b fatia) int { return cmp.Compare(b.TotalCentavos, a.TotalCentavos) })
	valores := make([]int64, len(fs))
	for i, f := range fs {
		valores[i] = f.TotalCentavos
	}
	for i, p := range painel.Decimos(valores) {
		fs[i].PercentualDecimos = p
	}
}
