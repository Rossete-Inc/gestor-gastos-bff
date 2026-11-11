// Package painel tem as contas do painel que ficam no BFF (design.md): os
// percentuais das fatias da pizza pelo método do maior resto, para que
// somem exatamente 100% em cada visão.
package painel

import (
	"cmp"
	"slices"
)

// Escala é o total dos percentuais de uma visão: décimos de ponto percentual
// (openapi: Fatia.percentualDecimos), então 1000 = 100,0%.
const Escala = 1000

// Decimos reparte Escala entre os valores, proporcionalmente, pelo método do
// maior resto (Hamilton): cada valor recebe a parte inteira da sua cota, e o
// que falta para Escala vai, um décimo por vez, aos maiores restos. Empate
// de resto: primeiro o maior valor, depois o que vem antes na lista. A
// resposta tem o mesmo tamanho e a mesma ordem de valores.
//
// Valores negativos contam como zero. Se a soma for zero (lista vazia ou só
// zeros), todos ficam com zero.
func Decimos(valores []int64) []int {
	out := make([]int, len(valores))
	var total int64
	for _, v := range valores {
		total += max(v, 0)
	}
	if total == 0 {
		return out
	}

	type parte struct {
		i     int
		valor int64
		resto int64
	}
	partes := make([]parte, len(valores))
	distribuido := 0
	for i, v := range valores {
		v = max(v, 0)
		// v*Escala não estoura: o maior mês possível no contrato fica muito
		// abaixo de 9,2e18 / 1000 centavos.
		cota := v * Escala
		out[i] = int(cota / total)
		distribuido += out[i]
		partes[i] = parte{i: i, valor: v, resto: cota % total}
	}

	slices.SortStableFunc(partes, func(a, b parte) int {
		return cmp.Or(cmp.Compare(b.resto, a.resto), cmp.Compare(b.valor, a.valor))
	})
	for k := 0; k < Escala-distribuido; k++ {
		out[partes[k].i]++
	}
	return out
}
