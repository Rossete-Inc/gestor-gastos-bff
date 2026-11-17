package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/auth"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-users-service/contract"
)

// Conta ativa (ADR 0010, spec colaboracao). O app diz em qual conta trabalha
// no cabeçalho X-Conta; sem ele, ou com o próprio sub, vale a própria conta.
// Numa conta alheia o BFF confere o acesso no users-service e chama o
// expenses-service e o categories-service com o userId do dono: os serviços
// continuam vendo uma partição só.

// CabecalhoConta é o cabeçalho com o donoId da conta ativa.
const CabecalhoConta = "X-Conta"

// ttlAcesso é quanto um acesso confirmado fica em cache. A remoção de um
// colaborador e a troca de papel valem em até esse prazo (design.md).
const ttlAcesso = 10 * time.Second

// contaAtiva é a conta em que a requisição age.
type contaAtiva struct {
	// DonoID é a partição dos dados: o userId enviado aos serviços.
	DonoID string
	// Papel é DONO, EDITOR ou LEITOR.
	Papel string
	// AutorID é quem age (o sub do token), gravado como autor dos gastos.
	AutorID string
}

type chaveConta struct{}

// contaDe devolve a conta ativa posta por naConta.
func contaDe(ctx context.Context) contaAtiva {
	c, _ := ctx.Value(chaveConta{}).(contaAtiva)
	return c
}

var (
	errContaNaoEncontrada = errs.NotFound(contract.CodigoContaNaoEncontrada, "Conta não encontrada.")
	errSomenteLeitura     = errs.Forbidden("somente_leitura", "Você só pode consultar esta conta.")
	errSomenteDono        = errs.Forbidden("somente_dono", "Só o dono da conta pode gerenciar os colaboradores.")
)

// naConta autentica e resolve a conta ativa. Conta alheia sem acesso é 404,
// sem revelar se ela existe; escrita de um Leitor é 403.
func (a *api) naConta(next http.Handler) http.Handler {
	return a.autenticado(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := auth.FromContext(r.Context())
		conta, err := a.resolverConta(r.Context(), c.UserID, strings.TrimSpace(r.Header.Get(CabecalhoConta)))
		if err != nil {
			a.erro(w, r, err)
			return
		}
		if conta.Papel == contract.PapelLeitor && r.Method != http.MethodGet {
			a.erro(w, r, errSomenteLeitura)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chaveConta{}, conta)))
	}))
}

// propriaConta autentica e exige que a conta ativa seja a do próprio
// usuário: a gestão dos colaboradores é só do dono.
func (a *api) propriaConta(next http.Handler) http.Handler {
	return a.autenticado(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := auth.FromContext(r.Context())
		if x := strings.TrimSpace(r.Header.Get(CabecalhoConta)); x != "" && x != c.UserID {
			a.erro(w, r, errSomenteDono)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *api) resolverConta(ctx context.Context, userID, donoID string) (contaAtiva, error) {
	if donoID == "" || donoID == userID {
		return contaAtiva{DonoID: userID, Papel: contract.PapelDono, AutorID: userID}, nil
	}
	if a.deps.Colaboracao == nil || len(donoID) > maxID {
		return contaAtiva{}, errContaNaoEncontrada
	}
	if papel, ok := a.acessos.obter(userID, donoID, a.agora()); ok {
		return contaAtiva{DonoID: donoID, Papel: papel, AutorID: userID}, nil
	}
	acesso, err := a.deps.Colaboracao.ObterAcesso(ctx, userID, donoID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return contaAtiva{}, errContaNaoEncontrada
		}
		return contaAtiva{}, err
	}
	a.acessos.guardar(userID, donoID, acesso.Papel, a.agora())
	return contaAtiva{DonoID: donoID, Papel: acesso.Papel, AutorID: userID}, nil
}

// agora é o relógio da API; Deps.Agora fixa a hora nos testes.
func (a *api) agora() time.Time {
	if a.deps.Agora != nil {
		return a.deps.Agora()
	}
	return time.Now()
}

// cacheAcessos guarda, por (usuário, dono), o papel confirmado. Só as
// respostas positivas: quem não é membro sempre consulta.
type cacheAcessos struct {
	mu    sync.Mutex
	itens map[[2]string]acessoEmCache
}

type acessoEmCache struct {
	papel  string
	expira time.Time
}

func (c *cacheAcessos) obter(userID, donoID string, agora time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := [2]string{userID, donoID}
	it, ok := c.itens[k]
	if !ok {
		return "", false
	}
	if !agora.Before(it.expira) {
		delete(c.itens, k)
		return "", false
	}
	return it.papel, true
}

func (c *cacheAcessos) guardar(userID, donoID, papel string, agora time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.itens == nil {
		c.itens = map[[2]string]acessoEmCache{}
	}
	c.itens[[2]string{userID, donoID}] = acessoEmCache{papel: papel, expira: agora.Add(ttlAcesso)}
}

// esquecer tira o acesso do cache desta instância, para a saída e a remoção
// valerem na hora nela; nas demais, em até ttlAcesso.
func (c *cacheAcessos) esquecer(userID, donoID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.itens, [2]string{userID, donoID})
}
