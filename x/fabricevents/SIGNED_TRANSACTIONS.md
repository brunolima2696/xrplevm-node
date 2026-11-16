# Transações EVM assinadas via Fabric / IBC

A aplicação conecta o processador à recepção de pacotes versão 3 na porta
`fabric-events`. Apenas pacotes versão 3 são aceitos. Não há publicação em
mempool ou chamada de rede durante o consenso.
O canal continua usando `fabric-events-1`; não é preciso outro tipo de handshake.
Por padrão o construtor isolado é desativado. A aplicação o habilita, mas nenhum
pagamento é aceito sem um client Fabric autorizado pela configuração on-chain.

Use `exrpd tx fabric-events update-config --allowed-client fabric-msp-N ...`
por governança. O limite de
gas da configuração vale por transação interna e é limitado a 3M pelo node.

`SignedTransactionProcessor.Process(ctx, rawTx, idPedido)` recebe bytes Ethereum
assinados, recupera o remetente e utiliza verificações do ante-handler Cosmos EVM
antes de chamar `ApplyTransaction`. Não aceita um remetente fornecido pela VP e
não debita saldos diretamente.

- Tipos suportados: legacy protegido por EIP-155, access-list e EIP-1559.
- Verifica assinatura, chain ID, nonce, saldo, intrinsic gas, taxas/base fee,
  limite de bloco e políticas de criação/chamada da EVM.
- EIP-7702 e blobs são rejeitados nesta etapa. Limites padrão: 4096 bytes e 1M gas.
- `idPedido` é opcional. Se informado com `data` de 32 bytes, ambos devem coincidir.
  Com `data` vazio, o identificador externo não é protegido pela assinatura.
- A rejeição de validação não modifica saldo ou nonce da Alice.
- Sucesso e revert são tentativas executadas: consomem nonce e taxas. O revert
  descarta as alterações de valor/storage/logs pela rotina EVM existente.
- Erros internos ou orçamento insuficiente retornam erro e descartam o cache.
  Não devem ser convertidos em confirmação de pagamento.
- A rotina não reinicia o gas meter externo. O relayer paga pelo trabalho da
  transação IBC e Alice paga as taxas EVM assinadas: são cobranças distintas.
- Os efeitos ainda dependem do commit da transação SDK externa. Se ela falhar,
  todos os efeitos internos também são revertidos.

## Pacote / VP

O chaincode `cc_ibc.SendSignedTransaction(channel, vpJSON, timeoutNS)` recebe:

```json
{
  "@context": ["https://www.w3.org/ns/credentials/v2"],
  "type": ["VerifiablePresentation"],
  "payment": {
    "id_pedido": "0x<64 dígitos hex minúsculos>",
    "raw_transaction": "0x<transação Ethereum completa, já assinada>"
  }
}
```

É um envelope VP, não uma verificação genérica de credenciais W3C. A autorização
financeira é a assinatura Ethereum interna. O Fabric verifica estrutura, tamanho,
identidade do solicitante para retries e correlação do pedido; a XRPLEVM verifica
a assinatura e os parâmetros reais da transação. Nenhuma chave privada viaja na VP.

O pacote contém `version:3`, `source_chain_id`, `action:executeTransaction`,
`id_pedido` e `vp`. JSON com duplicatas, campos desconhecidos, conteúdo extra ou
VP inválida é rejeitado. A VP é limitada a 10KB e a transação assinada a 4096 bytes.

`id_pedido` pode ser inserido em `data` antes da assinatura. Com `data` de 32 bytes,
o módulo exige igualdade. Sem esse vínculo, o ID externo não é autenticado pela
assinatura Ethereum. O nonce EVM permanece obrigatório e independente do ID.
O timeout IBC não faz a transação Ethereum expirar: quem possui os bytes pode
publicá-la por outra entrada enquanto o nonce/saldo/taxas ainda a permitirem.

## Resultado e replay

O ACK de resultado contém `id_pedido`, `status` (`success`, `reverted`, `rejected`),
`hash` quando decodificável, `reason` e `gas_used`. Um ACK IBC de sucesso significa
que o relatório foi entregue, não necessariamente que o pagamento teve sucesso.
Erros de origem/configuração/integridade usam ACK de erro. Erros internos e falta
de orçamento não confirmam pagamento e não consomem saldo ou nonce da Alice.

O Fabric persiste o relatório em `QuerySignedTransaction(id_pedido)`. O campo
externo `status:confirmed` indica ACK recebido; consulte `result.status` para
saber se a transferência aconteceu. Novas tentativas precisam usar a mesma
identidade Fabric, canal e transação. Só são admitidas após erro/timeout ou uma
rejeição confirmada. Não é permitido substituir a transação de um pedido.

Na XRPLEVM, sucesso e revert reservam o ID para os bytes/origem executados e
persistem o registro por hash. Repetição idêntica devolve o resultado anterior sem
executar novamente. Outro pedido não pode executar o mesmo hash duas vezes, e o
nonce também impede replay por entradas externas. Rejeições não reservam o ID nem
consomem nonce e podem ser reavaliadas se as condições mudarem.

## Consulta

O registro contém `result.input`, `raw_transaction`, remetente, destinatário,
gas usado, preço efetivo, logs, altura e o hash da transação IBC externa.
É estado persistido da chain, mas **não é uma transação independente na lista
Ethereum do bloco**. Não são fabricados recibos ou índices para o RPC `eth`.

```bash
exrpd query fabric-events transaction 0x<HASH_EVM> --node tcp://localhost:26657
exrpd query fabric-events payment fabric_chain_0 0x<ID_PEDIDO> --node tcp://localhost:26657
cast rpc fabric_getTransactionByHash 0x<HASH_EVM> --rpc-url http://127.0.0.1:8545
cast rpc fabric_getTransactionResult 0x<HASH_EVM> --rpc-url http://127.0.0.1:8545
```

O namespace read-only `fabric` é habilitado no `local-node.sh` e na configuração
padrão do binário. Se iniciar manualmente, acrescente `fabric` a `--json-rpc.api`.
Para consultar a transação Cosmos externa, use seu `outer_tx_hash` com `query tx`.
`cast tx`/`cast receipt` (RPC `eth`) não substituem as consultas acima para a
transação interna. Consulta por ID retorna operações executadas; rejeições com
hash são consultáveis por hash, e o Fabric mantém seu resultado no ACK.

A validação end-to-end com a rede real deve ser executada localmente. Os testes
unitários usam as verificações reais de entrada e doubles para o executor EVM
nos testes de recepção/rollback, sem declarar que substituem essa validação.
