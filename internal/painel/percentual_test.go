package painel

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func soma(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func TestDecimos(t *testing.T) {
	casos := []struct {
		nome    string
		valores []int64
		quero   []int
	}{
		// Cenário "Participação por categoria": R$ 750,00 e R$ 250,00.
		{"exatos", []int64{75_000, 25_000}, []int{750, 250}},
		{"um só", []int64{4590}, []int{1000}},
		// 333,33... cada: o décimo que sobra vai ao primeiro (empate total).
		{"terços", []int64{1, 1, 1}, []int{334, 333, 333}},
		// Restos: 666,66 e 333,33 → a parte inteira dá 999; o maior resto
		// (0,66) leva o décimo.
		{"dois terços", []int64{2, 1}, []int{667, 333}},
		// 1/7 = 142,857...; 6/7 = 857,142... → 142 + 857 = 999; o maior
		// resto é o de 1/7 (0,857).
		{"sétimos", []int64{6, 1}, []int{857, 143}},
		// Empate de resto com valores diferentes não acontece com os mesmos
		// totais; com valores iguais, ganha quem vem antes.
		{"seis iguais", []int64{5, 5, 5, 5, 5, 5}, []int{167, 167, 167, 167, 166, 166}},
		{"fatia minúscula", []int64{99_999_999, 1}, []int{1000, 0}},
		{"com zero", []int64{0, 10, 0}, []int{0, 1000, 0}},
		{"negativo conta como zero", []int64{-5, 10}, []int{0, 1000}},
	}
	for _, c := range casos {
		got := Decimos(c.valores)
		if !slices.Equal(got, c.quero) {
			t.Errorf("%s: Decimos(%v) = %v, quero %v", c.nome, c.valores, got, c.quero)
		}
	}
}

func TestDecimosVazio(t *testing.T) {
	for _, vs := range [][]int64{nil, {}, {0, 0}} {
		got := Decimos(vs)
		if len(got) != len(vs) || soma(got) != 0 {
			t.Errorf("Decimos(%v) = %v", vs, got)
		}
	}
}

func TestDecimosSempreSomamMil(t *testing.T) {
	// Cenário "Percentuais somam 100%", com valores aleatórios em centavos.
	r := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		vs := make([]int64, 1+r.IntN(12))
		for i := range vs {
			vs[i] = 1 + r.Int64N(100_000_000)
		}
		got := Decimos(vs)
		if soma(got) != Escala {
			t.Fatalf("Decimos(%v) = %v, soma %d", vs, got, soma(got))
		}
		// Ninguém se afasta mais de um décimo da cota exata.
		var total int64
		for _, v := range vs {
			total += v
		}
		for i, v := range vs {
			exato := float64(v) * Escala / float64(total)
			if d := float64(got[i]) - exato; d <= -1 || d >= 1 {
				t.Fatalf("Decimos(%v)[%d] = %d, cota %.3f", vs, i, got[i], exato)
			}
		}
	}
}
