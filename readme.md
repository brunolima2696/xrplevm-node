# XRPL EVM Sidechain Node — Fork

Este repositório é um fork do [`xrplevm/node`](https://github.com/xrplevm/node), adaptado para os testes de interoperabilidade do projeto.

## Light client Besu/QBFT

O fork incorpora um light client IBC para redes Besu com consenso QBFT, compatível com IBC-Go v10. A implementação permite que a XRPL EVM Sidechain:

- registre o cliente `hb-qbft`;
- valide cabeçalhos e assinaturas do conjunto de validadores QBFT;
- mantenha os estados de cliente e consenso necessários pelo IBC.

O código está em [`lightclients/besu-qbft`](lightclients/besu-qbft).

## Light client Fabric/MSP

O fork também incorpora um light client IBC para redes Hyperledger Fabric. A implementação permite que a XRPL EVM Sidechain:

- registre o cliente `fabric-msp`;
- valide identidades, certificados e políticas de endosso MSP;
- verifique compromissos IBC presentes nos read-write sets endossados pelo chaincode.

O código está em [`lightclients/fabric-msp`](lightclients/fabric-msp).

## Aplicação Fabric Events

O módulo [`x/fabricevents`](x/fabricevents) recebe pacotes IBC na porta
`fabric-events`. No fluxo de pagamento, recebe uma VP contendo o `id_pedido` e uma
transação EVM já assinada, valida a autorização do client Fabric e encaminha a
transação ao processamento EVM nativo. Assinatura, chain ID, nonce, saldo e taxas
são validados antes da execução; o resultado e o hash EVM retornam ao Fabric pelo
acknowledgement.

Os clients Fabric permitidos e o limite de gas são parâmetros on-chain. Sem
clients autorizados, o processamento permanece desabilitado. Apenas pacotes de
transações assinadas (versão 3) são aceitos.

`exrpd tx fabric-events update-config` submete uma proposta de governança para
alterá-los. A alteração só entra em vigor após aprovação e execução da proposta;
não exige recriar a chain. `--generate-message` apenas imprime a mensagem para
uma proposta, sem transmitir transação. Consulte os parâmetros com
`exrpd query fabric-events config` e a autoridade com
`exrpd query fabric-events authority`.

A execução ocorre dentro da transação IBC, não como uma transação Ethereum
independente. Os registros podem ser consultados por
`exrpd query fabric-events transaction <hash>` ou pelos métodos JSON-RPC
`fabric_getTransactionByHash` e `fabric_getTransactionResult`, com o namespace
`fabric` habilitado. Detalhes em
[`x/fabricevents/SIGNED_TRANSACTIONS.md`](x/fabricevents/SIGNED_TRANSACTIONS.md).
