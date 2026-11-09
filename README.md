# gestor-gastos-bff

BFF do Gestor de Gastos (Go, Lambda, OpenAPI). É a única API pública: entrega ao app os dados prontos de cada tela e delega aos serviços por Lambda Invoke (ADR 0004).

## Contrato

O [`openapi.yaml`](openapi.yaml) é a fonte da verdade da API. O frontend gera os tipos com `orval` a partir dele, numa versão fixada.

| Rota | Tela ou uso |
| --- | --- |
| `GET /me` | Perfil; cria o perfil sob demanda se ainda não existir |
| `GET /telas/dashboard?mes=` | Painel: totais, pizza nas 4 visões e últimos 5 gastos |
| `GET /telas/gastos?mes=&grupo=&categoriaId=&cursor=` | Lista de gastos paginada, com as categorias para os filtros |
| `GET /telas/gasto-form?id=` | Formulário de gasto; com `id`, traz o gasto para edição |
| `POST /gastos`, `PUT /gastos/{id}`, `DELETE /gastos/{id}` | Lançar, editar e excluir gasto |
| `GET /categorias`, `POST /categorias`, `DELETE /categorias/{id}` | Categorias padrão e personalizadas |
| `GET /admin/telas/usuarios` | Lista de usuários (admin) |
| `PATCH /admin/usuarios/{id}`, `DELETE /admin/usuarios/{id}` | Bloquear, reativar, alterar papel e excluir (admin) |

Convenções (detalhes no `info.description` do contrato):

- **Valores:** em centavos inteiros (`valorCentavos`).
- **Percentuais:** em décimos inteiros (`percentualDecimos`: 750 = 75,0%), que somam exatamente 1000 em cada visão.
- **Datas:** `AAAA-MM-DD`; mês `AAAA-MM`.
- **Paginação:** por cursor opaco.
- **Erros:** `application/problem+json`, com `code` estável e `errors[]` nos 422.

## Lint

```sh
npx -y @stoplight/spectral-cli@6.17.1 lint openapi.yaml --fail-severity=warn
```

O [`.spectral.yaml`](.spectral.yaml) estende as regras padrão de OpenAPI e acrescenta duas regras do projeto:

- toda resposta 4xx e 5xx com corpo usa `application/problem+json`;
- todo campo `*Centavos` é `integer` `int64`.

A esteira roda o lint em todo PR.
